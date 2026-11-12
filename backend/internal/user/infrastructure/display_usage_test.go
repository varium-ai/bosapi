package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	"bosapi/backend/internal/user/domain"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestDisplayUsageBatchesKeysWithRollingWindow(t *testing.T) {
	repository, mock := quotaRepository(t)
	now := time.Date(2026, 10, 13, 7, 0, 0, 0, time.UTC)
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT api_key_id, .*FROM \(SELECT .*FROM usage_logs_v2 FORCE INDEX .*UNION ALL .*FROM usage_logs FORCE INDEX .*AS usage_source WHERE created_at <= \? GROUP BY api_key_id`).
		WithArgs(day, uint64(7), uint64(8), now.Add(-720*time.Hour), uint64(7), uint64(8), now.Add(-720*time.Hour), now).
		WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "today_amount", "recent_amount"}).AddRow(7, 0.125, 12.3456))
	keys := []domain.APIKey{{ID: 7}, {ID: 8}}
	if err := repository.populateDisplayUsageAt(context.Background(), keys, now); err != nil {
		t.Fatal(err)
	}
	if keys[0].UsageTodayAmount == nil || *keys[0].UsageTodayAmount != 0.125 || keys[0].Usage30dAmount == nil || *keys[0].Usage30dAmount != 12.3456 {
		t.Fatalf("unexpected usage: %+v", keys[0])
	}
	if keys[1].UsageTodayAmount == nil || *keys[1].UsageTodayAmount != 0 || keys[1].Usage30dAmount == nil || *keys[1].Usage30dAmount != 0 {
		t.Fatalf("no rows must be a measured zero: %+v", keys[1])
	}
	safe := keys[0].Redacted()
	if safe.UsageTodayAmount != keys[0].UsageTodayAmount || safe.Usage30dAmount != keys[0].Usage30dAmount {
		t.Fatal("display usage missing from safe response")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayUsageFailureIsNotReportedAsZero(t *testing.T) {
	repository, mock := quotaRepository(t)
	stale := 12.0
	keys := []domain.APIKey{{ID: 7, UsageTodayAmount: &stale, Usage30dAmount: &stale}}
	failure := errors.New("usage unavailable")
	mock.ExpectQuery("SELECT api_key_id, .* AS today_amount").WillReturnError(failure)
	if err := repository.populateDisplayUsageAt(context.Background(), keys, time.Now()); !errors.Is(err, failure) {
		t.Fatalf("unexpected error: %v", err)
	}
	if keys[0].UsageTodayAmount != nil || keys[0].Usage30dAmount != nil {
		t.Fatal("unavailable usage must remain unknown")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayUsageEmptyPageDoesNotQuery(t *testing.T) {
	repository, mock := quotaRepository(t)
	if err := repository.populateDisplayUsageAt(context.Background(), nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
