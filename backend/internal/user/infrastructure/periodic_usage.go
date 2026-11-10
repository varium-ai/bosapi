package infrastructure

import (
	"context"
	"time"

	"gorm.io/gorm"

	"bosapi/backend/internal/platform/usagetime"
)

// periodicUsageQuery reads counters and unacknowledged logs in one statement,
// so concurrent rollup acknowledgements cannot omit or double-count usage.
// Quotas deliberately include a Key's history across organization changes.
// Each branch clones the session so a context-bound DB or transaction cannot
// share mutable SELECT/WHERE clauses with another branch or the outer query.
func periodicUsageQuery(db *gorm.DB, ids []uint64, now time.Time) *gorm.DB {
	day, week, month := usagetime.PeriodStarts(now)
	start := month
	if week.Before(start) {
		start = week
	}
	date := func(at time.Time) string { return at.In(usagetime.Location()).Format("2006-01-02") }
	stored := db.Session(&gorm.Session{}).Table("usage_daily_rollups FORCE INDEX (idx_rollup_key_date)").
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
		return db.Session(&gorm.Session{}).Table(table).
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
	return db.Session(&gorm.Session{}).Table("(? UNION ALL ? UNION ALL ?) AS periodic_usage", stored,
		pending("usage_logs_v2 FORCE INDEX (idx_usage_key_pending_time)"),
		pending("usage_logs FORCE INDEX (idx_usage_key_pending_time)")).
		Select(`api_key_id, COALESCE(SUM(daily_tokens), 0) AS daily_tokens,
		 COALESCE(SUM(daily_amount), 0) AS daily_amount,
		 COALESCE(SUM(weekly_tokens), 0) AS weekly_tokens,
		 COALESCE(SUM(weekly_amount), 0) AS weekly_amount,
		 COALESCE(SUM(monthly_tokens), 0) AS monthly_tokens,
		 COALESCE(SUM(monthly_amount), 0) AS monthly_amount`).Group("api_key_id")
}

// OrganizationPeriodicUsage holds calendar totals for one tenant.
type OrganizationPeriodicUsage struct {
	OrganizationID uint64  `json:"organization_id"`
	DailyTokens    int64   `json:"daily_tokens"`
	DailyAmount    float64 `json:"daily_amount"`
	WeeklyTokens   int64   `json:"weekly_tokens"`
	WeeklyAmount   float64 `json:"weekly_amount"`
	MonthlyTokens  int64   `json:"monthly_tokens"`
	MonthlyAmount  float64 `json:"monthly_amount"`
}

type organizationPeriodicUsageRecord struct {
	OrganizationID uint64  `gorm:"column:organization_id"`
	DailyTokens    int64   `gorm:"column:daily_tokens"`
	DailyAmount    float64 `gorm:"column:daily_amount"`
	WeeklyTokens   int64   `gorm:"column:weekly_tokens"`
	WeeklyAmount   float64 `gorm:"column:weekly_amount"`
	MonthlyTokens  int64   `gorm:"column:monthly_tokens"`
	MonthlyAmount  float64 `gorm:"column:monthly_amount"`
}

// organizationPeriodicUsageQuery reads stored rollups and unacknowledged raw
// rows in one statement. Raw rows are probed by organization and time.
func organizationPeriodicUsageQuery(db *gorm.DB, organizationID uint64, now time.Time) *gorm.DB {
	day, week, month := usagetime.PeriodStarts(now)
	start := month
	if week.Before(start) {
		start = week
	}
	date := func(at time.Time) string { return at.In(usagetime.Location()).Format("2006-01-02") }
	stored := db.Session(&gorm.Session{}).Table("usage_daily_rollups FORCE INDEX (idx_rollup_org_date)").
		Select(`organization_id,
			 SUM(CASE WHEN usage_date >= ? THEN total_tokens ELSE 0 END) AS daily_tokens,
			 SUM(CASE WHEN usage_date >= ? THEN cost_amount ELSE 0 END) AS daily_amount,
			 SUM(CASE WHEN usage_date >= ? THEN total_tokens ELSE 0 END) AS weekly_tokens,
			 SUM(CASE WHEN usage_date >= ? THEN cost_amount ELSE 0 END) AS weekly_amount,
			 SUM(CASE WHEN usage_date >= ? THEN total_tokens ELSE 0 END) AS monthly_tokens,
			 SUM(CASE WHEN usage_date >= ? THEN cost_amount ELSE 0 END) AS monthly_amount`,
			date(day), date(day), date(week), date(week), date(month), date(month)).
		Where("organization_id = ? AND usage_date >= ?", organizationID, date(start)).
		Group("organization_id")
	pending := func(table string) *gorm.DB {
		return db.Session(&gorm.Session{}).Table(table).
			Select(`organization_id,
				 SUM(CASE WHEN created_at >= ? THEN total_tokens ELSE 0 END) AS daily_tokens,
				 SUM(CASE WHEN created_at >= ? THEN cost_amount ELSE 0 END) AS daily_amount,
				 SUM(CASE WHEN created_at >= ? THEN total_tokens ELSE 0 END) AS weekly_tokens,
				 SUM(CASE WHEN created_at >= ? THEN cost_amount ELSE 0 END) AS weekly_amount,
				 SUM(CASE WHEN created_at >= ? THEN total_tokens ELSE 0 END) AS monthly_tokens,
				 SUM(CASE WHEN created_at >= ? THEN cost_amount ELSE 0 END) AS monthly_amount`,
				day, day, week, week, month, month).
			Where("organization_id = ? AND rollup_applied = 0 AND created_at >= ?", organizationID, start).
			Group("organization_id")
	}
	return db.Session(&gorm.Session{}).Table("(? UNION ALL ? UNION ALL ?) AS organization_periodic_usage", stored,
		pending("usage_logs_v2 FORCE INDEX (idx_usage_org_pending_time)"),
		pending("usage_logs FORCE INDEX (idx_usage_org_pending_time)")).
		Select(`organization_id, COALESCE(SUM(daily_tokens), 0) AS daily_tokens,
			 COALESCE(SUM(daily_amount), 0) AS daily_amount,
			 COALESCE(SUM(weekly_tokens), 0) AS weekly_tokens,
			 COALESCE(SUM(weekly_amount), 0) AS weekly_amount,
			 COALESCE(SUM(monthly_tokens), 0) AS monthly_tokens,
			 COALESCE(SUM(monthly_amount), 0) AS monthly_amount`).
		Group("organization_id")
}

// FindOrganizationPeriodicUsage returns calendar totals for a tenant.
func FindOrganizationPeriodicUsage(ctx context.Context, db *gorm.DB, organizationID uint64, now time.Time) (OrganizationPeriodicUsage, error) {
	var records []organizationPeriodicUsageRecord
	if err := organizationPeriodicUsageQuery(db.WithContext(ctx), organizationID, now).Find(&records).Error; err != nil {
		return OrganizationPeriodicUsage{}, err
	}
	if len(records) == 0 {
		return OrganizationPeriodicUsage{OrganizationID: organizationID}, nil
	}
	return OrganizationPeriodicUsage{
		OrganizationID: records[0].OrganizationID,
		DailyTokens:    records[0].DailyTokens,
		DailyAmount:    records[0].DailyAmount,
		WeeklyTokens:   records[0].WeeklyTokens,
		WeeklyAmount:   records[0].WeeklyAmount,
		MonthlyTokens:  records[0].MonthlyTokens,
		MonthlyAmount:  records[0].MonthlyAmount,
	}, nil
}
