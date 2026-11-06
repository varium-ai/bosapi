package infrastructure

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"errors"
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
