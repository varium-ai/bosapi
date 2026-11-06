package infrastructure

import (
	"context"
	"strings"
	"time"

	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/internal/user/domain"
)

type adminKeyUsageRecord struct {
	APIKeyID                                 uint64
	DailyTokens, WeeklyTokens, MonthlyTokens int64
	DailyAmount, WeeklyAmount, MonthlyAmount float64
	TodayAmount, RecentAmount                float64
}

// Each branch has one equality plus a time range on the covering index. A large
// IN/GROUP BY can otherwise make MySQL walk the entire multi-Key index twice.
func adminKeyUsageQuery(keys []domain.APIKey, now time.Time) (string, []any) {
	day, week, month := usagetime.PeriodStarts(now)
	recent := now.Add(-30 * 24 * time.Hour)
	start := recent
	for _, boundary := range []time.Time{week, month} {
		if boundary.Before(start) {
			start = boundary
		}
	}
	branches := make([]string, 0, len(keys))
	args := make([]any, 0, 15*len(keys))
	for _, key := range keys {
		if key.ID == 0 {
			continue
		}
		branches = append(branches, `SELECT ? AS api_key_id,
			COALESCE(SUM(CASE WHEN created_at >= ? THEN total_tokens ELSE 0 END),0) AS daily_tokens,
			COALESCE(SUM(CASE WHEN created_at >= ? THEN cost_amount ELSE 0 END),0) AS daily_amount,
			COALESCE(SUM(CASE WHEN created_at >= ? THEN total_tokens ELSE 0 END),0) AS weekly_tokens,
			COALESCE(SUM(CASE WHEN created_at >= ? THEN cost_amount ELSE 0 END),0) AS weekly_amount,
			COALESCE(SUM(CASE WHEN created_at >= ? THEN total_tokens ELSE 0 END),0) AS monthly_tokens,
			COALESCE(SUM(CASE WHEN created_at >= ? THEN cost_amount ELSE 0 END),0) AS monthly_amount,
			COALESCE(SUM(CASE WHEN created_at >= ? AND created_at <= ? THEN cost_amount ELSE 0 END),0) AS today_amount,
			COALESCE(SUM(CASE WHEN created_at >= ? AND created_at <= ? THEN cost_amount ELSE 0 END),0) AS recent_amount
			FROM (
                SELECT created_at, total_tokens, cost_amount FROM usage_logs_v2 FORCE INDEX (idx_usage_key_time_totals) WHERE api_key_id = ? AND created_at >= ?
                UNION ALL SELECT created_at, total_tokens, cost_amount FROM usage_logs FORCE INDEX (idx_usage_key_time_totals) WHERE api_key_id = ? AND created_at >= ?
            ) AS usage_source`)
		args = append(args, key.ID, day, day, week, week, month, month, day, now, recent, now, key.ID, start, key.ID, start)
	}
	return strings.Join(branches, " UNION ALL "), args
}

func (repository *KeyRepository) populateAdminKeyUsageAt(ctx context.Context, keys []domain.APIKey, now time.Time) error {
	for i := range keys {
		keys[i].UsageTodayAmount, keys[i].Usage30dAmount = nil, nil
	}
	query, args := adminKeyUsageQuery(keys, now.UTC())
	if query == "" {
		return nil
	}
	var rows []adminKeyUsageRecord
	if err := repository.database.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return err
	}
	byKey := make(map[uint64]adminKeyUsageRecord, len(rows))
	for _, row := range rows {
		byKey[row.APIKeyID] = row
	}
	for i := range keys {
		row := byKey[keys[i].ID]
		keys[i].DailyUsedTokens, keys[i].WeeklyUsedTokens, keys[i].MonthlyUsedTokens = row.DailyTokens, row.WeeklyTokens, row.MonthlyTokens
		keys[i].DailyUsedAmount, keys[i].WeeklyUsedAmount, keys[i].MonthlyUsedAmount = row.DailyAmount, row.WeeklyAmount, row.MonthlyAmount
		today, recent := row.TodayAmount, row.RecentAmount
		keys[i].UsageTodayAmount, keys[i].Usage30dAmount = &today, &recent
	}
	return nil
}
