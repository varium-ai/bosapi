package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/migrations"
)

func securityNotificationMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	db := newAdminDashboardMySQL(t)
	initial, err := migrations.Files.ReadFile("001_initial_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"users", "security_events"} {
		start := strings.Index(string(initial), "CREATE TABLE IF NOT EXISTS "+name+" (")
		if start < 0 {
			t.Fatalf("missing table %s", name)
		}
		definition := string(initial[start:])
		definition = definition[:strings.Index(definition, ";")+1]
		if err := db.Exec(definition).Error; err != nil {
			t.Fatal(err)
		}
	}
	reads, err := migrations.Files.ReadFile("014_security_event_reads.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(reads)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO users (id, email, password_hash, organization_id, role, status)
	 VALUES (1, 'one@example.test', 'hash', 9, 'org_admin', 'active'),
	 (2, 'two@example.test', 'hash', 9, 'org_admin', 'active')`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSecurityEventReadMySQLSkipsReceiptsAndKeepsLateCommitsUnread(t *testing.T) {
	db := securityNotificationMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db = db.WithContext(ctx)
	if err := db.Exec(`INSERT INTO security_events (id, organization_id, severity, created_at) VALUES
	 (1, 9, 'warning', UTC_TIMESTAMP()), (2, 9, 'info', UTC_TIMESTAMP()),
	 (3, 10, 'critical', UTC_TIMESTAMP()), (4, 9, 'high', UTC_TIMESTAMP())`).Error; err != nil {
		t.Fatal(err)
	}
	late := db.Begin(&sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if late.Error != nil {
		t.Fatal(late.Error)
	}
	defer func() {
		if err := late.Rollback().Error; err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	if err := late.Exec("INSERT INTO security_events (id, organization_id, severity) VALUES (5, 9, 'warning')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO security_events (id, organization_id, severity) VALUES (6, 9, 'warning')").Error; err != nil {
		t.Fatal(err)
	}
	repository := NewSecurityEventRepository(db)
	scope := domain.SecurityEventReadScope{UserID: 1, OrganizationID: 9}
	assertCount := func(scope domain.SecurityEventReadScope, want int64) {
		t.Helper()
		if got, err := repository.CountUnreadSecurityEvents(ctx, scope); err != nil || got != want {
			t.Fatalf("scope=%+v count=%d want=%d error=%v", scope, got, want, err)
		}
	}
	assertCount(scope, 3)
	if err := repository.MarkSecurityEventsRead(ctx, scope); err != nil {
		t.Fatal(err)
	}
	assertCount(scope, 0)
	// Repeated acknowledgement must not update or lock the already-read receipts.
	if err := db.Exec(`CREATE TRIGGER reject_duplicate_receipt BEFORE UPDATE ON security_event_reads
	 FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'existing receipt was rewritten'`).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkSecurityEventsRead(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := late.Commit().Error; err != nil {
		t.Fatal(err)
	}
	assertCount(scope, 1)
	assertCount(domain.SecurityEventReadScope{UserID: 2, OrganizationID: 9}, 4)
	assertCount(domain.SecurityEventReadScope{UserID: 1}, 5)
	if err := repository.MarkSecurityEventsRead(ctx, scope); err != nil {
		t.Fatal(err)
	}
	assertCount(scope, 0)
	var receipts int64
	if err := db.Table("security_event_reads").Where("user_id = ? AND organization_id = ?", 1, 9).Count(&receipts).Error; err != nil || receipts != 4 {
		t.Fatalf("receipts=%d error=%v", receipts, err)
	}
}

func TestSecurityEventReadMySQLCachedCountInvalidatedAfterAcknowledgement(t *testing.T) {
	db := securityNotificationMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db = db.WithContext(ctx)
	if err := db.Exec("INSERT INTO security_events (organization_id, severity) VALUES (9, 'warning')").Error; err != nil {
		t.Fatal(err)
	}
	repository := NewCachedSecurityEventRepository(db)
	scope := domain.SecurityEventReadScope{UserID: 1, OrganizationID: 9}
	if count, err := repository.CountUnreadSecurityEvents(ctx, scope); err != nil || count != 1 {
		t.Fatalf("count=%d error=%v", count, err)
	}
	if err := repository.MarkSecurityEventsRead(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if count, err := repository.CountUnreadSecurityEvents(ctx, scope); err != nil || count != 0 {
		t.Fatalf("acknowledged count=%d error=%v", count, err)
	}
}
