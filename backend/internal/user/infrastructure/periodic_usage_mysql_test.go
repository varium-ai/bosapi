package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"bosapi/backend/internal/platform/usagerollup"
	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/internal/user/domain"
)

func TestPeriodicUsageMySQLRollupsPendingAndExpiredHistory(t *testing.T) {
	db := newKeyUsageMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Date(2026, 9, 30, 17, 0, 0, 0, time.UTC)
	day, week, _ := usagetime.PeriodStarts(now)
	// The week crosses into the previous month, and the Key has moved tenants.
	for i, at := range []time.Time{week.Add(-time.Millisecond), week, day.Add(-time.Millisecond), day, now} {
		if err := db.WithContext(ctx).Exec(`INSERT INTO usage_logs
		 (organization_id, api_key_id, model, total_tokens, cost_amount, created_at)
		 VALUES (?, 7, 'example', 3, 0.00000001, ?)`, i%2+1, at).Error; err != nil {
			t.Fatal(err)
		}
	}
	query := func() []domain.APIKey {
		t.Helper()
		keys := []domain.APIKey{{ID: 7, OrganizationID: 2}, {ID: 8}}
		if err := NewKeyRepository(db).populatePeriodicUsageAt(ctx, keys, now); err != nil {
			t.Fatal(err)
		}
		return keys
	}
	want := query()
	if want[0].DailyUsedTokens != 6 || want[0].WeeklyUsedTokens != 12 || want[0].MonthlyUsedTokens != 6 || want[0].WeeklyUsedAmount != 0.00000004 {
		t.Fatalf("calendar totals=%+v", want[0])
	}
	if count, err := usagerollup.New(db).Batch(ctx, 3); err != nil || count != 3 {
		t.Fatalf("count=%d error=%v", count, err)
	}
	if got := query(); !reflect.DeepEqual(got, want) {
		t.Fatalf("mixed counters=%+v want=%+v", got, want)
	}
	// A repeatable-read reader must keep the pre-batch view of both counters and flags.
	tx := db.WithContext(ctx).Begin(&sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer func() {
		if err := tx.Rollback().Error; err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	keys := []domain.APIKey{{ID: 7, OrganizationID: 2}, {ID: 8}}
	if err := NewKeyRepository(tx).populatePeriodicUsageAt(ctx, keys, now); err != nil {
		t.Fatal(err)
	}
	if count, err := usagerollup.New(db).Batch(ctx, 100); err != nil || count != 2 {
		t.Fatalf("count=%d error=%v", count, err)
	}
	if err := NewKeyRepository(tx).populatePeriodicUsageAt(ctx, keys, now); err != nil || !reflect.DeepEqual(keys, want) {
		t.Fatalf("snapshot=%+v error=%v", keys, err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := db.WithContext(ctx).Exec("DELETE FROM usage_logs WHERE rollup_applied = 1").Error; err != nil {
		t.Fatal(err)
	}
	if got := query(); !reflect.DeepEqual(got, want) {
		t.Fatalf("expired raw rows changed quotas: %+v", got)
	}
	if err := db.WithContext(ctx).Exec(`INSERT INTO usage_logs_v2
	 (id, request_id, usage_id, organization_id, api_key_id, model, endpoint, status_code, input_tokens, output_tokens, total_tokens, cost_amount, created_at)
	 VALUES (100, 'new', 'usage', 2, 7, 'example', 'chat/completions', 200, 1, 2, 3, 0.00000001, ?)`, now).Error; err != nil {
		t.Fatal(err)
	}
	if got := query(); got[0].WeeklyUsedTokens != 15 || got[0].WeeklyUsedAmount != 0.00000005 {
		t.Fatalf("partition pending=%+v", got)
	}
}

func TestPeriodicUsageMySQLUsesPendingCoveringIndexes(t *testing.T) {
	db := newKeyUsageMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Now().UTC()
	if err := db.WithContext(ctx).Exec(`INSERT INTO usage_logs
	 (api_key_id, organization_id, total_tokens, cost_amount, created_at, rollup_applied)
	 WITH RECURSIVE sequence AS (SELECT 1 AS n UNION ALL SELECT n+1 FROM sequence WHERE n<1000)
	 SELECT 7, 2, 3, 0.00000001, ?, 1 FROM sequence`, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.WithContext(ctx).Exec("ANALYZE TABLE usage_logs").Error; err != nil {
		t.Fatal(err)
	}
	statement := periodicUsageQuery(db.Session(&gorm.Session{DryRun: true}), []uint64{7}, now).Find(&[]periodicUsageRecord{}).Statement
	var plan []struct {
		Table, Key, Extra string
		Rows              int64
	}
	if err := db.WithContext(ctx).Raw("EXPLAIN "+statement.SQL.String(), statement.Vars...).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, step := range plan {
		if step.Table == "usage_logs" || step.Table == "usage_logs_v2" {
			matched++
			if step.Key != "idx_usage_key_pending_time" || step.Rows >= 1000 || !strings.Contains(step.Extra, "Using index") {
				t.Fatalf("unbounded pending probe=%+v", step)
			}
		}
	}
	if matched != 2 {
		t.Fatalf("physical sources missing: %+v", plan)
	}
}
