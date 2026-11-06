package infrastructure

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/internal/user/domain"
	"github.com/DATA-DOG/go-sqlmock"
)

func adminKeyUsageRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"api_key_id", "daily_tokens", "daily_amount", "weekly_tokens", "weekly_amount", "monthly_tokens", "monthly_amount", "today_amount", "recent_amount"})
}

func TestAdminKeyUsageCombinesPeriodsAndDisplayInOneQuery(t *testing.T) {
	for _, now := range []time.Time{
		time.Date(2026, 10, 1, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*3600)),
		time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC),
	} {
		repository, mock := quotaRepository(t)
		keys := []domain.APIKey{{ID: 7}, {ID: 8}}
		query, args := adminKeyUsageQuery(keys, now)
		day, week, month := usagetime.PeriodStarts(now)
		recent := now.Add(-720 * time.Hour)
		start := recent
		for _, boundary := range []time.Time{week, month} {
			if boundary.Before(start) {
				start = boundary
			}
		}
		if strings.Count(query, "UNION ALL") != 3 {
			t.Fatal("each Key must use its own bounded index range in one statement")
		}
		var want []driver.Value
		for _, id := range []uint64{7, 8} {
			want = append(want, id, day, day, week, week, month, month, day, now.UTC(), recent.UTC(), now.UTC(), id, start.UTC(), id, start.UTC())
		}
		if len(args) != len(want) {
			t.Fatalf("query arguments=%d want %d", len(args), len(want))
		}
		mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(want...).
			WillReturnRows(adminKeyUsageRows().AddRow(7, 10, 0.125, 20, 1.5, 30, 2.5, 0.1, 12.3456).AddRow(8, 0, 0, 0, 0, 0, 0, 0, 0))
		if err := repository.populateAdminKeyUsageAt(context.Background(), keys, now); err != nil {
			t.Fatal(err)
		}
		key := keys[0]
		if key.DailyUsedTokens != 10 || key.WeeklyUsedTokens != 20 || key.MonthlyUsedTokens != 30 ||
			key.DailyUsedAmount != 0.125 || key.WeeklyUsedAmount != 1.5 || key.MonthlyUsedAmount != 2.5 ||
			key.UsageTodayAmount == nil || *key.UsageTodayAmount != 0.1 || key.Usage30dAmount == nil || *key.Usage30dAmount != 12.3456 {
			t.Fatalf("unexpected usage: %+v", key)
		}
		if keys[1].UsageTodayAmount == nil || *keys[1].UsageTodayAmount != 0 || keys[1].Usage30dAmount == nil || *keys[1].Usage30dAmount != 0 {
			t.Fatal("empty Key must have measured zero usage")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdminKeyUsageFailureKeepsDisplayUnknown(t *testing.T) {
	repository, mock := quotaRepository(t)
	stale := 42.0
	keys := []domain.APIKey{{ID: 7, UsageTodayAmount: &stale, Usage30dAmount: &stale}}
	failure := errors.New("usage unavailable")
	mock.ExpectQuery("SELECT \\? AS api_key_id").WillReturnError(failure)
	if err := repository.populateAdminKeyUsageAt(context.Background(), keys, time.Now()); !errors.Is(err, failure) {
		t.Fatalf("unexpected error: %v", err)
	}
	if keys[0].UsageTodayAmount != nil || keys[0].Usage30dAmount != nil {
		t.Fatal("unavailable display usage must remain unknown")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAdminKeyUsageEmptyPageDoesNotQuery(t *testing.T) {
	repository, mock := quotaRepository(t)
	for _, keys := range [][]domain.APIKey{nil, {{ID: 0}}} {
		if err := repository.populateAdminKeyUsageAt(context.Background(), keys, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
