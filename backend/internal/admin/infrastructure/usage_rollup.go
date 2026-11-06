package infrastructure

import (
	"context"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/geoip"
	"bosapi/backend/internal/platform/usagetime"
	"gorm.io/gorm"
)

// NewPreaggregatedAdminUsageRepository serves whole business days from stored
// rollups plus the unprocessed tail, in a single consistent SQL statement.
func NewPreaggregatedAdminUsageRepository(database *gorm.DB, resolver ...*geoip.Resolver) *AdminUsageRepository {
	repository := NewAdminUsageRepository(database, resolver...)
	repository.dailyRollups = true
	return repository
}

func supportsUsageRollups(filter domain.AdminUsageFilter) bool {
	if filter.ClientIP != "" {
		return false
	}
	for _, boundary := range []*time.Time{filter.From, filter.To} {
		if boundary != nil && !boundary.Equal(usagetime.DayStart(*boundary)) {
			return false
		}
	}
	return true
}

func (repository *AdminUsageRepository) organizationUsageBuckets(database *gorm.DB, filter domain.AdminOrganizationUsageFilter) *gorm.DB {
	if !repository.dailyRollups || !supportsUsageRollups(filter.AdminUsageFilter) {
		return adminOrganizationUsageBuckets(database, filter)
	}
	query := repository.usageBuckets(database, filter.AdminUsageFilter)
	if filter.Search != "" {
		organizations := database.Table("organizations AS org").Select("org.id").Where("LOCATE(?, org.name) > 0", filter.Search)
		query = query.Where("combined.organization_id IN (?) OR CAST(combined.organization_id AS CHAR) = ?", organizations, filter.Search)
	}
	return query
}

// The pending predicate and rollup counters change in the same transaction.
// UNION ALL sees both sources at one snapshot, before or after each batch, so
// requests are neither omitted nor counted twice while a backfill is running.
func storedUsageBuckets(database *gorm.DB, filter domain.AdminUsageFilter) *gorm.DB {
	stored := database.Table("usage_daily_rollups").
		Select("DATE_FORMAT(usage_date, '%Y-%m-%d') AS period, organization_id, api_key_id, model, requests, errors, input_tokens, cache_read_tokens, cache_write_tokens, cache_read_cost_amount, cache_write_cost_amount, output_tokens, total_tokens, cost_amount")
	if filter.OrganizationID > 0 {
		stored = stored.Where("organization_id = ?", filter.OrganizationID)
	}
	if filter.APIKeyID > 0 {
		stored = stored.Where("api_key_id = ?", filter.APIKeyID)
	}
	if filter.Model != "" {
		stored = stored.Where("model = ?", filter.Model)
	}
	if filter.From != nil {
		stored = stored.Where("usage_date >= ?", filter.From.In(usagetime.Location()).Format("2006-01-02"))
	}
	if filter.To != nil {
		stored = stored.Where("usage_date < ?", filter.To.In(usagetime.Location()).Format("2006-01-02"))
	}
	return stored
}
func (repository *AdminUsageRepository) usageBuckets(database *gorm.DB, filter domain.AdminUsageFilter) *gorm.DB {
	if repository.materializedOnly {
		return database.Table("(?) AS combined", storedUsageBuckets(database, filter)).Select("combined.*")
	}
	return rollupUsageBuckets(database, filter)
}
func rollupUsageBuckets(database *gorm.DB, filter domain.AdminUsageFilter) *gorm.DB {
	stored := storedUsageBuckets(database, filter)
	pendingSource := adminUsageQuery(database, filter).Table("usage_logs_pending AS usage_logs")
	pending := organizationBuckets(database, pendingSource, domain.AdminOrganizationUsageFilter{AdminUsageFilter: filter})
	return database.Table("(? UNION ALL ?) AS combined", stored, pending).
		Select("period, organization_id, api_key_id, model, SUM(requests) AS requests, SUM(errors) AS errors, SUM(input_tokens) AS input_tokens, SUM(cache_read_tokens) AS cache_read_tokens, SUM(cache_write_tokens) AS cache_write_tokens, SUM(cache_read_cost_amount) AS cache_read_cost_amount, SUM(cache_write_cost_amount) AS cache_write_cost_amount, SUM(output_tokens) AS output_tokens, SUM(total_tokens) AS total_tokens, SUM(cost_amount) AS cost_amount").
		Group("period, organization_id, api_key_id, model")
}

func (repository *AdminUsageRepository) aggregateUsageRollups(ctx context.Context, filter domain.AdminUsageFilter, period string) ([]domain.AdminUsagePoint, error) {
	grouping := map[string]string{
		"total": "'total'", "daily": "usage_buckets.period",
		"weekly":  "DATE_FORMAT(DATE_SUB(DATE(usage_buckets.period), INTERVAL WEEKDAY(usage_buckets.period) DAY), '%Y-%m-%d')",
		"monthly": "DATE_FORMAT(usage_buckets.period, '%Y-%m')",
	}[period]
	database := repository.database.WithContext(ctx)
	query := database.Table("(?) AS usage_buckets", repository.usageBuckets(database, filter)).
		Select(grouping + " AS period, COALESCE(SUM(requests), 0) AS requests, COALESCE(SUM(errors), 0) AS errors, COALESCE(SUM(input_tokens), 0) AS input_tokens, COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens, COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens, COALESCE(SUM(cache_read_cost_amount), 0) AS cache_read_cost_amount, COALESCE(SUM(cache_write_cost_amount), 0) AS cache_write_cost_amount, COALESCE(SUM(output_tokens), 0) AS output_tokens, COALESCE(SUM(total_tokens), 0) AS total_tokens, COALESCE(SUM(cost_amount), 0) AS cost_amount")
	if period != "total" {
		query = query.Group(grouping).Order("period ASC").Limit(1000)
	}
	items := make([]domain.AdminUsagePoint, 0)
	err := query.Scan(&items).Error
	return items, err
}
