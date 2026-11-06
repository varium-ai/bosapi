package infrastructure

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"bosapi/backend/internal/admin/domain"
)

func securityEventReadTestDatabase(t *testing.T) (*SecurityEventRepository, sqlmock.Sqlmock) {
	t.Helper()
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mock.ExpectClose()
		if err := sqlDatabase.Close(); err != nil {
			t.Error(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	return NewSecurityEventRepository(database), mock
}

func TestCountUnreadSecurityEventsScopesWarningsAndReceipts(t *testing.T) {
	for _, test := range []struct {
		name         string
		organization uint64
		count        int64
	}{
		{name: "first tenant visit includes historical warnings", organization: 9, count: 3},
		{name: "acknowledged tenant warnings", organization: 9},
		{name: "new tenant warning after acknowledgement", organization: 9, count: 1},
		{name: "late smaller ID without receipt remains unread", organization: 9, count: 1},
		{name: "global admin historical warnings", count: 7},
		{name: "global receipts do not share tenant receipts", count: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, mock := securityEventReadTestDatabase(t)
			scope := domain.SecurityEventReadScope{UserID: 7, OrganizationID: test.organization}
			query := "SELECT count(*) FROM `security_events` WHERE severity IN (?,?,?) AND NOT EXISTS (SELECT 1 FROM `security_event_reads` WHERE user_id = ? AND organization_id = ? AND event_id = security_events.id)"
			args := []driver.Value{"warning", "high", "critical", 7, test.organization}
			if test.organization > 0 {
				query = "SELECT count(*) FROM `security_events` WHERE severity IN (?,?,?) AND organization_id = ? AND NOT EXISTS (SELECT 1 FROM `security_event_reads` WHERE user_id = ? AND organization_id = ? AND event_id = security_events.id)"
				args = []driver.Value{"warning", "high", "critical", test.organization, 7, test.organization}
			}
			mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(args...).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(test.count))
			count, err := repository.CountUnreadSecurityEvents(context.Background(), scope)
			if err != nil || count != test.count {
				t.Fatalf("count=%d error=%v want=%d", count, err, test.count)
			}
		})
	}
}

func TestCountUnreadSecurityEventsPropagatesFailures(t *testing.T) {
	repository, mock := securityEventReadTestDatabase(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `security_events` WHERE severity IN (?,?,?) AND organization_id = ? AND NOT EXISTS (SELECT 1 FROM `security_event_reads` WHERE user_id = ? AND organization_id = ? AND event_id = security_events.id)")).
		WithArgs("warning", "high", "critical", 9, 7, 9).WillReturnError(context.Canceled)
	_, err := repository.CountUnreadSecurityEvents(context.Background(), domain.SecurityEventReadScope{UserID: 7, OrganizationID: 9})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestCachedSecurityUnreadInvalidatesOnlyAfterCommittedAcknowledgement(t *testing.T) {
	base, mock := securityEventReadTestDatabase(t)
	repository := NewCachedSecurityEventRepository(base.database)
	scope := domain.SecurityEventReadScope{UserID: 7, OrganizationID: 9}
	ctx := context.Background()
	mock.ExpectQuery("SELECT count.*FROM `security_events`").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	for range 2 {
		if count, err := repository.CountUnreadSecurityEvents(ctx, scope); err != nil || count != 3 {
			t.Fatalf("count=%d error=%v", count, err)
		}
	}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO security_event_reads").WithArgs(7, 9, 7, 9, "warning", "high", "critical", 9).WillReturnError(context.Canceled)
	mock.ExpectRollback()
	if err := repository.MarkSecurityEventsRead(ctx, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if count, err := repository.CountUnreadSecurityEvents(ctx, scope); err != nil || count != 3 {
		t.Fatalf("rollback count=%d error=%v", count, err)
	}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO security_event_reads").WithArgs(7, 9, 7, 9, "warning", "high", "critical", 9).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()
	if err := repository.MarkSecurityEventsRead(ctx, scope); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT count.*FROM `security_events`").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	if count, err := repository.CountUnreadSecurityEvents(ctx, scope); err != nil || count != 1 {
		t.Fatalf("late-event count=%d error=%v", count, err)
	}
}

func TestMarkSecurityEventsReadAcknowledgesOnlyVisibleScopedEvents(t *testing.T) {
	for _, test := range []struct {
		name         string
		organization uint64
	}{
		{name: "tenant warnings", organization: 9},
		{name: "global warnings"},
		{name: "empty tenant", organization: 9},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, mock := securityEventReadTestDatabase(t)
			query := "INSERT INTO security_event_reads (user_id, organization_id, event_id) SELECT ?, ?, events.id FROM security_events AS events LEFT JOIN security_event_reads AS acknowledged ON acknowledged.user_id = ? AND acknowledged.organization_id = ? AND acknowledged.event_id = events.id WHERE events.severity IN (?,?,?) AND acknowledged.event_id IS NULL ON DUPLICATE KEY UPDATE event_id = security_event_reads.event_id"
			args := []driver.Value{7, test.organization, 7, test.organization, "warning", "high", "critical"}
			if test.organization > 0 {
				query = "INSERT INTO security_event_reads (user_id, organization_id, event_id) SELECT ?, ?, events.id FROM security_events AS events LEFT JOIN security_event_reads AS acknowledged ON acknowledged.user_id = ? AND acknowledged.organization_id = ? AND acknowledged.event_id = events.id WHERE events.severity IN (?,?,?) AND events.organization_id = ? AND acknowledged.event_id IS NULL ON DUPLICATE KEY UPDATE event_id = security_event_reads.event_id"
				args = append(args, test.organization)
			}
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta(query)).WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			if err := repository.MarkSecurityEventsRead(context.Background(), domain.SecurityEventReadScope{UserID: 7, OrganizationID: test.organization}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMarkSecurityEventsReadPropagatesFailures(t *testing.T) {
	for _, phase := range []string{"begin", "insert", "commit"} {
		t.Run(phase, func(t *testing.T) {
			repository, mock := securityEventReadTestDatabase(t)
			begin := mock.ExpectBegin()
			if phase == "begin" {
				begin.WillReturnError(context.Canceled)
			} else {
				insert := mock.ExpectExec("INSERT INTO security_event_reads").WithArgs(7, 9, 7, 9, "warning", "high", "critical", 9)
				if phase == "insert" {
					insert.WillReturnError(context.Canceled)
					mock.ExpectRollback()
				} else {
					insert.WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit().WillReturnError(context.Canceled)
				}
			}
			err := repository.MarkSecurityEventsRead(context.Background(), domain.SecurityEventReadScope{UserID: 7, OrganizationID: 9})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSecurityEventsMinimumSeverityPreservesExactFilter(t *testing.T) {
	for _, test := range []struct {
		minimum, exact string
		levels         []driver.Value
	}{
		{minimum: "info", levels: []driver.Value{"info", "warning", "high", "critical"}},
		{minimum: "warning", levels: []driver.Value{"warning", "high", "critical"}},
		{minimum: "high", levels: []driver.Value{"high", "critical"}},
		{minimum: "critical", levels: []driver.Value{"critical"}},
		{minimum: "warning", exact: "high", levels: []driver.Value{"high", "warning", "high", "critical"}},
		{minimum: "warning' OR 1=1 --", levels: []driver.Value{}},
	} {
		t.Run(test.minimum+test.exact, func(t *testing.T) {
			repository, mock := securityEventReadTestDatabase(t)
			where := " FROM `security_events` WHERE severity IN ("
			if test.exact != "" {
				where = " FROM `security_events` WHERE severity = ? AND severity IN ("
			}
			switch test.minimum {
			case "info":
				where += "?,?,?,?)"
			case "warning":
				where += "?,?,?)"
			case "high":
				where += "?,?)"
			case "critical":
				where += "?)"
			default:
				where += "NULL)"
			}
			mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*)" + where)).WithArgs(test.levels...).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			args := append(append([]driver.Value{}, test.levels...), 20)
			mock.ExpectQuery("SELECT security_events\\.\\*" + regexp.QuoteMeta(where+" ORDER BY created_at DESC, id DESC LIMIT ?")).
				WithArgs(args...).WillReturnRows(sqlmock.NewRows([]string{"id"}))
			items, count, err := repository.ListSecurityEvents(context.Background(), domain.SecurityEventFilter{Severity: test.exact, MinSeverity: test.minimum}, 1, 20)
			if err != nil || count != 0 || len(items) != 0 {
				t.Fatalf("items=%v count=%d error=%v", items, count, err)
			}
		})
	}
}
