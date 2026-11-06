package infrastructure

import (
	"time"

	"gorm.io/gorm"

	"bosapi/backend/internal/platform/usagetime"
)

// periodicUsageQuery reads counters and unacknowledged logs in one statement,
// so concurrent rollup acknowledgements cannot omit or double-count usage.
// Quotas deliberately include a Key's history across organization changes.
func periodicUsageQuery(db *gorm.DB, ids []uint64, now time.Time) *gorm.DB {
	day, week, month := usagetime.PeriodStarts(now)
	start := month
	if week.Before(start) {
		start = week
	}
	date := func(at time.Time) string { return at.In(usagetime.Location()).Format("2006-01-02") }
	stored := db.Table("usage_daily_rollups FORCE INDEX (idx_rollup_key_date)").
		Select(`api_key_id,
		 SUM(CASE WHEN usage_date >= ? THEN total_tokens ELSE 0 END) AS daily_tokens,
		 SUM(CASE WHEN usage_date >= ? THEN cost_amount ELSE 0 END) AS daily_amount,
		 SUM(CASE WHEN usage_date >= ? THEN total_tokens ELSE 0 END) AS weekly_tokens,
		 SUM(CASE WHEN usage_date >= ? THEN cost_amount ELSE 0 END) AS weekly_amount,
		 SUM(CASE WHEN usage_date >= ? THEN total_tokens ELSE 0 END) AS monthly_tokens,
		 SUM(CASE WHEN usage_date >= ? THEN cost_amount ELSE 0 END) AS monthly_amount`,
			date(day), date(day), date(week), date(week), date(month), date(month)).
		Where("api_key_id IN ? AND usage_date >= ?", ids, date(start)).Group("api_key_id")
	pending := func(table string) *gorm.DB {
		return db.Table(table).
			Select(`api_key_id,
			 SUM(CASE WHEN created_at >= ? THEN total_tokens ELSE 0 END) AS daily_tokens,
			 SUM(CASE WHEN created_at >= ? THEN cost_amount ELSE 0 END) AS daily_amount,
			 SUM(CASE WHEN created_at >= ? THEN total_tokens ELSE 0 END) AS weekly_tokens,
			 SUM(CASE WHEN created_at >= ? THEN cost_amount ELSE 0 END) AS weekly_amount,
			 SUM(CASE WHEN created_at >= ? THEN total_tokens ELSE 0 END) AS monthly_tokens,
			 SUM(CASE WHEN created_at >= ? THEN cost_amount ELSE 0 END) AS monthly_amount`,
				day, day, week, week, month, month).
			Where("api_key_id IN ? AND rollup_applied = 0 AND created_at >= ?", ids, start).Group("api_key_id")
	}
	return db.Table("(? UNION ALL ? UNION ALL ?) AS periodic_usage", stored,
		pending("usage_logs_v2 FORCE INDEX (idx_usage_key_pending_time)"),
		pending("usage_logs FORCE INDEX (idx_usage_key_pending_time)")).
		Select(`api_key_id, COALESCE(SUM(daily_tokens), 0) AS daily_tokens,
		 COALESCE(SUM(daily_amount), 0) AS daily_amount,
		 COALESCE(SUM(weekly_tokens), 0) AS weekly_tokens,
		 COALESCE(SUM(weekly_amount), 0) AS weekly_amount,
		 COALESCE(SUM(monthly_tokens), 0) AS monthly_tokens,
		 COALESCE(SUM(monthly_amount), 0) AS monthly_amount`).Group("api_key_id")
}
