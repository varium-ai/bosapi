package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/internal/user/domain"
	"gorm.io/gorm"
)

// customPeriodicUsageQuery combines full UTC days with exact raw boundary
// slices and pending receipts in one statement. Rolled-up rows in the boundary
// slices are read from raw history, so no receipt is counted twice.
func customPeriodicUsageQuery(db *gorm.DB, column string, id uint64, now time.Time, anchor *time.Time) *gorm.DB {
	starts, resets := usagetime.QuotaPeriods(now, anchor)
	branches := make([]*gorm.DB, 0, 3)
	for i, start := range starts {
		end := resets[i]
		if end.After(now) {
			end = now
		}
		if start.After(end) {
			end = start
		}
		fullStart := usagetime.DayStart(start)
		if fullStart.Before(start) {
			fullStart = fullStart.Add(24 * time.Hour)
		}
		fullEnd := usagetime.DayStart(end)
		if fullEnd.Before(fullStart) {
			fullEnd = fullStart
		}
		stored := db.Session(&gorm.Session{}).Table("usage_daily_rollups").
			Select("total_tokens, cost_amount").Where(column+" = ? AND usage_date >= ? AND usage_date < ?", id, fullStart.Format(time.DateOnly), fullEnd.Format(time.DateOnly))
		raw := func(table string) *gorm.DB {
			return db.Session(&gorm.Session{}).Table(table).Select("total_tokens, cost_amount").
				Where(column+" = ? AND created_at >= ? AND created_at < ? AND (rollup_applied = 0 OR created_at < ? OR created_at >= ?)", id, start, end, fullStart, fullEnd)
		}
		branches = append(branches, db.Session(&gorm.Session{}).Table("(? UNION ALL ? UNION ALL ?) AS cycle_usage", stored, raw("usage_logs"), raw("usage_logs_v2")).
			Select("? AS period, COALESCE(SUM(total_tokens),0) AS tokens, COALESCE(SUM(cost_amount),0) AS amount", i))
	}
	return db.Session(&gorm.Session{}).Table("(? UNION ALL ? UNION ALL ?) AS quota_usage", branches[0], branches[1], branches[2]).Select("*")
}

type customPeriodUsage struct {
	Period int
	Tokens int64
	Amount float64
}

func readCustomPeriods(ctx context.Context, db *gorm.DB, column string, id uint64, now time.Time, anchor *time.Time) ([3]customPeriodUsage, error) {
	var rows []customPeriodUsage
	var result [3]customPeriodUsage
	err := customPeriodicUsageQuery(db.WithContext(ctx), column, id, now, anchor).Scan(&rows).Error
	for _, row := range rows {
		if row.Period >= 0 && row.Period < 3 {
			result[row.Period] = row
		}
	}
	return result, err
}

func (r *KeyRepository) populateCustomPeriodicUsage(ctx context.Context, keys []domain.APIKey, now time.Time) error {
	for i := range keys {
		k := &keys[i]
		if k.QuotaResetStartAt == nil {
			continue
		}
		u, err := readCustomPeriods(ctx, r.database, "api_key_id", k.ID, now, k.QuotaResetStartAt)
		if err != nil {
			return err
		}
		k.DailyUsedTokens, k.WeeklyUsedTokens, k.MonthlyUsedTokens = u[0].Tokens, u[1].Tokens, u[2].Tokens
		k.DailyUsedAmount, k.WeeklyUsedAmount, k.MonthlyUsedAmount = u[0].Amount, u[1].Amount, u[2].Amount
	}
	return nil
}

// Read the anchor with the usage reader too, so admin snapshots and gateway
// cold starts cannot silently fall back to natural calendar totals.
func organizationQuotaAnchor(ctx context.Context, db *gorm.DB, id uint64) (*time.Time, error) {
	var rows []struct{ ValueText *string }
	// SQLite is used by focused query tests and has no application settings table.
	if db.Dialector.Name() == "sqlite" {
		return nil, nil
	}
	if err := db.WithContext(ctx).Table("settings").Select("value_text").Where("setting_key = ?", fmt.Sprintf("rate_limit.organization.%d", id)).Limit(1).Scan(&rows).Error; err != nil {
		return nil, err
	}
	var config struct {
		Start *time.Time `json:"quota_reset_start_at"`
	}
	if len(rows) > 0 && rows[0].ValueText != nil {
		if err := json.Unmarshal([]byte(*rows[0].ValueText), &config); err != nil {
			return nil, err
		}
	}
	return config.Start, nil
}
