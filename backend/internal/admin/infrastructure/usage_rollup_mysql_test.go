package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/usagerollup"
	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

const insertRollupTestLog = `INSERT INTO usage_logs
	(id, organization_id, api_key_id, model, status_code, input_tokens, cache_read_tokens, cache_write_tokens,
	 cache_read_cost_amount, cache_write_cost_amount, output_tokens, total_tokens, cost_amount, created_at)
	VALUES (?, 9, 7, 'example', 200, 1, 1, 0, 0.00000001, 0, 2, 3, 0.00000001, ?)`

func TestUsageRollupMySQLLateCommitAndExactTail(t *testing.T) {
	database := newAdminDashboardMySQL(t)
	installUsageRollups(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	processor := usagerollup.New(database)
	// Reserve a lower auto-increment ID in an uncommitted transaction. A later
	// row is visible and must be processed without waiting for the earlier one.
	pending := database.WithContext(ctx).Begin(&sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if pending.Error != nil {
		t.Fatal(pending.Error)
	}
	defer pending.Rollback()
	if err := pending.Exec(insertRollupTestLog, 1, "2026-10-01 03:00:00").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.WithContext(ctx).Exec(insertRollupTestLog, 2, "2026-10-01 04:00:00").Error; err != nil {
		t.Fatal(err)
	}
	filter := domain.AdminUsageFilter{OrganizationID: 9}
	repository := NewPreaggregatedAdminUsageRepository(database)
	assertRequests := func(want uint64) {
		t.Helper()
		items, err := repository.AggregateAdminUsage(ctx, filter, "total")
		if err != nil || len(items) != 1 || items[0].Requests != want || items[0].TotalTokens != want*3 ||
			items[0].CacheReadTokens != want || items[0].CacheWriteTokens != 0 ||
			items[0].CacheReadCostAmount != float64(want)*0.00000001 || items[0].CacheWriteCostAmount != 0 ||
			items[0].CacheHitRate != 1 {
			t.Fatalf("result=%+v want=%d error=%v", items, want, err)
		}
	}
	assertRequests(1)
	if count, err := processor.Batch(ctx, 100); err != nil || count != 1 {
		t.Fatalf("locked earlier log must be skipped: count=%d error=%v", count, err)
	}
	if err := pending.Commit().Error; err != nil {
		t.Fatal(err)
	}
	// The exact repository includes committed pending rows; display snapshots
	// deliberately wait for materialization (tested separately).
	assertRequests(2)
	if count, err := processor.Batch(ctx, 100); err != nil || count != 1 {
		t.Fatalf("late commit was lost: count=%d error=%v", count, err)
	}
	assertRequests(2)
	if count, err := processor.Batch(ctx, 100); err != nil || count != 0 {
		t.Fatalf("retry counted logs twice: count=%d error=%v", count, err)
	}
	if err := database.WithContext(ctx).Exec(insertRollupTestLog+" ON DUPLICATE KEY UPDATE id = id", 1, "2026-10-01 03:00:00").Error; err != nil {
		t.Fatal(err)
	}
	if count, err := processor.Batch(ctx, 100); err != nil || count != 0 {
		t.Fatalf("duplicate ledger insert became pending again: count=%d error=%v", count, err)
	}
	// A newly committed event for an older business date also remains eligible.
	if err := database.WithContext(ctx).Exec(insertRollupTestLog, 3, "2026-09-01 03:00:00").Error; err != nil {
		t.Fatal(err)
	}
	assertRequests(3)
	if count, err := processor.Batch(ctx, 100); err != nil || count != 1 {
		t.Fatalf("backdated event lost: count=%d error=%v", count, err)
	}
	var amount string
	if err := database.WithContext(ctx).Raw("SELECT CAST(SUM(cost_amount) AS CHAR) FROM usage_daily_rollups").Scan(&amount).Error; err != nil || amount != "0.00000003" {
		t.Fatalf("decimal cost=%s error=%v", amount, err)
	}
}

func TestUsageRollupMySQLAcknowledgementFailureRollsBackSums(t *testing.T) {
	database := newAdminDashboardMySQL(t)
	installUsageRollups(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, statement := range []string{
		`INSERT INTO usage_logs (organization_id, api_key_id, model, status_code, input_tokens, cache_read_tokens, cache_write_tokens,
		 cache_read_cost_amount, cache_write_cost_amount, output_tokens, total_tokens, cost_amount, created_at)
		 VALUES (9, 7, 'example', 500, 1, 1, 0, 0.123456789, 0, 2, 3, 0.123456789, '2026-10-01 03:00:00')`,
		`CREATE TRIGGER reject_rollup BEFORE UPDATE ON usage_logs FOR EACH ROW
		 SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'test acknowledgement failure'`,
	} {
		if err := database.WithContext(ctx).Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	processor := usagerollup.New(database)
	if count, err := processor.Batch(ctx, 100); err == nil || count != 0 {
		t.Fatalf("failed acknowledgement must roll back: count=%d error=%v", count, err)
	}
	var count int64
	if err := database.WithContext(ctx).Table("usage_daily_rollups").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rolled back sums remain: count=%d error=%v", count, err)
	}
	if err := database.WithContext(ctx).Table("usage_logs").Where("rollup_applied = 0").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("source acknowledgement committed: count=%d error=%v", count, err)
	}
	before, err := NewPreaggregatedAdminUsageRepository(database).AggregateAdminUsage(ctx, domain.AdminUsageFilter{}, "total")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].CacheReadTokens != 1 || before[0].CacheWriteTokens != 0 ||
		before[0].CacheReadCostAmount != 0.123456789 || before[0].CacheWriteCostAmount != 0 {
		t.Fatalf("pending cache breakdown before retry: %+v", before)
	}
	if err := database.WithContext(ctx).Exec("DROP TRIGGER reject_rollup").Error; err != nil {
		t.Fatal(err)
	}
	if count, err := processor.Batch(ctx, 100); err != nil || count != 1 {
		t.Fatalf("retry failed: count=%d error=%v", count, err)
	}
	after, err := NewPreaggregatedAdminUsageRepository(database).AggregateAdminUsage(ctx, domain.AdminUsageFilter{}, "total")
	if err != nil || !reflect.DeepEqual(before, after) || after[0].CostAmount != 0.12345679 || after[0].Errors != 1 {
		t.Fatalf("retry changed counters: before=%+v after=%+v error=%v", before, after, err)
	}
}

func TestUsageRollupMySQLConcurrentWorkersAndReaders(t *testing.T) {
	database := newAdminDashboardMySQL(t)
	installUsageRollups(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := database.WithContext(ctx).Exec(`INSERT INTO usage_logs
		(organization_id, api_key_id, model, status_code, input_tokens, cache_read_tokens, cache_write_tokens,
		 cache_read_cost_amount, cache_write_cost_amount, output_tokens, total_tokens, cost_amount, created_at)
		WITH RECURSIVE sequence AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM sequence WHERE n < 200)
		SELECT 9, 7, CONCAT('model-', MOD(n, 3)), IF(MOD(n, 7) = 0, 500, 200), 1, 1, 0, 0.00000001, 0, 2, 3, 0.00000001,
		TIMESTAMPADD(SECOND, n, '2026-10-01 03:00:00') FROM sequence`).Error; err != nil {
		t.Fatal(err)
	}
	raw := NewAdminUsageRepository(database)
	rollups := NewPreaggregatedAdminUsageRepository(database)
	want, err := raw.AggregateAdminUsage(ctx, domain.AdminUsageFilter{}, "total")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			processor := usagerollup.New(database)
			for ctx.Err() == nil {
				count, err := processor.Batch(ctx, 5)
				var mysqlError *drivermysql.MySQLError
				if errors.As(err, &mysqlError) && mysqlError.Number == 1213 {
					continue // Deadlock rollback leaves the batch pending for retry.
				}
				if err != nil || count == 0 {
					results <- err
					return
				}
			}
			results <- ctx.Err()
		}()
	}
	for range 30 {
		got, err := rollups.AggregateAdminUsage(ctx, domain.AdminUsageFilter{}, "total")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("concurrent materialization changed the snapshot: got=%+v want=%+v error=%v", got, want, err)
			break
		}
	}
	workers.Wait()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if count, err := usagerollup.New(database).Batch(ctx, 1000); err != nil || count != 0 {
		t.Fatalf("workers left pending rows: count=%d error=%v", count, err)
	}
	got, err := rollups.AggregateAdminUsage(ctx, domain.AdminUsageFilter{}, "total")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("concurrent workers changed counters: got=%+v want=%+v error=%v", got, want, err)
	}
	var buckets int64
	if err := database.WithContext(ctx).Table("usage_daily_rollups").Count(&buckets).Error; err != nil || buckets != 3 {
		t.Fatalf("bucket count=%d error=%v", buckets, err)
	}
}

// Confirm that a query's read snapshot survives a concurrent completed batch.
func TestUsageRollupMySQLRepeatableReadSnapshot(t *testing.T) {
	database := newAdminDashboardMySQL(t)
	installUsageRollups(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := database.WithContext(ctx).Exec(insertRollupTestLog, 1, "2026-10-01 03:00:00").Error; err != nil {
		t.Fatal(err)
	}
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		repository := NewPreaggregatedAdminUsageRepository(tx)
		before, err := repository.AggregateAdminUsage(ctx, domain.AdminUsageFilter{}, "total")
		if err != nil {
			return err
		}
		if count, err := usagerollup.New(database).Batch(ctx, 100); err != nil || count != 1 {
			t.Fatalf("batch count=%d error=%v", count, err)
		}
		after, err := repository.AggregateAdminUsage(ctx, domain.AdminUsageFilter{}, "total")
		if err != nil || !reflect.DeepEqual(before, after) || after[0].Requests != 1 {
			t.Fatalf("snapshot changed: before=%+v after=%+v error=%v", before, after, err)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
}
