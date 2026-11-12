package infrastructure

import (
	"context"
	"time"

	"gorm.io/gorm"

	"bosapi/backend/internal/admin/domain"
)

// DailyAdminOrganizationUsage preserves attribution from the usage log even after key deletion.
func (repository *AdminUsageRepository) DailyAdminOrganizationUsage(ctx context.Context, filter domain.AdminOrganizationUsageFilter, page, size int) (domain.AdminOrganizationUsageDashboard, error) {
	result := domain.AdminOrganizationUsageDashboard{
		Items: make([]domain.AdminOrganizationUsagePoint, 0), TopOrganizations: make([]domain.AdminOrganizationTokenRank, 0),
		TopKeys: make([]domain.AdminKeyTokenRank, 0), TopModels: make([]domain.AdminModelTokenRank, 0),
	}
	var rows []adminOrganizationDashboardRow
	database := repository.database.WithContext(ctx)
	if err := database.Raw(adminOrganizationDashboardSQL, repository.organizationUsageBuckets(database, filter),
		adminDashboardKeyItems(database, filter.OrganizationID), size, (page-1)*size).Scan(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		switch row.RowType {
		case "summary":
			result.Total = row.Total
			result.Summary = row.AdminUsagePoint
		case "item":
			result.Items = append(result.Items, row.AdminOrganizationUsagePoint)
		case "organization":
			result.TopOrganizations = append(result.TopOrganizations, domain.AdminOrganizationTokenRank{
				OrganizationID: row.OrganizationID, OrganizationName: row.OrganizationName,
				InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, TotalTokens: row.TotalTokens,
			})
		case "key":
			result.TopKeys = append(result.TopKeys, domain.AdminKeyTokenRank{
				APIKeyID: row.APIKeyID, APIKeyName: row.APIKeyName,
				OrganizationName: row.OrganizationName, OrganizationCount: row.OrganizationCount,
				InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, TotalTokens: row.TotalTokens,
			})
		case "model":
			result.TopModels = append(result.TopModels, domain.AdminModelTokenRank{
				Model: row.Model, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, TotalTokens: row.TotalTokens,
			})
		}
	}
	return result, nil
}

type adminOrganizationDashboardRow struct {
	domain.AdminOrganizationUsagePoint
	RowType           string
	Total             int64
	APIKeyID          uint64
	APIKeyName        string
	OrganizationCount uint64
	Model             string
}

// Aggregated CTEs materialize once in MySQL. Keeping every dashboard section in
// this statement avoids rescanning the request logs for each ranking and keeps
// all counters on the same snapshot. Raw SQL is needed to share these CTEs;
// filters and pagination are bound through GORM subqueries and placeholders.
const adminOrganizationUsageCTEs = `WITH usage_buckets AS (?),
	user_days AS (
		SELECT period, buckets.organization_id, COALESCE(org.name, '') AS organization_name,
			COUNT(DISTINCT api_key_id) AS key_count, SUM(requests) AS requests, SUM(errors) AS errors,
			SUM(cache_read_tokens) AS cache_read_tokens,
			SUM(cache_write_tokens) AS cache_write_tokens,
			SUM(cache_read_cost_amount) AS cache_read_cost_amount,
			SUM(cache_write_cost_amount) AS cache_write_cost_amount,
			SUM(input_tokens) AS input_tokens, SUM(output_tokens) AS output_tokens,
			SUM(total_tokens) AS total_tokens, SUM(cost_amount) AS cost_amount
		FROM usage_buckets AS buckets LEFT JOIN organizations AS org ON org.id = buckets.organization_id
		GROUP BY period, buckets.organization_id, org.name
	)`

const adminOrganizationDashboardSQL = adminOrganizationUsageCTEs + `, key_ranks AS (
		SELECT api_key_id, MIN(organization_id) AS organization_id, COUNT(DISTINCT organization_id) AS organization_count,
			SUM(input_tokens) AS input_tokens, SUM(output_tokens) AS output_tokens, SUM(total_tokens) AS total_tokens
		FROM usage_buckets GROUP BY api_key_id HAVING SUM(total_tokens) > 0
		ORDER BY total_tokens DESC, api_key_id ASC LIMIT 20
	), key_items AS (?), model_ranks AS (
		SELECT model, SUM(input_tokens) AS input_tokens, SUM(output_tokens) AS output_tokens,
			SUM(total_tokens) AS total_tokens
		FROM usage_buckets GROUP BY model HAVING SUM(total_tokens) > 0
		ORDER BY total_tokens DESC, model ASC LIMIT 20
	), page_items AS (
		SELECT * FROM user_days
		ORDER BY period DESC, total_tokens DESC, organization_id ASC LIMIT ? OFFSET ?
	), organization_ranks AS (
		SELECT organization_id, organization_name, SUM(input_tokens) AS input_tokens,
			SUM(output_tokens) AS output_tokens, SUM(total_tokens) AS total_tokens
		FROM user_days GROUP BY organization_id, organization_name HAVING SUM(total_tokens) > 0
		ORDER BY total_tokens DESC, organization_id ASC LIMIT 20
	)
	SELECT 'summary' AS row_type, '' AS period, 0 AS organization_id, '' AS organization_name,
		0 AS key_count, COUNT(*) AS total, COALESCE(SUM(requests), 0) AS requests,
		COALESCE(SUM(errors), 0) AS errors, COALESCE(SUM(input_tokens), 0) AS input_tokens,
		COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
		COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens,
		COALESCE(SUM(cache_read_cost_amount), 0) AS cache_read_cost_amount,
		COALESCE(SUM(cache_write_cost_amount), 0) AS cache_write_cost_amount,
		COALESCE(SUM(output_tokens), 0) AS output_tokens, COALESCE(SUM(total_tokens), 0) AS total_tokens,
		COALESCE(SUM(cost_amount), 0) AS cost_amount, 0 AS api_key_id, '' AS api_key_name,
		0 AS organization_count, '' AS model FROM user_days
	UNION ALL
	SELECT 'item', period, organization_id, organization_name, key_count, 0,
		requests, errors, input_tokens, cache_read_tokens, cache_write_tokens,
		cache_read_cost_amount, cache_write_cost_amount, output_tokens, total_tokens, cost_amount,
		0, '', 0, '' FROM page_items
	UNION ALL
	SELECT 'organization', '', organization_id, organization_name, 0, 0, 0, 0,
		input_tokens, 0, 0, 0, 0, output_tokens, total_tokens, 0, 0, '', 0, '' FROM organization_ranks
	UNION ALL
	SELECT 'key', '', 0, organization_name, 0, 0, 0, 0,
		input_tokens, 0, 0, 0, 0, output_tokens, total_tokens, 0, api_key_id, api_key_name, organization_count, '' FROM key_items
	UNION ALL
	SELECT 'model', '', 0, '', 0, 0, 0, 0,
		input_tokens, 0, 0, 0, 0, output_tokens, total_tokens, 0, 0, '', 0, model FROM model_ranks
	ORDER BY row_type ASC, period DESC, total_tokens DESC, organization_id ASC, api_key_id ASC, model ASC`

func dailyAdminOrganizationDashboardQuery(database *gorm.DB, filter domain.AdminOrganizationUsageFilter, page, size int) *gorm.DB {
	return database.Raw(adminOrganizationDashboardSQL, adminOrganizationUsageBuckets(database, filter),
		adminDashboardKeyItems(database, filter.OrganizationID), size, (page-1)*size)
}

// ListAdminOrganizationUsagePage omits totals and rankings already cached for
// this filter. Retain SQL aggregation so amounts keep their DECIMAL precision.
func (repository *AdminUsageRepository) ListAdminOrganizationUsagePage(ctx context.Context, filter domain.AdminOrganizationUsageFilter, page, size int) ([]domain.AdminOrganizationUsagePoint, error) {
	items := make([]domain.AdminOrganizationUsagePoint, 0)
	err := repository.database.WithContext(ctx).Raw(adminOrganizationUsageCTEs+`
		SELECT * FROM user_days
		ORDER BY period DESC, total_tokens DESC, organization_id ASC LIMIT ? OFFSET ?`,
		repository.organizationUsageBuckets(repository.database.WithContext(ctx), filter), size, (page-1)*size).Scan(&items).Error
	return items, err
}

func adminOrganizationUsageBuckets(database *gorm.DB, filter domain.AdminOrganizationUsageFilter) *gorm.DB {
	return organizationBuckets(database, adminDashboardUsageQuery(database, filter.AdminUsageFilter), filter)
}

func organizationBuckets(database, source *gorm.DB, filter domain.AdminOrganizationUsageFilter) *gorm.DB {
	query := source.Select("DATE_FORMAT(usage_logs.created_at, '%Y-%m-%d') AS period, usage_logs.organization_id, usage_logs.api_key_id, usage_logs.model, COUNT(*) AS requests, COALESCE(SUM(usage_logs.status_code >= 400), 0) AS errors, COALESCE(SUM(usage_logs.input_tokens), 0) AS input_tokens, COALESCE(SUM(usage_logs.cache_read_tokens), 0) AS cache_read_tokens, COALESCE(SUM(usage_logs.cache_write_tokens), 0) AS cache_write_tokens, COALESCE(SUM(usage_logs.cache_read_cost_amount), 0) AS cache_read_cost_amount, COALESCE(SUM(usage_logs.cache_write_cost_amount), 0) AS cache_write_cost_amount, COALESCE(SUM(usage_logs.output_tokens), 0) AS output_tokens, COALESCE(SUM(usage_logs.total_tokens), 0) AS total_tokens, COALESCE(SUM(usage_logs.cost_amount), 0) AS cost_amount").
		Group("period, usage_logs.organization_id, usage_logs.api_key_id, usage_logs.model")
	if filter.Search != "" {
		// Resolve matching organizations once, rather than joining their names to
		// every request. The explicit ID branch also retains deleted organizations.
		organizations := database.Table("organizations AS org").Select("org.id").Where("LOCATE(?, org.name) > 0", filter.Search)
		query = query.Where("usage_logs.organization_id IN (?) OR CAST(usage_logs.organization_id AS CHAR) = ?", organizations, filter.Search)
	}
	return query
}

// Resolve names only from small metadata tables and the filtered historical
// buckets. A moved or deleted Key must retain its recorded organization scope.
func adminDashboardKeyItems(database *gorm.DB, _ uint64) *gorm.DB {
	return database.Table("key_ranks AS ranked").
		Select("ranked.*, COALESCE(api_key.name, '') AS api_key_name, CASE WHEN ranked.organization_count = 1 THEN COALESCE(org.name, CONCAT('Organization #', ranked.organization_id)) ELSE '' END AS organization_name").
		Joins("LEFT JOIN api_keys AS api_key ON api_key.id = ranked.api_key_id").
		Joins("LEFT JOIN organizations AS org ON org.id = ranked.organization_id")
}

func adminDashboardUsageQuery(database *gorm.DB, filter domain.AdminUsageFilter) *gorm.DB {
	if filter.From == nil || filter.To == nil || !filter.From.Before(*filter.To) || filter.To.Sub(*filter.From) > 24*time.Hour {
		return adminUsageQuery(database, filter)
	}
	index := "idx_usage_logs_created_at"
	switch {
	case filter.APIKeyID > 0:
		index = "idx_usage_key_time"
	case filter.OrganizationID > 0:
		index = "idx_usage_org_time"
	case filter.Model != "":
		index = "idx_usage_model_time"
	}
	// Bind tenant/time predicates into each physical branch. This keeps the
	// bounded indexed plans even before the legacy table has finished draining.
	legacy := applyAdminUsageFilter(database.Table("usage_logs FORCE INDEX ("+index+")"), filter).
		Select("usage_logs.id, usage_logs.created_at, usage_logs.organization_id, usage_logs.api_key_id, usage_logs.model, usage_logs.status_code, usage_logs.input_tokens, usage_logs.cache_read_tokens, usage_logs.cache_write_tokens, usage_logs.cache_read_cost_amount, usage_logs.cache_write_cost_amount, usage_logs.output_tokens, usage_logs.total_tokens, usage_logs.cost_amount, usage_logs.client_ip")
	current := applyAdminUsageFilter(database.Table("usage_logs_v2 AS usage_logs FORCE INDEX ("+index+")"), filter).
		Select("usage_logs.id, usage_logs.created_at, usage_logs.organization_id, usage_logs.api_key_id, usage_logs.model, usage_logs.status_code, usage_logs.input_tokens, usage_logs.cache_read_tokens, usage_logs.cache_write_tokens, usage_logs.cache_read_cost_amount, usage_logs.cache_write_cost_amount, usage_logs.output_tokens, usage_logs.total_tokens, usage_logs.cost_amount, usage_logs.client_ip")
	return database.Table("(? UNION ALL ?) AS usage_logs", legacy, current)
}
