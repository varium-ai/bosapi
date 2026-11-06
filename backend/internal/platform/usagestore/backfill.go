// Package usagestore maintains partitioned raw usage logs while preserving
// lifetime accounting identities and acknowledged daily summaries.
package usagestore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"gorm.io/gorm"
)

const BatchSize = 200

// MoveBatch drains the legacy table, including expired history. Only acknowledged
// rows can move: copying a pending flag while a rollup commits would double count.
// The old delete, receipt and retained copy share one READ COMMITTED transaction.
func MoveBatch(ctx context.Context, database *gorm.DB) (int, error) {
	count := 0
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var states []struct{ Waiting bool }
		if err := tx.Raw(`SELECT COALESCE(next_batch_at > UTC_TIMESTAMP(6), 0) AS waiting
			FROM usage_log_backfill WHERE id = 1 AND enabled = 1 FOR UPDATE SKIP LOCKED`).Scan(&states).Error; err != nil {
			return err
		}
		if len(states) == 0 || states[0].Waiting {
			return nil
		}
		var state struct {
			RetainedFrom        time.Time
			PartitionWrites     bool
			LegacyHighWatermark uint64
		}
		if err := tx.Raw("SELECT retained_from, partition_writes, legacy_high_watermark FROM usage_log_retention WHERE id = 1 FOR SHARE").Scan(&state).Error; err != nil {
			return err
		}
		if !state.PartitionWrites {
			return nil
		}
		var rows []struct{ ID uint64 }
		if err := tx.Raw(`SELECT id FROM usage_logs FORCE INDEX (idx_usage_rollup_pending)
			WHERE rollup_applied = 1 ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED`, BatchSize).Scan(&rows).Error; err != nil {
			return err
		}
		ids := make([]uint64, len(rows))
		for i, row := range rows {
			if row.ID > state.LegacyHighWatermark {
				return errors.New("legacy usage writer committed after partition activation")
			}
			ids[i] = row.ID
		}
		if len(ids) > 0 {
			if err := tx.Exec(`INSERT INTO usage_log_receipts (request_id, usage_id, cost_amount)
				SELECT request_id, usage_id, cost_amount FROM usage_logs WHERE id IN ? ORDER BY id
				ON DUPLICATE KEY UPDATE id = usage_log_receipts.id`, ids).Error; err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO usage_logs_v2
				(id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code,
				 error_code, input_tokens, cache_read_tokens, cache_write_tokens,
				 cache_read_cost_amount, cache_write_cost_amount,
				 output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms,
				 first_token_ms, created_at, rollup_applied)
				SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code,
				 error_code, input_tokens, cache_read_tokens, cache_write_tokens,
				 cache_read_cost_amount, cache_write_cost_amount,
				 output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms,
				 first_token_ms, created_at, rollup_applied FROM usage_logs WHERE id IN ? AND created_at >= ?`, ids, state.RetainedFrom).Error; err != nil {
				return err
			}
			result := tx.Exec("DELETE FROM usage_logs WHERE id IN ? AND rollup_applied = 1", ids)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != int64(len(ids)) {
				return errors.New("usage log move count changed")
			}
			count = len(ids)
		}
		// A database-clock throttle is shared across replicas, including idle polls.
		return tx.Exec(`UPDATE usage_log_backfill SET moved_rows = moved_rows + ?,
			next_batch_at = UTC_TIMESTAMP(6) + INTERVAL 500000 MICROSECOND WHERE id = 1`, count).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, err
	}
	return count, nil
}
