package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"bosapi/backend/internal/platform/snapshot"
	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/internal/user/domain"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func NewCachedKeyRepository(database *gorm.DB, client redis.UniversalClient) *KeyRepository {
	return &KeyRepository{database: database, adminUsageCache: snapshot.New(client, "bos:admin-key-usage:v1:", 4*time.Minute, 5*time.Minute, 128)}
}
func (r *KeyRepository) Close() {
	if r.adminUsageCache != nil {
		r.adminUsageCache.Close()
	}
}
func (r *KeyRepository) populateCachedAdminUsage(ctx context.Context, keys []domain.APIKey) {
	if len(keys) == 0 {
		return
	}
	ids := make([]uint64, 0, len(keys))
	scope := make([][2]uint64, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.ID)
		scope = append(scope, [2]uint64{k.ID, k.OrganizationID})
	}
	sort.Slice(scope, func(i, j int) bool { return scope[i][0] < scope[j][0] })
	key := snapshot.Key(struct {
		Scope [][2]uint64
		Day   string
	}{scope, usagetime.DayStart(time.Now()).Format("2006-01-02")})
	value, err := r.adminUsageCache.Get(ctx, key, false, func(ctx context.Context) (json.RawMessage, time.Time, error) {
		now := time.Now().UTC()
		var rows []adminKeyUsageRecord
		query, args := adminKeyRollupQuery(ids, now)
		err := r.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return tx.Raw(query, args...).Scan(&rows).Error }, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return nil, time.Time{}, err
		}
		data, err := json.Marshal(rows)
		return data, usagetime.DayStart(now).Add(24 * time.Hour), err
	})
	for i := range keys {
		keys[i].UsageStatsStatus = value.Status
		if !value.GeneratedAt.IsZero() {
			at := value.GeneratedAt
			keys[i].UsageStatsAsOf = &at
		}
		keys[i].UsageTodayAmount = nil
		keys[i].Usage30dAmount = nil
	}
	if err != nil {
		return
	}
	var rows []adminKeyUsageRecord
	if json.Unmarshal(value.Data, &rows) != nil {
		return
	}
	byID := make(map[uint64]adminKeyUsageRecord, len(rows))
	for _, v := range rows {
		byID[v.APIKeyID] = v
	}
	for i := range keys {
		row := byID[keys[i].ID]
		keys[i].DailyUsedTokens, keys[i].WeeklyUsedTokens, keys[i].MonthlyUsedTokens = row.DailyTokens, row.WeeklyTokens, row.MonthlyTokens
		keys[i].DailyUsedAmount, keys[i].WeeklyUsedAmount, keys[i].MonthlyUsedAmount = row.DailyAmount, row.WeeklyAmount, row.MonthlyAmount
		today, recent := row.TodayAmount, row.RecentAmount
		keys[i].UsageTodayAmount = &today
		keys[i].Usage30dAmount = &recent
	}
}

// Full days use materialized DECIMAL sums plus the unprocessed tail. Only the
// partial rolling-window start and future records within today read raw history.
// This preserves the original natural-period and rolling-30x24h semantics.
func adminKeyRollupQuery(ids []uint64, now time.Time) (string, []any) {
	day, week, month := usagetime.PeriodStarts(now)
	recent := now.Add(-30 * 24 * time.Hour)
	recentDay := usagetime.DayStart(recent)
	start := recentDay
	for _, v := range []time.Time{week, month} {
		if v.Before(start) {
			start = v
		}
	}
	date := func(t time.Time) string { return t.In(usagetime.Location()).Format("2006-01-02") }
	args := []any{ids, date(start), ids, start}
	corrections := []string{}
	for _, id := range ids {
		for _, table := range []string{"usage_logs", "usage_logs_v2"} {
			corrections = append(corrections, "SELECT ? AS api_key_id, COALESCE(SUM(cost_amount),0) AS before_recent, 0 AS after_now FROM "+table+" FORCE INDEX (idx_usage_key_time_totals) WHERE api_key_id = ? AND created_at >= ? AND created_at < ?")
			args = append(args, id, id, recentDay, recent)
			corrections = append(corrections, "SELECT ? AS api_key_id, 0 AS before_recent, COALESCE(SUM(cost_amount),0) AS after_now FROM "+table+" FORCE INDEX (idx_usage_key_time_totals) WHERE api_key_id = ? AND created_at > ? AND created_at < ?")
			args = append(args, id, id, now, day.Add(24*time.Hour))
		}
	}
	query := `WITH source AS (
 SELECT api_key_id, usage_date, total_tokens, cost_amount FROM usage_daily_rollups WHERE api_key_id IN ? AND usage_date >= ?
 UNION ALL SELECT api_key_id, DATE(CONVERT_TZ(created_at,'+00:00','+08:00')) AS usage_date, total_tokens, cost_amount FROM usage_logs_pending WHERE api_key_id IN ? AND created_at >= ?
 ), adjustments AS (SELECT api_key_id,SUM(before_recent) AS before_recent,SUM(after_now) AS after_now FROM (` + strings.Join(corrections, " UNION ALL ") + `) a GROUP BY api_key_id), totals AS (
 SELECT api_key_id,
 SUM(CASE WHEN usage_date >= ? THEN total_tokens ELSE 0 END) AS daily_tokens,
 SUM(CASE WHEN usage_date >= ? THEN cost_amount ELSE 0 END) AS daily_amount,
 SUM(CASE WHEN usage_date >= ? THEN total_tokens ELSE 0 END) AS weekly_tokens,
 SUM(CASE WHEN usage_date >= ? THEN cost_amount ELSE 0 END) AS weekly_amount,
 SUM(CASE WHEN usage_date >= ? THEN total_tokens ELSE 0 END) AS monthly_tokens,
 SUM(CASE WHEN usage_date >= ? THEN cost_amount ELSE 0 END) AS monthly_amount,
 SUM(CASE WHEN usage_date = ? THEN cost_amount ELSE 0 END) AS today_amount,
 SUM(CASE WHEN usage_date >= ? AND usage_date <= ? THEN cost_amount ELSE 0 END) AS recent_amount
 FROM source GROUP BY api_key_id)
 SELECT totals.api_key_id,daily_tokens,daily_amount,weekly_tokens,weekly_amount,monthly_tokens,monthly_amount,
 today_amount-COALESCE(after_now,0) AS today_amount,
 recent_amount-COALESCE(before_recent,0)-COALESCE(after_now,0) AS recent_amount
 FROM totals LEFT JOIN adjustments ON adjustments.api_key_id=totals.api_key_id`
	args = append(args, date(day), date(day), date(week), date(week), date(month), date(month), date(day), date(recentDay), date(day))
	return query, args
}
