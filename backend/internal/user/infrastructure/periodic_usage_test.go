package infrastructure

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"bosapi/backend/internal/user/domain"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestFindGatewayKeyPeriodicUsageUsesShanghaiBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, now, day, week, month, earliest string
	}{
		{"before midnight", "2026-10-12T15:59:59Z", "2026-10-11T16:00:00Z", "2026-10-11T16:00:00Z", "2026-09-30T16:00:00Z", "2026-09-30T16:00:00Z"},
		{"after midnight", "2026-10-12T16:00:00Z", "2026-10-12T16:00:00Z", "2026-10-11T16:00:00Z", "2026-09-30T16:00:00Z", "2026-09-30T16:00:00Z"},
		{"Monday", "2026-10-11T16:00:00Z", "2026-10-11T16:00:00Z", "2026-10-11T16:00:00Z", "2026-09-30T16:00:00Z", "2026-09-30T16:00:00Z"},
		{"week spans previous month", "2026-09-30T16:00:00Z", "2026-09-30T16:00:00Z", "2026-09-27T16:00:00Z", "2026-09-30T16:00:00Z", "2026-09-27T16:00:00Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, mock := quotaRepository(t)
			parse := func(value string) time.Time {
				parsed, err := time.Parse(time.RFC3339, value)
				if err != nil {
					t.Fatal(err)
				}
				return parsed
			}
			now, day, week, month := parse(test.now), parse(test.day), parse(test.week), parse(test.month)
			expectPeriodicGatewayKey(mock, now)
			mock.ExpectQuery(`SELECT api_key_id, .*FROM \(SELECT .*usage_daily_rollups FORCE INDEX .*UNION ALL SELECT .*usage_logs_v2 FORCE INDEX .*rollup_applied = 0.*UNION ALL SELECT .*usage_logs FORCE INDEX .*rollup_applied = 0`).
				WithArgs(periodicBoundaryArgs(7, day, week, month, parse(test.earliest))...).
				WillReturnRows(periodicRows().AddRow(7, 10, 0.5, 20, 1.5, 30, 2.5))
			key, found, err := repository.FindGatewayKey(context.Background(), "sk-gw-periodic-key", now)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if key.DailyUsedTokens != 10 || key.DailyUsedAmount != 0.5 || key.WeeklyUsedTokens != 20 || key.WeeklyUsedAmount != 1.5 || key.MonthlyUsedTokens != 30 || key.MonthlyUsedAmount != 2.5 {
				t.Fatalf("periodic usage not returned: %+v", key)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFindGatewayKeyRejectsUnavailablePeriodicUsage(t *testing.T) {
	repository, mock := quotaRepository(t)
	now := time.Now().UTC()
	expectPeriodicGatewayKey(mock, now)
	failure := errors.New("usage unavailable")
	mock.ExpectQuery(`SELECT api_key_id, .*FROM \(SELECT .*usage_daily_rollups FORCE INDEX .*UNION ALL SELECT .*usage_logs_v2 FORCE INDEX .*rollup_applied = 0.*UNION ALL SELECT .*usage_logs FORCE INDEX .*rollup_applied = 0`).WillReturnError(failure)
	if _, _, err := repository.FindGatewayKey(context.Background(), "sk-gw-periodic-key", now); !errors.Is(err, failure) {
		t.Fatalf("periodic usage failure must reject authentication: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFindActiveKeyByIDPopulatesPortalUsage(t *testing.T) {
	for _, limited := range []bool{false, true} {
		name := "without quotas"
		if limited {
			name = "with quotas"
		}
		t.Run(name, func(t *testing.T) {
			repository, mock := quotaRepository(t)
			now := time.Date(2026, 10, 14, 10, 15, 0, 0, time.UTC)
			expectActivePortalKey(mock, now, limited)
			day := time.Date(2026, 10, 13, 16, 0, 0, 0, time.UTC)
			week := time.Date(2026, 10, 11, 16, 0, 0, 0, time.UTC)
			month := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
			mock.ExpectQuery(`SELECT api_key_id, .*FROM \(SELECT .*usage_daily_rollups FORCE INDEX .*UNION ALL SELECT .*usage_logs_v2 FORCE INDEX .*rollup_applied = 0.*UNION ALL SELECT .*usage_logs FORCE INDEX .*rollup_applied = 0`).
				WithArgs(periodicBoundaryArgs(134, day, week, month, month)...).
				WillReturnRows(periodicRows().AddRow(134, 506476, 0.506476, 506476, 0.506476, 506476, 0.506476))
			key, found, err := repository.FindActiveKeyByID(context.Background(), 134, now)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			view := key.Redacted()
			if view.DailyUsedTokens != 506476 || view.WeeklyUsedTokens != 506476 || view.MonthlyUsedTokens != 506476 ||
				view.DailyUsedAmount != 0.506476 || view.WeeklyUsedAmount != 0.506476 || view.MonthlyUsedAmount != 0.506476 {
				t.Fatalf("portal periodic usage missing: %+v", view)
			}
			if view.QuotaUsedTokens != 506476 || view.QuotaUsedAmount != 0.506476 {
				t.Fatalf("total usage changed: %+v", view)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFindActiveKeyByIDRejectsUnavailablePortalUsage(t *testing.T) {
	repository, mock := quotaRepository(t)
	now := time.Now().UTC()
	expectActivePortalKey(mock, now, false)
	failure := errors.New("usage unavailable")
	mock.ExpectQuery(`SELECT api_key_id, .*FROM \(SELECT .*usage_daily_rollups FORCE INDEX .*UNION ALL SELECT .*usage_logs_v2 FORCE INDEX .*rollup_applied = 0.*UNION ALL SELECT .*usage_logs FORCE INDEX .*rollup_applied = 0`).WillReturnError(failure)
	if _, found, err := repository.FindActiveKeyByID(context.Background(), 134, now); found || !errors.Is(err, failure) {
		t.Fatalf("unavailable usage must not return fabricated zeroes: found=%v err=%v", found, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFindActiveKeyIdentityByIDSkipsUsageEvenWithQuotas(t *testing.T) {
	repository, mock := quotaRepository(t)
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	expectActivePortalKey(mock, now, true)
	key, found, err := repository.FindActiveKeyIdentityByID(context.Background(), 134, now)
	if err != nil || !found || key.ID != 134 || key.OrganizationID != 2 {
		t.Fatalf("identity=%+v found=%v error=%v", key, found, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFindActiveKeyIdentityByIDRejectsInvalidIdentityAndQueryFailures(t *testing.T) {
	for _, phase := range []string{"missing key", "key failure", "organization failure", "inactive organization", "invalid policy"} {
		t.Run(phase, func(t *testing.T) {
			repository, mock := quotaRepository(t)
			now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
			lookup := mock.ExpectQuery("FROM `api_keys` WHERE id = \\? AND status = \\?").WithArgs(uint64(7), "active", now, 1)
			switch phase {
			case "missing key":
				lookup.WillReturnError(gorm.ErrRecordNotFound)
			case "key failure":
				lookup.WillReturnError(context.Canceled)
			default:
				policy := "[]"
				if phase == "invalid policy" {
					policy = "invalid-json"
				}
				lookup.WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "allowed_models"}).AddRow(7, 9, policy))
				organization := mock.ExpectQuery("SELECT status FROM `organizations` WHERE id = \\?").WithArgs(uint64(9))
				if phase == "organization failure" {
					organization.WillReturnError(context.Canceled)
				} else {
					status := "active"
					if phase == "inactive organization" {
						status = "disabled"
					}
					organization.WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(status))
				}
			}
			_, found, err := repository.FindActiveKeyIdentityByID(context.Background(), 7, now)
			wantError := phase != "missing key" && phase != "inactive organization"
			if found || (err != nil) != wantError {
				t.Fatalf("found=%v error=%v", found, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func expectActivePortalKey(mock sqlmock.Sqlmock, now time.Time, limited bool) {
	var dailyQuota any
	if limited {
		dailyQuota = int64(10000)
	}
	mock.ExpectQuery("FROM `api_keys` WHERE id = \\? AND status = \\?").
		WithArgs(uint64(134), "active", now, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "status", "daily_quota_tokens", "quota_used_tokens", "quota_used_amount"}).
			AddRow(134, 2, "active", dailyQuota, 506476, 0.506476))
	mock.ExpectQuery("SELECT status FROM `organizations` WHERE id = \\?").WithArgs(uint64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))
}

func TestPeriodicUsageResetsWhenNewPeriodHasNoRows(t *testing.T) {
	repository, mock := quotaRepository(t)
	keys := []domain.APIKey{{ID: 7, DailyUsedTokens: 10, WeeklyUsedTokens: 20, MonthlyUsedTokens: 30, DailyUsedAmount: 1, WeeklyUsedAmount: 2, MonthlyUsedAmount: 3}}
	// A Monday which is also the first of the month resets all three periods.
	now := time.Date(2026, 5, 31, 16, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT api_key_id, .*FROM \(SELECT .*usage_daily_rollups FORCE INDEX .*UNION ALL SELECT .*usage_logs_v2 FORCE INDEX .*rollup_applied = 0.*UNION ALL SELECT .*usage_logs FORCE INDEX .*rollup_applied = 0`).
		WithArgs(periodicBoundaryArgs(7, now, now, now, now)...).
		WillReturnRows(periodicRows())
	if err := repository.populatePeriodicUsageAt(context.Background(), keys, now); err != nil {
		t.Fatal(err)
	}
	key := keys[0]
	if key.DailyUsedTokens != 0 || key.WeeklyUsedTokens != 0 || key.MonthlyUsedTokens != 0 || key.DailyUsedAmount != 0 || key.WeeklyUsedAmount != 0 || key.MonthlyUsedAmount != 0 {
		t.Fatalf("old period usage remains: %+v", key)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectPeriodicGatewayKey(mock sqlmock.Sqlmock, now time.Time) {
	hash := sha256.Sum256([]byte("sk-gw-periodic-key"))
	mock.ExpectQuery("FROM `api_keys` WHERE key_hash = \\? AND status = \\?").
		WithArgs(hash[:], "active", now, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "key_hash", "key_prefix", "key_suffix", "organization_id", "status", "daily_quota_tokens"}).
			AddRow(7, hash[:], "sk-gw-", "-key", 2, "active", 100))
	mock.ExpectQuery("SELECT status FROM `organizations` WHERE id = \\?").WithArgs(uint64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))
}

func periodicRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"api_key_id", "daily_tokens", "daily_amount", "weekly_tokens", "weekly_amount", "monthly_tokens", "monthly_amount"})
}

func periodicBoundaryArgs(id uint64, day, week, month, start time.Time) []driver.Value {
	date := func(at time.Time) string { return at.In(time.FixedZone("UTC+8", 8*60*60)).Format("2006-01-02") }
	return []driver.Value{date(day), date(day), date(week), date(week), date(month), date(month), id, date(start),
		day, day, week, week, month, month, id, start,
		day, day, week, week, month, month, id, start}
}

func organizationPeriodicRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"organization_id", "daily_tokens", "daily_amount", "weekly_tokens", "weekly_amount", "monthly_tokens", "monthly_amount"})
}
func TestOrganizationPeriodicUsageBoundariesAndEmptyPeriods(t *testing.T) {
	for _, test := range []struct {
		name, now, day, week, month, earliest string
		empty                                 bool
	}{
		{"before midnight", "2026-10-12T15:59:59Z", "2026-10-11T16:00:00Z", "2026-10-11T16:00:00Z", "2026-09-30T16:00:00Z", "2026-09-30T16:00:00Z", false},
		{"after midnight", "2026-10-12T16:00:00Z", "2026-10-12T16:00:00Z", "2026-10-11T16:00:00Z", "2026-09-30T16:00:00Z", "2026-09-30T16:00:00Z", false},
		{"Sunday", "2026-10-11T15:59:59Z", "2026-10-10T16:00:00Z", "2026-10-04T16:00:00Z", "2026-09-30T16:00:00Z", "2026-09-30T16:00:00Z", false},
		{"Monday", "2026-10-11T16:00:00Z", "2026-10-11T16:00:00Z", "2026-10-11T16:00:00Z", "2026-09-30T16:00:00Z", "2026-09-30T16:00:00Z", false},
		{"week spans previous month", "2026-09-30T16:00:00Z", "2026-09-30T16:00:00Z", "2026-09-27T16:00:00Z", "2026-09-30T16:00:00Z", "2026-09-27T16:00:00Z", false},
		{"year rollover", "2026-12-31T16:00:00Z", "2026-12-31T16:00:00Z", "2026-12-27T16:00:00Z", "2026-12-31T16:00:00Z", "2026-12-27T16:00:00Z", false},
		{"all periods reset", "2026-05-31T16:00:00Z", "2026-05-31T16:00:00Z", "2026-05-31T16:00:00Z", "2026-05-31T16:00:00Z", "2026-05-31T16:00:00Z", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, mock := quotaRepository(t)
			parse := func(value string) time.Time {
				v, err := time.Parse(time.RFC3339, value)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			rows := organizationPeriodicRows()
			want := OrganizationPeriodicUsage{OrganizationID: 2}
			if !test.empty {
				want = OrganizationPeriodicUsage{OrganizationID: 2, DailyTokens: 10, DailyAmount: 0.1, WeeklyTokens: 20, WeeklyAmount: 0.2, MonthlyTokens: 30, MonthlyAmount: 0.3}
				rows.AddRow(2, 10, 0.1, 20, 0.2, 30, 0.3)
			}
			mock.ExpectQuery(`SELECT organization_id, .*FROM \(SELECT .*usage_daily_rollups FORCE INDEX \(idx_rollup_org_date\).*UNION ALL SELECT .*usage_logs_v2 FORCE INDEX \(idx_usage_org_pending_time\).*rollup_applied = 0.*UNION ALL SELECT .*usage_logs FORCE INDEX \(idx_usage_org_pending_time\).*rollup_applied = 0`).WithArgs(periodicBoundaryArgs(2, parse(test.day), parse(test.week), parse(test.month), parse(test.earliest))...).WillReturnRows(rows)
			got, err := FindOrganizationPeriodicUsage(context.Background(), repository.database, 2, parse(test.now).In(time.FixedZone("host", -7*3600)))
			if err != nil || got != want {
				t.Fatalf("usage=%+v want=%+v err=%v", got, want, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestOrganizationPeriodicUsageErrorsAndCancellation(t *testing.T) {
	for _, phase := range []string{"database error", "canceled", "deadline"} {
		t.Run(phase, func(t *testing.T) {
			repository, mock := quotaRepository(t)
			ctx := context.Background()
			want := errors.New("ledger unavailable")
			switch phase {
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				want = context.DeadlineExceeded
			default:
				mock.ExpectQuery("SELECT organization_id, .*FROM").WillReturnError(want)
			}
			got, err := FindOrganizationPeriodicUsage(ctx, repository.database, 2, time.Now())
			if !errors.Is(err, want) || got != (OrganizationPeriodicUsage{}) {
				t.Fatalf("usage=%+v err=%v want=%v", got, err, want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestPeriodicUsageSQLKeepsSourcesIsolatedWithContext(t *testing.T) {
	repository, _ := quotaRepository(t)
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	// WithContext and a transaction both produce sessions which must not share
	// mutable clauses between the UNION branches or their outer aggregate.
	for _, org := range []bool{false, true} {
		t.Run(fmt.Sprint(org), func(t *testing.T) {
			db := repository.database.WithContext(ctx).Session(&gorm.Session{DryRun: true})
			now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			var statement *gorm.Statement
			if org {
				statement = organizationPeriodicUsageQuery(db, 2, now).Find(&[]organizationPeriodicUsageRecord{}).Statement
			} else {
				statement = periodicUsageQuery(db, []uint64{7}, now).Find(&[]periodicUsageRecord{}).Statement
			}
			sql := statement.SQL.String()
			if statement.Context.Value(contextKey{}) != "request" {
				t.Fatal("query lost request context")
			}
			if strings.Count(sql, "UNION ALL") != 2 || strings.Count(sql, "GROUP BY") != 4 || strings.Count(sql, "rollup_applied = 0") != 2 || strings.Count(sql, "created_at >= ? THEN") != 12 {
				t.Fatalf("shared query clauses: %s", sql)
			}
			dimension, index := "api_key_id", "key"
			if org {
				dimension, index = "organization_id", "org"
			}
			for _, fragment := range []string{"usage_daily_rollups FORCE INDEX (idx_rollup_" + index + "_date)", "usage_logs_v2 FORCE INDEX (idx_usage_" + index + "_pending_time)", "usage_logs FORCE INDEX (idx_usage_" + index + "_pending_time)"} {
				if strings.Count(sql, fragment) != 1 {
					t.Fatalf("missing/duplicated index %s: %s", fragment, sql)
				}
			}
			if org && (strings.Contains(sql, "api_keys") || strings.Contains(sql, "api_key_id")) {
				t.Fatalf("tenant history must include moved/deleted keys without joining key identities: %s", sql)
			}
			if strings.Count(sql, "WHERE "+dimension) != 3 || len(statement.Vars) != 24 {
				t.Fatalf("unscoped SQL or unexpected boundaries: %s vars=%v", sql, statement.Vars)
			}
			if db.Statement.SQL.Len() != 0 || db.Statement.Table != "" {
				t.Fatal("builder mutated the caller session")
			}
		})
	}
}
