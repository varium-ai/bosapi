package infrastructure

import (
	"bosapi/backend/internal/admin/domain"
	"gorm.io/gorm"
)

// Bound both physical branches before materializing the transition UNION. The
// mover transfers rows atomically and preserves globally unique public IDs.
func adminUsagePageQuery(database *gorm.DB, filter domain.AdminUsageFilter, offset, limit int) *gorm.DB {
	branch := func(table string) *gorm.DB {
		q := applyAdminUsageFilter(database.Table(table+" AS usage_logs"), filter)
		if filter.Before != nil {
			q = q.Where("(usage_logs.created_at < ? OR (usage_logs.created_at = ? AND usage_logs.id < ?))", *filter.Before, *filter.Before, filter.BeforeID)
		}
		return q.Select("usage_logs.*").Order("usage_logs.created_at DESC, usage_logs.id DESC").Limit(offset + limit)
	}
	return database.Table("((?) UNION ALL (?)) AS usage_logs", branch("usage_logs_v2"), branch("usage_logs"))
}
