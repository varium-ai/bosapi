package infrastructure

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bosapi/backend/internal/platform/testusage"
	"bosapi/backend/internal/platform/usagerollup"
	"bosapi/backend/internal/platform/usagestore"
	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/internal/user/domain"
	"gorm.io/gorm"
)

func partitionQuotaDatabase(t *testing.T) (*gorm.DB, *KeyRepository, time.Time) {
	t.Helper()
	db := newKeyUsageMySQL(t)
	if err := db.Exec(`CREATE TABLE api_keys (
		id BIGINT UNSIGNED PRIMARY KEY, user_id BIGINT UNSIGNED NULL, organization_id BIGINT UNSIGNED NOT NULL,
		quota_used_tokens BIGINT UNSIGNED NULL, quota_used_amount DECIMAL(20,8) NULL, last_used_at DATETIME(3) NULL);
		INSERT INTO api_keys VALUES (7, NULL, 2, NULL, NULL, NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	testusage.Activate(t, db, false)
	var now time.Time
	if err := db.Raw("SELECT UTC_TIMESTAMP(3)").Scan(&now).Error; err != nil {
		t.Fatal(err)
	}
	return db, NewKeyRepository(db), now
}

func partitionUsage(now time.Time, request string) GatewayUsageRecord {
	return GatewayUsageRecord{RequestID: request, UsageID: "usage", OrganizationID: 2, APIKeyID: 7,
		Model: "model", Endpoint: "chat/completions", StatusCode: 200, InputTokens: 1, OutputTokens: 2,
		TotalTokens: 3, CostAmount: 0.123456789, CreatedAt: now}
}

func assertUsageCounts(t *testing.T, db *gorm.DB, raw, receipts, requests, tokens uint64, amount string) {
	t.Helper()
	for table, want := range map[string]uint64{"usage_logs_read": raw, "usage_log_receipts": receipts} {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil || uint64(count) != want {
			t.Fatalf("%s rows=%d want=%d err=%v", table, count, want, err)
		}
	}
	var totals struct {
		Requests uint64
		Tokens   uint64
		Amount   string
	}
	if err := db.Raw(`SELECT COALESCE(SUM(requests),0) AS requests, COALESCE(SUM(total_tokens),0) AS tokens,
		CAST(COALESCE(SUM(cost_amount),0) AS CHAR) AS amount FROM usage_daily_rollups`).Scan(&totals).Error; err != nil {
		t.Fatal(err)
	}
	if totals.Requests != requests || totals.Tokens != tokens || totals.Amount != amount {
		t.Fatalf("rollups=%+v want requests=%d tokens=%d amount=%s", totals, requests, tokens, amount)
	}
}

func TestPartitionedUsageMySQLRetryExpiryAndLateReports(t *testing.T) {
	db, repository, now := partitionQuotaDatabase(t)
	ctx := context.Background()
	record := partitionUsage(now, "retry")
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, record); err != nil || !ok {
		t.Fatalf("write=%v err=%v", ok, err)
	}
	record.CreatedAt = now.Add(24 * time.Hour)
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, record); err != nil || ok {
		t.Fatalf("date-crossing retry charged: %v %v", ok, err)
	}
	if count, err := usagerollup.New(db).Batch(ctx, 100); err != nil || count != 1 {
		t.Fatalf("rollup=%d err=%v", count, err)
	}
	assertUsageCounts(t, db, 1, 1, 1, 3, "0.12345679")
	if _, err := usagestore.Maintain(ctx, db, now.AddDate(0, 0, 32)); err != nil {
		t.Fatal(err)
	}
	assertUsageCounts(t, db, 0, 1, 1, 3, "0.12345679")
	record.CreatedAt = now.AddDate(0, 0, 32)
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, record); err != nil || ok {
		t.Fatalf("retry after partition expiry charged: %v %v", ok, err)
	}
	late := partitionUsage(now, "late")
	late.CostAmount = 0.000000014
	late.StatusCode = 500
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, late); err != nil || !ok {
		t.Fatalf("late report=%v err=%v", ok, err)
	}
	assertUsageCounts(t, db, 0, 2, 2, 6, "0.12345680")
	var quota struct {
		Tokens uint64
		Amount string
	}
	if err := db.Raw("SELECT quota_used_tokens AS tokens, CAST(quota_used_amount AS CHAR) AS amount FROM api_keys WHERE id = 7").Scan(&quota).Error; err != nil || quota.Tokens != 6 || quota.Amount != "0.12345680" {
		t.Fatalf("quota=%+v err=%v", quota, err)
	}
}

func TestPartitionedUsageMySQLConcurrentRetriesAndRollback(t *testing.T) {
	db, repository, now := partitionQuotaDatabase(t)
	ctx := context.Background()
	record := partitionUsage(now, "concurrent")
	var inserted atomic.Int64
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ok, err := repository.RecordGatewayUsageAndQuota(ctx, record)
			if err != nil {
				t.Errorf("concurrent write: %v", err)
			}
			if ok {
				inserted.Add(1)
			}
		}()
	}
	workers.Wait()
	if inserted.Load() != 1 {
		t.Fatalf("charged %d retries", inserted.Load())
	}
	if err := db.Exec(`CREATE TRIGGER reject_quota BEFORE UPDATE ON api_keys FOR EACH ROW
		SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'quota failure'`).Error; err != nil {
		t.Fatal(err)
	}
	record.RequestID = "rollback"
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, record); err == nil || ok {
		t.Fatalf("rollback=%v err=%v", ok, err)
	}
	if err := db.Exec("DROP TRIGGER reject_quota").Error; err != nil {
		t.Fatal(err)
	}
	var receipts int64
	if err := db.Table("usage_log_receipts").Count(&receipts).Error; err != nil || receipts != 1 {
		t.Fatalf("rolled-back receipt remains: %d %v", receipts, err)
	}
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, record); err != nil || !ok {
		t.Fatalf("retry rollback=%v err=%v", ok, err)
	}
	// INSERT IGNORE must not silently accept an invalid decimal and then charge it.
	record.RequestID, record.CostAmount = "overflow", 1e30
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, record); err == nil || ok {
		t.Fatalf("invalid amount accepted: %v %v", ok, err)
	}
	if _, err := usagerollup.New(db).Batch(ctx, 100); err != nil {
		t.Fatal(err)
	}
	assertUsageCounts(t, db, 2, 2, 2, 6, "0.24691358")
}

func TestPartitionedUsageMySQLRollingUpgradeCompatibility(t *testing.T) {
	db, repository, now := partitionQuotaDatabase(t)
	ctx := context.Background()
	if err := db.Exec("UPDATE usage_log_retention SET partition_writes = 0").Error; err != nil {
		t.Fatal(err)
	}
	record := partitionUsage(now, "compat-new-first")
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, record); err != nil || !ok {
		t.Fatalf("compat write=%v %v", ok, err)
	}
	// An old binary's retry still hits the original global unique constraint.
	oldInsert := `INSERT INTO usage_logs (request_id, usage_id, organization_id, api_key_id, total_tokens, cost_amount, created_at)
		VALUES (?, 'usage', 2, 7, 3, 0.12345679, ?) ON DUPLICATE KEY UPDATE id = id`
	if result := db.Exec(oldInsert, record.RequestID, now); result.Error != nil || result.RowsAffected != 0 {
		t.Fatalf("old binary could double charge: rows=%d err=%v", result.RowsAffected, result.Error)
	}
	if err := db.Exec(oldInsert, "compat-old-first", now).Error; err != nil {
		t.Fatal(err)
	}
	record.RequestID = "compat-old-first"
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, record); err != nil || ok {
		t.Fatalf("old-first retry charged: %v %v", ok, err)
	}
	var count int64
	if err := db.Table("usage_logs_v2").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("partition writes activated during rollout: %d %v", count, err)
	}
	testusage.Activate(t, db, true)
	record.RequestID = "partition-new"
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, record); err != nil || !ok {
		t.Fatalf("activated write=%v %v", ok, err)
	}
	if count, err := usagerollup.New(db).Batch(ctx, 100); err != nil || count != 3 {
		t.Fatalf("both sources not processed: %d %v", count, err)
	}
	if count, err := usagestore.MoveBatch(ctx, db); err != nil || count != 2 {
		t.Fatalf("compat rows not moved: %d %v", count, err)
	}
	assertUsageCounts(t, db, 3, 3, 3, 9, "0.37037037")
}

func TestPartitionedUsageMySQLLegacyMoveAndMonthlyQuota(t *testing.T) {
	db, repository, _ := partitionQuotaDatabase(t)
	ctx := context.Background()
	local := time.Now().In(usagetime.Location())
	monthStart := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, usagetime.Location())
	for monthStart.AddDate(0, 1, -1).Day() != 31 {
		monthStart = monthStart.AddDate(0, 1, 0)
	}
	now := monthStart.AddDate(0, 1, 0).Add(-time.Second).UTC()
	month := monthStart.UTC()
	if err := db.Exec("UPDATE usage_log_retention SET partition_writes = 0").Error; err != nil {
		t.Fatal(err)
	}
	for i, created := range []time.Time{month.Add(-time.Millisecond), month, now} {
		r := partitionUsage(created, fmt.Sprintf("legacy-%d", i))
		if err := db.Exec(`INSERT INTO usage_logs
			(request_id, usage_id, organization_id, api_key_id, model, endpoint, status_code, input_tokens, output_tokens, total_tokens, cost_amount, created_at)
			VALUES (?, ?, 2, 7, 'model', 'chat/completions', 200, 1, 2, 3, 0.00000001, ?)`, r.RequestID, r.UsageID, created).Error; err != nil {
			t.Fatal(err)
		}
	}
	testusage.Activate(t, db, false)
	if count, err := usagestore.MoveBatch(ctx, db); err != nil || count != 0 {
		t.Fatalf("drain must wait for rollout opt-in: %d %v", count, err)
	}
	if err := db.Exec("UPDATE usage_log_backfill SET enabled = 1").Error; err != nil {
		t.Fatal(err)
	}
	if count, err := usagestore.MoveBatch(ctx, db); err != nil || count != 0 {
		t.Fatalf("pending rows moved: %d %v", count, err)
	}
	duplicate := partitionUsage(month.Add(48*time.Hour), "LEGACY-1") // same original case-insensitive identity
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, duplicate); err != nil || ok {
		t.Fatalf("legacy retry charged: %v %v", ok, err)
	}
	if count, err := usagerollup.New(db).Batch(ctx, 100); err != nil || count != 3 {
		t.Fatalf("rollup=%d %v", count, err)
	}
	// Simulating month end can advance the clock by almost two months. Each
	// maintenance tick intentionally adds no more than 32 missing boundaries.
	for range 3 {
		if _, err := usagestore.Maintain(ctx, db, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("UPDATE usage_log_backfill SET next_batch_at = NULL").Error; err != nil {
		t.Fatal(err)
	}
	// Public detail ID is identical before and after the physical move.
	before, err := repository.FindUsage(ctx, domain.UsageFilter{OrganizationID: 2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := usagestore.MoveBatch(ctx, db); err != nil || count != 3 {
		t.Fatalf("move=%d %v", count, err)
	}
	after, err := repository.FindUsage(ctx, domain.UsageFilter{OrganizationID: 2}, 2)
	if err != nil || after.ID != before.ID || after.RequestID != before.RequestID {
		t.Fatalf("detail moved: before=%+v after=%+v err=%v", before, after, err)
	}
	if count, err := usagestore.MoveBatch(ctx, db); err != nil || count != 0 {
		t.Fatalf("global throttle: %d %v", count, err)
	}
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, duplicate); err != nil || ok {
		t.Fatalf("moved retry charged: %v %v", ok, err)
	}
	assertUsageCounts(t, db, 2, 3, 3, 9, "0.00000003")
	keys := []domain.APIKey{{ID: 7}}
	if err := repository.populatePeriodicUsageAt(ctx, keys, now); err != nil || keys[0].MonthlyUsedTokens != 6 || keys[0].MonthlyUsedAmount != 0.00000002 {
		t.Fatalf("31-day month lost its first day: %+v %v", keys, err)
	}
	if err := repository.populateAdminKeyUsageAt(ctx, keys, now); err != nil || keys[0].MonthlyUsedTokens != 6 {
		t.Fatalf("admin monthly quota=%+v %v", keys, err)
	}
	var plan []struct{ Table, Partitions string }
	from, to := usagetime.DayStart(now), usagetime.DayStart(now).AddDate(0, 0, 1)
	if err := db.Raw("EXPLAIN SELECT id FROM usage_logs_read WHERE organization_id = ? AND api_key_id = ? AND created_at >= ? AND created_at < ?", 2, 7, from, to).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	found := false
	for _, step := range plan {
		if step.Table == "usage_logs_v2" {
			found = true
			if step.Partitions != "p"+now.In(usagetime.Location()).Format("20060102") {
				t.Fatalf("view failed to prune daily partitions: %+v", plan)
			}
		}
	}
	if !found {
		t.Fatalf("partitioned source missing from plan: %+v", plan)
	}
}

func TestPartitionedUsageMySQLPendingExpiryFence(t *testing.T) {
	db, repository, now := partitionQuotaDatabase(t)
	ctx := context.Background()
	record := partitionUsage(now, "pending")
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, record); err != nil || !ok {
		t.Fatalf("write=%v %v", ok, err)
	}
	future := now.AddDate(0, 0, 32)
	if _, err := usagestore.Maintain(ctx, db, future); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("usage_logs_v2").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("pending partition dropped: %d %v", count, err)
	}
	// The sealed cutoff also rejects raw inserts from a late source move/report.
	late := partitionUsage(now, "late-pending")
	if ok, err := repository.RecordGatewayUsageAndQuota(ctx, late); err != nil || !ok {
		t.Fatalf("late report=%v %v", ok, err)
	}
	if err := db.Table("usage_logs_v2").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("sealed raw partition revived: %d %v", count, err)
	}
	if n, err := usagerollup.New(db).Batch(ctx, 100); err != nil || n != 1 {
		t.Fatalf("rollup=%d %v", n, err)
	}
	if _, err := usagestore.Maintain(ctx, db, future); err != nil {
		t.Fatal(err)
	}
	assertUsageCounts(t, db, 0, 2, 2, 6, "0.24691358")
}

func TestPartitionedUsageMySQLFenceWaitsForInFlightWriter(t *testing.T) {
	db, _, now := partitionQuotaDatabase(t)
	ctx := context.Background()
	tx := db.Begin()
	defer tx.Rollback()
	record := UsageRecord{RequestID: "in-flight", UsageID: "usage", OrganizationID: 2, APIKeyID: 7, Model: "model", Endpoint: "chat/completions", StatusCode: 200, TotalTokens: 3, CostAmount: 0.00000001, CreatedAt: now}
	if ok, err := insertUsageRecord(tx, &record); err != nil || !ok {
		t.Fatalf("insert=%v %v", ok, err)
	}
	var before, after time.Time
	if err := db.Raw("SELECT retained_from FROM usage_log_retention WHERE id = 1").Scan(&before).Error; err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err := usagestore.Maintain(wait, db, now.AddDate(0, 0, 32)); err == nil {
		t.Fatal("retention advanced past an uncommitted raw write")
	}
	if err := db.Raw("SELECT retained_from FROM usage_log_retention WHERE id = 1").Scan(&after).Error; err != nil || !after.Equal(before) {
		t.Fatalf("fence changed before writer commit: %s %s %v", before, after, err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if _, err := usagerollup.New(db).Batch(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := usagestore.Maintain(ctx, db, now.AddDate(0, 0, 32)); err != nil {
		t.Fatalf("maintenance lock/settings not recovered: %v", err)
	}
	assertUsageCounts(t, db, 0, 1, 1, 3, "0.00000001")
}

func TestPartitionedUsageMySQLLegacyMoveRollsBack(t *testing.T) {
	db, _, now := partitionQuotaDatabase(t)
	ctx := context.Background()
	if err := db.Exec("UPDATE usage_log_retention SET partition_writes = 0").Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE usage_log_backfill SET enabled = 1`,
		`INSERT INTO usage_logs (request_id, usage_id, organization_id, api_key_id, total_tokens, cost_amount, created_at) VALUES ('move', 'usage', 2, 7, 3, 0.00000001, UTC_TIMESTAMP(3))`,
		`CREATE TRIGGER reject_move BEFORE DELETE ON usage_logs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'move failure'`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	testusage.Activate(t, db, true)
	if _, err := usagerollup.New(db).Batch(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if count, err := usagestore.MoveBatch(ctx, db); err == nil || count != 0 {
		t.Fatalf("failed move committed: %d %v", count, err)
	}
	assertUsageCounts(t, db, 1, 0, 1, 3, "0.00000001")
	if err := db.Exec("DROP TRIGGER reject_move").Error; err != nil {
		t.Fatal(err)
	}
	if count, err := usagestore.MoveBatch(ctx, db); err != nil || count != 1 {
		t.Fatalf("move retry=%d %v", count, err)
	}
	if count, err := usagerollup.New(db).Batch(ctx, 100); err != nil || count != 0 {
		t.Fatalf("copied acknowledgement double counted: %d %v", count, err)
	}
	if _, err := usagestore.Maintain(ctx, db, now); err != nil {
		t.Fatal(err)
	}
	assertUsageCounts(t, db, 1, 1, 1, 3, "0.00000001")
}

func TestPartitionedUsageMySQLSharedBatchLimitAndSnapshot(t *testing.T) {
	db, _, now := partitionQuotaDatabase(t)
	ctx := context.Background()
	if err := db.Exec("UPDATE usage_log_retention SET partition_writes = 0").Error; err != nil {
		t.Fatal(err)
	}
	rows := make([]UsageRecord, 501)
	for i := range rows {
		rows[i] = UsageRecord{RequestID: fmt.Sprintf("batch-%d", i), UsageID: "usage", OrganizationID: 2, APIKeyID: 7, Model: "model", Endpoint: "chat/completions", StatusCode: 200, TotalTokens: 3, CostAmount: 0.00000001, CreatedAt: now}
	}
	if err := db.CreateInBatches(rows, 200).Error; err != nil {
		t.Fatal(err)
	}
	testusage.Activate(t, db, true)
	if _, err := usagerollup.New(db).Batch(ctx, 1000); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE usage_log_backfill SET enabled = 1").Error; err != nil {
		t.Fatal(err)
	}
	snapshot := db.Begin(&sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	defer snapshot.Rollback()
	var before, after int64
	if err := snapshot.Table("usage_logs_read").Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	var total atomic.Int64
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			count, err := usagestore.MoveBatch(ctx, db)
			if err != nil {
				t.Errorf("move=%v", err)
			}
			total.Add(int64(count))
		}()
	}
	workers.Wait()
	if total.Load() != usagestore.BatchSize {
		t.Fatalf("replicas multiplied the batch allowance: %d", total.Load())
	}
	if err := snapshot.Table("usage_logs_read").Count(&after).Error; err != nil || after != before || after != 501 {
		t.Fatalf("snapshot duplicated/lost rows: before=%d after=%d err=%v", before, after, err)
	}
	assertUsageCounts(t, db, 501, 200, 501, 1503, "0.00000501")
}
