package infrastructure

import (
	"context"
	"errors"
	"math"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// insertUsageRecord requires a caller-owned transaction. Receipts, raw logs,
// expired-request rollups and quota charges therefore commit or roll back together.
func insertUsageRecord(tx *gorm.DB, record *UsageRecord) (bool, error) {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	var state struct {
		RetainedFrom        time.Time
		PartitionWrites     bool
		LegacyHighWatermark uint64
	}
	// Take this lock before receipts, matching the mover's lock order. Shared
	// locks allow concurrent writes; activation/retention briefly fence them.
	if err := tx.Raw("SELECT retained_from, partition_writes, legacy_high_watermark FROM usage_log_retention WHERE id = 1 FOR SHARE").Scan(&state).Error; err != nil {
		return false, err
	}
	// Expected retries should not generate an ERROR log for every duplicate.
	writes := tx.Session(&gorm.Session{Logger: usageRetryLogger{tx.Logger}})
	receipt := writes.Exec(`INSERT INTO usage_log_receipts (request_id, usage_id, cost_amount) VALUES (?, ?, ?)`,
		record.RequestID, record.UsageID, record.CostAmount)
	if duplicateUsage(receipt.Error) {
		return false, nil
	}
	if receipt.Error != nil {
		return false, receipt.Error
	}
	var receiptID uint64
	if err := tx.Raw("SELECT LAST_INSERT_ID()").Scan(&receiptID).Error; err != nil {
		return false, err
	}
	if receiptID > math.MaxUint64-state.LegacyHighWatermark {
		return false, errors.New("usage receipt ID space exhausted")
	}
	// Old versions may still have committed requests in the source. Its unique
	// index is authoritative until the background mover has created each receipt.
	var exists bool
	if err := tx.Raw("SELECT EXISTS(SELECT 1 FROM usage_logs WHERE request_id = ? AND usage_id = ?)",
		record.RequestID, record.UsageID).Scan(&exists).Error; err != nil || exists {
		return false, err
	}
	if !state.PartitionWrites {
		// During a rolling upgrade, old binaries still deduplicate against the
		// source's unique key. Keep new writes there until every writer is upgraded.
		result := writes.Table("usage_logs").Create(record)
		if duplicateUsage(result.Error) {
			return false, nil
		}
		if result.Error != nil {
			return false, result.Error
		}
		return true, nil
	}
	record.ID = state.LegacyHighWatermark + receiptID
	if !record.CreatedAt.Before(state.RetainedFrom) {
		err := tx.Table("usage_logs_v2").Create(record).Error
		return err == nil, err
	}
	// A late report still charges and contributes to historical totals without
	// reviving a sealed raw partition. Decimal rounding stays inside MySQL.
	err := tx.Exec(`INSERT INTO usage_daily_rollups
		(usage_date, organization_id, api_key_id, model, requests, errors,
		 input_tokens, cache_read_tokens, cache_write_tokens,
		 cache_read_cost_amount, cache_write_cost_amount,
		 output_tokens, total_tokens, cost_amount)
		SELECT DATE(CONVERT_TZ(?, '+00:00', '+08:00')), ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, receipt.cost_amount
		FROM usage_log_receipts AS receipt WHERE receipt.id = ?
		ON DUPLICATE KEY UPDATE requests = requests + 1, errors = errors + ?,
		input_tokens = input_tokens + ?, cache_read_tokens = cache_read_tokens + ?,
		cache_write_tokens = cache_write_tokens + ?,
		cache_read_cost_amount = cache_read_cost_amount + ?,
		cache_write_cost_amount = cache_write_cost_amount + ?,
		output_tokens = output_tokens + ?, total_tokens = total_tokens + ?,
		cost_amount = usage_daily_rollups.cost_amount + VALUES(cost_amount)`,
		record.CreatedAt, record.OrganizationID, record.APIKeyID, record.Model, record.StatusCode >= 400,
		record.InputTokens, record.CacheReadTokens, record.CacheWriteTokens,
		record.CacheReadCostAmount, record.CacheWriteCostAmount,
		record.OutputTokens, record.TotalTokens, receiptID,
		record.StatusCode >= 400, record.InputTokens, record.CacheReadTokens, record.CacheWriteTokens,
		record.CacheReadCostAmount, record.CacheWriteCostAmount,
		record.OutputTokens, record.TotalTokens).Error
	return err == nil, err
}

func duplicateUsage(err error) bool {
	var duplicate *drivermysql.MySQLError
	return errors.Is(err, gorm.ErrDuplicatedKey) || errors.As(err, &duplicate) && duplicate.Number == 1062
}

type usageRetryLogger struct{ logger.Interface }

func (log usageRetryLogger) Trace(ctx context.Context, began time.Time, query func() (string, int64), err error) {
	if !duplicateUsage(err) {
		log.Interface.Trace(ctx, began, query, err)
	}
}
