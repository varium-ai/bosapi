package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"bosapi/backend/internal/admin/domain"
	"gorm.io/gorm"
)

// Pending probes and summaries share one repeatable-read snapshot. Limit each
// physical pending branch; a saturated probe makes freshness unknown instead of
// reporting a false high-water mark. No all-history COUNT runs on this path.
func (r *AdminUsageRepository) loadUsageSnapshot(ctx context.Context, filter domain.AdminUsageFilter, load func(domain.AdminUsageRepository) (any, error)) (json.RawMessage, time.Time, error) {
	var data json.RawMessage
	now := time.Now()
	validUntil := now.Add(adminUsageCacheTTL)
	err := r.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		source := *r
		source.database = tx
		if r.materializedOnly && supportsUsageRollups(filter) {
			for _, table := range []string{"usage_logs", "usage_logs_v2"} {
				var rows []struct{ CreatedAt time.Time }
				if err := tx.Table(table + " FORCE INDEX (idx_usage_rollup_pending)").Select("created_at").Where("rollup_applied = 0").Order("id").Limit(1001).Scan(&rows).Error; err != nil {
					return err
				}
				if len(rows) == 1001 {
					validUntil = now
					continue
				}
				for _, row := range rows {
					if deadline := row.CreatedAt.Add(adminUsageCacheTTL); deadline.Before(validUntil) {
						validUntil = deadline
					}
				}
			}
		}
		value, err := load(&source)
		if err != nil {
			return err
		}
		data, err = json.Marshal(value)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return data, validUntil, err
}
