package interfaces

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"bosapi/backend/internal/platform/usagetime"
	"gorm.io/gorm"
)

// Keep availability identical for Key presence and successful-call evidence.
// The optional evidence predicates must be evaluated before a branch's LIMIT.
const setupAvailabilitySQL = `EXISTS (SELECT 1 FROM accounts a JOIN provider_models pm ON pm.account_id = a.id
 LEFT JOIN organization_provider_bindings b ON b.account_id = a.id AND b.organization_id = k.organization_id
 JOIN organizations o ON o.id = k.organization_id
 WHERE a.status = 'active' AND a.schedulable = 1 AND pm.status = 'active' AND pm.model_name <> '' AND pm.model_name <> '*'
 AND (b.status = 'active'
  AND (b.model_allowlist IS NULL OR JSON_TYPE(b.model_allowlist) = 'NULL' OR JSON_LENGTH(b.model_allowlist) = 0 OR JSON_CONTAINS(b.model_allowlist, JSON_QUOTE(pm.model_name))))
 AND (k.allowed_provider_ids IS NULL OR JSON_TYPE(k.allowed_provider_ids) = 'NULL' OR JSON_LENGTH(k.allowed_provider_ids) = 0 OR JSON_CONTAINS(k.allowed_provider_ids, CAST(a.id AS JSON)))
 AND (k.allowed_models IS NULL OR JSON_TYPE(k.allowed_models) = 'NULL' OR JSON_LENGTH(k.allowed_models) = 0 OR JSON_CONTAINS(k.allowed_models, JSON_QUOTE(pm.model_name)))
 %s)`

type setupKeyCandidate struct {
	ID               uint64
	UpdatedAt        *time.Time
	HasPeriodicQuota bool
}

func setupCandidateQuery(db *gorm.DB, organizationID uint64, now time.Time, private bool) *gorm.DB {
	scope := ""
	return db.Table("api_keys AS k").
		Select(`k.id, k.updated_at, (k.daily_quota_amount IS NOT NULL OR k.daily_quota_tokens IS NOT NULL
 OR k.weekly_quota_amount IS NOT NULL OR k.weekly_quota_tokens IS NOT NULL
 OR k.monthly_quota_amount IS NOT NULL OR k.monthly_quota_tokens IS NOT NULL) AS has_periodic_quota`).
		Where("k.organization_id = ? AND k.status = 'active'", organizationID).
		Where("k.expires_at IS NULL OR k.expires_at > ?", now).
		Where("k.quota_amount IS NULL OR COALESCE(k.quota_used_amount, 0) < k.quota_amount").
		Where("k.quota_tokens IS NULL OR COALESCE(k.quota_used_tokens, 0) < k.quota_tokens").
		Where(fmt.Sprintf(setupAvailabilitySQL, scope))
}

// Rollup acknowledgement and source deletion can race this endpoint. One read
// snapshot sees either side of each move/batch, never both or neither.
func loadSetupKeyStatus(db *gorm.DB, organizationID uint64, organizationUpdatedAt, now time.Time, private bool) (bool, *time.Time, error) {
	var usable bool
	var latest *time.Time
	err := db.Transaction(func(tx *gorm.DB) error {
		const pageSize = 100
		var after uint64
		for {
			var keys []setupKeyCandidate
			if err := setupCandidateQuery(tx, organizationID, now, private).
				Where("k.id > ?", after).Order("k.id").Limit(pageSize).Scan(&keys).Error; err != nil {
				return err
			}
			for _, key := range keys {
				if key.HasPeriodicQuota {
					var budget struct{ Usable bool }
					if err := setupPeriodicQuotaQuery(tx, key.ID, now).Scan(&budget).Error; err != nil {
						return err
					}
					if !budget.Usable {
						continue
					}
				}
				usable = true
				// SQL's original created_at >= NULL predicate admits no evidence.
				if key.UpdatedAt == nil {
					continue
				}
				from := organizationUpdatedAt
				if key.UpdatedAt.After(from) {
					from = *key.UpdatedAt
				}
				var evidence struct{ CreatedAt *time.Time }
				if err := setupEvidenceQuery(tx, key.ID, organizationID, from, latest, private).Scan(&evidence).Error; err != nil {
					return err
				}
				if evidence.CreatedAt != nil {
					latest = evidence.CreatedAt
				}
			}
			if len(keys) < pageSize {
				return nil
			}
			after = keys[len(keys)-1].ID
		}
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return usable, latest, err
}

// Every source is aggregated once for a literal Key and period range. Do not
// correlate against the UNION view: that materializes other Keys' history.
// All six comparisons stay in SQL to preserve DECIMAL and BIGINT precision.
func setupPeriodicQuotaQuery(db *gorm.DB, keyID uint64, now time.Time) *gorm.DB {
	day, week, month := usagetime.PeriodStarts(now)
	periods := []struct {
		name  string
		start time.Time
	}{{"daily", day}, {"weekly", week}, {"monthly", month}}
	start := month
	if week.Before(start) {
		start = week
	}
	var branches, totals, conditions []string
	var args []any
	for _, source := range []string{"usage_daily_rollups", "usage_logs", "usage_logs_v2"} {
		var sums []string
		column, index := "created_at", "idx_usage_key_pending_time"
		var boundary any = start
		if source == "usage_daily_rollups" {
			column, index = "usage_date", "idx_rollup_key_date"
			boundary = start.In(usagetime.Location()).Format("2006-01-02")
		}
		for _, period := range periods {
			for _, metric := range []struct{ name, column string }{{"amount", "cost_amount"}, {"tokens", "total_tokens"}} {
				alias := period.name + "_" + metric.name
				sums = append(sums, "SUM(CASE WHEN "+column+" >= ? THEN "+metric.column+" ELSE 0 END) AS "+alias)
				if source == "usage_daily_rollups" {
					args = append(args, period.start.In(usagetime.Location()).Format("2006-01-02"))
					totals = append(totals, "COALESCE(SUM("+alias+"),0) AS "+alias)
					limit := "k." + period.name + "_quota_" + metric.name
					conditions = append(conditions, "("+limit+" IS NULL OR totals."+alias+" < "+limit+")")
				} else {
					args = append(args, period.start)
				}
			}
		}
		where := "api_key_id = ? AND " + column + " >= ?"
		if source != "usage_daily_rollups" {
			where += " AND rollup_applied = 0"
		}
		branches = append(branches, "SELECT "+strings.Join(sums, ", ")+" FROM "+source+" FORCE INDEX ("+index+") WHERE "+where)
		args = append(args, keyID, boundary)
	}
	args = append(args, keyID)
	return db.Raw("SELECT ("+strings.Join(conditions, " AND ")+") AS usable FROM api_keys AS k CROSS JOIN (SELECT "+
		strings.Join(totals, ", ")+" FROM ("+strings.Join(branches, " UNION ALL ")+") AS parts) AS totals WHERE k.id = ?", args...)
}

func setupEvidenceQuery(db *gorm.DB, keyID, organizationID uint64, from time.Time, latest *time.Time, private bool) *gorm.DB {
	scope := ""
	scope += ` AND (pm.model_name = u.model OR (a.api_format = 'gemini' AND REPLACE(pm.model_name, 'models/', '') = u.model))
 AND u.created_at >= a.updated_at AND u.created_at >= pm.updated_at`
	branch := func(table string) *gorm.DB {
		// A scalar CASE keeps EXISTS out of the outer semijoin. Otherwise MySQL
		// can enumerate matching history per provider and sort before LIMIT 1,
		// even when EXPLAIN reports the expected Key index.
		query := db.Table(table+" AS u FORCE INDEX (idx_usage_key_time)").Select("u.created_at").
			Joins("JOIN api_keys AS k ON k.id = u.api_key_id").
			Where("u.api_key_id = ? AND u.organization_id = ? AND u.created_at >= ?", keyID, organizationID, from).
			Where("u.status_code >= 200 AND u.status_code < 300 AND u.error_code = ''").
			Where("CASE WHEN " + fmt.Sprintf(setupAvailabilitySQL, scope) + " THEN 1 ELSE 0 END = 1")
		if latest != nil {
			query = query.Where("u.created_at > ?", *latest)
		}
		return query.Order("u.created_at DESC").Limit(1)
	}
	return db.Raw("SELECT created_at FROM ((?) UNION ALL (?)) AS calls ORDER BY created_at DESC LIMIT 1",
		branch("usage_logs"), branch("usage_logs_v2"))
}
