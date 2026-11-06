// Package usagerollup incrementally materializes immutable usage logs into
// UTC+8 day/organization/key/model counters without changing gateway writes.
package usagerollup

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"bosapi/backend/internal/platform/logging"
	"gorm.io/gorm"
)

const batchSize = 1000

// Processor safely shares pending work across backend replicas. Per-log flags,
// rather than an ID watermark, retain late commits and backdated requests.
type Processor struct{ database *gorm.DB }

func New(database *gorm.DB) *Processor { return &Processor{database: database} }

// Batch commits the SQL decimal sums and their source-log acknowledgements in
// one transaction. Rollback leaves every source row available for retry.
func (processor *Processor) Batch(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 5000 {
		return 0, errors.New("usage rollup batch size must be between 1 and 5000")
	}
	oldCount, oldErr := processor.batchTable(ctx, limit, "usage_logs")
	newCount, newErr := processor.batchTable(ctx, limit, "usage_logs_v2")
	return oldCount + newCount, errors.Join(oldErr, newErr)
}

func (processor *Processor) batchTable(ctx context.Context, limit int, table string) (int, error) {
	count := 0
	err := processor.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []uint64
		if err := tx.Raw(`SELECT id FROM `+table+` FORCE INDEX (idx_usage_rollup_pending)
			WHERE rollup_applied = 0 ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED`, limit).Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Exec(strings.ReplaceAll(accumulateSQL, "FROM usage_logs ", "FROM "+table+" "), ids).Error; err != nil {
			return err
		}
		ack := tx.Exec("UPDATE "+table+" SET rollup_applied = 1 WHERE rollup_applied = 0 AND id IN ?", ids)
		if ack.Error != nil {
			return ack.Error
		}
		if ack.RowsAffected != int64(len(ids)) {
			return errors.New("usage rollup acknowledgement count changed")
		}
		count = len(ids)
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// All arithmetic stays in MySQL, including the ledger's rounded DECIMAL cost.
// Deterministic bucket order reduces contention between independent workers.
const accumulateSQL = `INSERT INTO usage_daily_rollups
	(usage_date, organization_id, api_key_id, model, requests, errors,
	 input_tokens, cache_read_tokens, cache_write_tokens,
	 cache_read_cost_amount, cache_write_cost_amount,
	 output_tokens, total_tokens, cost_amount)
	SELECT usage_date, organization_id, api_key_id, model, add_requests, add_errors,
		add_input, add_cache_read, add_cache_write,
		add_cache_read_cost, add_cache_write_cost,
		add_output, add_tokens, add_cost FROM (
		SELECT DATE(CONVERT_TZ(created_at, '+00:00', '+08:00')) AS usage_date,
			organization_id, api_key_id, model, COUNT(*) AS add_requests,
			SUM(status_code >= 400) AS add_errors, SUM(input_tokens) AS add_input,
			SUM(cache_read_tokens) AS add_cache_read,
			SUM(cache_write_tokens) AS add_cache_write,
			SUM(cache_read_cost_amount) AS add_cache_read_cost,
			SUM(cache_write_cost_amount) AS add_cache_write_cost,
			SUM(output_tokens) AS add_output, SUM(total_tokens) AS add_tokens,
			SUM(cost_amount) AS add_cost
		FROM usage_logs WHERE rollup_applied = 0 AND id IN ?
		GROUP BY usage_date, organization_id, api_key_id, model
	) AS incoming ORDER BY usage_date, organization_id, api_key_id, model
	ON DUPLICATE KEY UPDATE
		requests = usage_daily_rollups.requests + incoming.add_requests,
		errors = usage_daily_rollups.errors + incoming.add_errors,
		input_tokens = usage_daily_rollups.input_tokens + incoming.add_input,
		cache_read_tokens = usage_daily_rollups.cache_read_tokens + incoming.add_cache_read,
		cache_write_tokens = usage_daily_rollups.cache_write_tokens + incoming.add_cache_write,
		cache_read_cost_amount = usage_daily_rollups.cache_read_cost_amount + incoming.add_cache_read_cost,
		cache_write_cost_amount = usage_daily_rollups.cache_write_cost_amount + incoming.add_cache_write_cost,
		output_tokens = usage_daily_rollups.output_tokens + incoming.add_output,
		total_tokens = usage_daily_rollups.total_tokens + incoming.add_tokens,
		cost_amount = usage_daily_rollups.cost_amount + incoming.add_cost`

// Start backfills history and then processes new requests. The returned stop
// function is idempotent and waits for the current transaction to roll back.
func Start(database *gorm.DB) func() { return start(New(database).Batch) }

func start(batch func(context.Context, int) (int, error)) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			batchCtx, cancelBatch := context.WithTimeout(ctx, 10*time.Second)
			count, err := batch(batchCtx, batchSize)
			cancelBatch()
			delay := time.Second
			if err != nil && ctx.Err() == nil {
				logging.Printf(ctx, "ERROR", "usage.rollup.batch_failed", "err=%v", err)
			} else if count >= batchSize {
				delay = 20 * time.Millisecond
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); <-done }) }
}
