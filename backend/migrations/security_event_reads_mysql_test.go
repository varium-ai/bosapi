package migrations

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/admin/infrastructure"
)

func TestSecurityEventReadReceiptsMySQLPreserveLateCommits(t *testing.T) {
	connection, ctx, repository := securityEventReadMySQLDatabase(t)
	pending := beginPendingSecurityEvent(t, ctx, connection)
	if _, err := pending.ExecContext(ctx, "INSERT INTO security_events (id, organization_id, severity) VALUES (?, ?, ?)", 100, 9, "warning"); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, "INSERT INTO security_events (id, organization_id, severity) VALUES (?, ?, ?)", 101, 9, "warning"); err != nil {
		t.Fatal(err)
	}
	scope := domain.SecurityEventReadScope{UserID: 7, OrganizationID: 9}
	markCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := repository.MarkSecurityEventsRead(markCtx, scope); err != nil {
		t.Fatalf("acknowledgement must not wait for an invisible event: %v", err)
	}
	assertUnreadSecurityEventCount(t, ctx, repository, scope, 0)
	assertSecurityEventReceiptCount(t, ctx, connection, scope, 1)
	if err := pending.Commit(); err != nil {
		t.Fatal(err)
	}
	assertUnreadSecurityEventCount(t, ctx, repository, scope, 1)
	if err := repository.MarkSecurityEventsRead(ctx, scope); err != nil {
		t.Fatal(err)
	}
	assertUnreadSecurityEventCount(t, ctx, repository, scope, 0)
	assertSecurityEventReceiptCount(t, ctx, connection, scope, 2)
}

func TestSecurityEventReadReceiptsMySQLIsolateScopesAndCascade(t *testing.T) {
	connection, ctx, repository := securityEventReadMySQLDatabase(t)
	seedSecurityEventReadScopes(t, ctx, connection)
	organization := domain.SecurityEventReadScope{UserID: 7, OrganizationID: 9}
	global := domain.SecurityEventReadScope{UserID: 7}
	assertUnreadSecurityEventCount(t, ctx, repository, organization, 3)
	assertUnreadSecurityEventCount(t, ctx, repository, global, 5)
	for range 2 {
		if err := repository.MarkSecurityEventsRead(ctx, organization); err != nil {
			t.Fatal(err)
		}
	}
	assertUnreadSecurityEventCount(t, ctx, repository, global, 5)
	for range 2 {
		if err := repository.MarkSecurityEventsRead(ctx, global); err != nil {
			t.Fatal(err)
		}
	}
	assertUnreadSecurityEventCount(t, ctx, repository, organization, 0)
	assertUnreadSecurityEventCount(t, ctx, repository, global, 0)
	assertSecurityEventReceiptCount(t, ctx, connection, organization, 3)
	assertSecurityEventReceiptCount(t, ctx, connection, global, 5)
	for _, test := range []struct {
		name  string
		scope domain.SecurityEventReadScope
		want  int64
	}{
		{"another administrator in the same organization", domain.SecurityEventReadScope{UserID: 8, OrganizationID: 9}, 3},
		{"another organization", domain.SecurityEventReadScope{UserID: 9, OrganizationID: 10}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertUnreadSecurityEventCount(t, ctx, repository, test.scope, test.want)
			assertSecurityEventReceiptCount(t, ctx, connection, test.scope, 0)
		})
	}
	assertSecurityEventReadCascades(t, ctx, connection, organization, global)
}

func securityEventReadMySQLDatabase(t *testing.T) (*sql.Conn, context.Context, *infrastructure.SecurityEventRepository) {
	t.Helper()
	connection, ctx := legacyBindingsTestDatabase(t)
	for _, name := range []string{"001_initial_schema.sql", "014_security_event_reads.sql"} {
		applySecurityEventSeverityScript(t, ctx, connection, name)
	}
	for _, user := range []struct {
		id, organizationID uint64
		email              string
	}{{7, 9, "first@example.invalid"}, {8, 9, "second@example.invalid"}, {9, 10, "third@example.invalid"}} {
		if _, err := connection.ExecContext(ctx, `INSERT INTO users
 (id, email, password_hash, organization_id, role, status)
 VALUES (?, ?, ?, ?, ?, ?)`, user.id, user.email, "test-only", user.organizationID, "org_admin", "active"); err != nil {
			t.Fatal(err)
		}
	}
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: connection, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	return connection, ctx, infrastructure.NewSecurityEventRepository(database)
}

func seedSecurityEventReadScopes(t *testing.T, ctx context.Context, connection *sql.Conn) {
	t.Helper()
	for index, event := range []struct {
		organizationID any
		severity       string
	}{{9, "info"}, {9, "warning"}, {9, "high"}, {9, "critical"}, {10, "info"}, {10, "warning"}, {nil, "warning"}} {
		if _, err := connection.ExecContext(ctx, "INSERT INTO security_events (id, organization_id, severity) VALUES (?, ?, ?)",
			index+1, event.organizationID, event.severity); err != nil {
			t.Fatal(err)
		}
	}
}

func assertSecurityEventReadCascades(t *testing.T, ctx context.Context, connection *sql.Conn, organization, global domain.SecurityEventReadScope) {
	t.Helper()
	if _, err := connection.ExecContext(ctx, "DELETE FROM security_events WHERE id = ?", 2); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM security_event_reads WHERE event_id = ?", 2).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted event receipt count=%d error=%v want=0", count, err)
	}
	assertSecurityEventReceiptCount(t, ctx, connection, organization, 2)
	assertSecurityEventReceiptCount(t, ctx, connection, global, 4)
	if _, err := connection.ExecContext(ctx, "DELETE FROM users WHERE id = ?", organization.UserID); err != nil {
		t.Fatal(err)
	}
	assertSecurityEventReceiptCount(t, ctx, connection, organization, 0)
	assertSecurityEventReceiptCount(t, ctx, connection, global, 0)
}

func beginPendingSecurityEvent(t *testing.T, ctx context.Context, connection *sql.Conn) *sql.Tx {
	t.Helper()
	var schema string
	if err := connection.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	config, err := drivermysql.ParseDSN(os.Getenv("BOS_MIGRATIONS_TEST_DSN"))
	if err != nil {
		t.Fatal("invalid BOS_MIGRATIONS_TEST_DSN")
	}
	config.DBName = schema
	connector, err := drivermysql.NewConnector(config)
	if err != nil {
		t.Fatal(err)
	}
	database := sql.OpenDB(connector)
	database.SetMaxOpenConns(2)
	database.SetMaxIdleConns(2)
	database.SetConnMaxLifetime(time.Minute)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := transaction.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	})
	return transaction
}

func assertSecurityEventReceiptCount(t *testing.T, ctx context.Context, connection *sql.Conn, scope domain.SecurityEventReadScope, want int64) {
	t.Helper()
	var count int64
	err := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM security_event_reads WHERE user_id = ? AND organization_id = ?",
		scope.UserID, scope.OrganizationID).Scan(&count)
	if err != nil || count != want {
		t.Fatalf("receipt count=%d error=%v want=%d", count, err, want)
	}
}

func assertUnreadSecurityEventCount(t *testing.T, ctx context.Context, repository *infrastructure.SecurityEventRepository, scope domain.SecurityEventReadScope, want int64) {
	t.Helper()
	count, err := repository.CountUnreadSecurityEvents(ctx, scope)
	if err != nil || count != want {
		t.Fatalf("unread count=%d error=%v want=%d", count, err, want)
	}
}
