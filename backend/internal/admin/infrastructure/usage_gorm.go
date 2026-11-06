package infrastructure

import (
	"context"
	"net"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/geoip"
	"bosapi/backend/internal/platform/usagetime"
	"gorm.io/gorm"
)

type adminUsageRecord struct {
	ID                   uint64
	RequestID            string
	OrganizationID       uint64
	OrganizationName     string
	APIKeyID             uint64
	APIKeyName           string
	Model                string
	Endpoint             string
	StatusCode           uint16
	ErrorCode            string `gorm:"column:error_code"`
	InputTokens          uint64
	CacheReadTokens      uint64
	CacheWriteTokens     uint64
	CacheReadCostAmount  float64
	CacheWriteCostAmount float64
	OutputTokens         uint64
	TotalTokens          uint64
	CostAmount           float64
	DurationMillis       uint32  `gorm:"column:duration_ms"`
	FirstTokenMillis     *uint32 `gorm:"column:first_token_ms"`
	CreatedAt            time.Time
	ClientIP             []byte `gorm:"column:client_ip"`
}

func (adminUsageRecord) TableName() string { return "usage_logs" }

// AdminUsageRepository reads usage records and aggregates through Gorm.
type AdminUsageRepository struct {
	database         *gorm.DB
	geoip            *geoip.Resolver
	dailyRollups     bool
	materializedOnly bool
}

// NewAdminUsageRepository constructs a Gorm-backed administrative usage repository.
func NewAdminUsageRepository(database *gorm.DB, resolver ...*geoip.Resolver) *AdminUsageRepository {
	var lookup *geoip.Resolver
	if len(resolver) > 0 {
		lookup = resolver[0]
	}
	return &AdminUsageRepository{database: database, geoip: lookup}
}

// ListAdminUsage returns a bounded page of body-free usage records.
func (repository *AdminUsageRepository) ListAdminUsage(ctx context.Context, filter domain.AdminUsageFilter, page, size int) ([]domain.AdminUsageRecord, int64, error) {
	query := adminUsageQuery(repository.database.WithContext(ctx), filter)
	var total int64
	if !filter.SkipTotal {
		if err := query.Count(&total).Error; err != nil {
			return nil, 0, err
		}
	}
	var records []adminUsageRecord
	offset := (page - 1) * size
	if filter.SkipTotal {
		total = -1
		query = adminUsagePageQuery(repository.database.WithContext(ctx), filter, (page-1)*size, size+1)
		size++
	}
	if err := query.
		Select("usage_logs.id, usage_logs.request_id, usage_logs.organization_id, COALESCE(org.name, '') AS organization_name, usage_logs.api_key_id, COALESCE(api_key.name, '') AS api_key_name, usage_logs.model, usage_logs.endpoint, usage_logs.status_code, usage_logs.input_tokens, usage_logs.cache_read_tokens, usage_logs.cache_write_tokens, usage_logs.cache_read_cost_amount, usage_logs.cache_write_cost_amount, usage_logs.output_tokens, usage_logs.total_tokens, usage_logs.cost_amount, usage_logs.duration_ms, usage_logs.first_token_ms, usage_logs.created_at, usage_logs.client_ip").
		Joins("LEFT JOIN organizations AS org ON org.id = usage_logs.organization_id").
		Joins("LEFT JOIN api_keys AS api_key ON api_key.id = usage_logs.api_key_id").
		Order("usage_logs.created_at DESC, usage_logs.id DESC").Offset(offset).Limit(size).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	items := make([]domain.AdminUsageRecord, 0, len(records))
	for _, record := range records {
		clientIP := formatClientIP(net.IP(record.ClientIP).String(), repository.geoip)
		items = append(items, domain.AdminUsageRecord{ID: record.ID, RequestID: record.RequestID, OrganizationID: record.OrganizationID, OrganizationName: record.OrganizationName, APIKeyID: record.APIKeyID, APIKeyName: record.APIKeyName, Model: record.Model, Endpoint: record.Endpoint, StatusCode: record.StatusCode, InputTokens: record.InputTokens, CacheReadTokens: record.CacheReadTokens, CacheWriteTokens: record.CacheWriteTokens, CacheReadCostAmount: record.CacheReadCostAmount, CacheWriteCostAmount: record.CacheWriteCostAmount, OutputTokens: record.OutputTokens, TotalTokens: record.TotalTokens, CostAmount: record.CostAmount, FirstTokenMillis: record.FirstTokenMillis, CreatedAt: record.CreatedAt, ClientIP: clientIP})
	}
	return items, total, nil
}

func (repository *AdminUsageRepository) GroupAdminUsageByIP(ctx context.Context, filter domain.AdminUsageFilter, page, size int) ([]domain.AdminUsageIP, int64, error) {
	return groupAdminUsageByIP(repository.database.WithContext(ctx), filter, page, size)
}

// AggregateAdminUsage groups requests using a fixed, allowlisted SQL expression.
func (repository *AdminUsageRepository) AggregateAdminUsage(ctx context.Context, filter domain.AdminUsageFilter, period string) ([]domain.AdminUsagePoint, error) {
	if repository.dailyRollups && supportsUsageRollups(filter) {
		return repository.aggregateUsageRollups(ctx, filter, period)
	}
	localTime := usagetime.SQLTimestamp
	grouping := map[string]string{
		"total":   "'total'",
		"daily":   "DATE_FORMAT(" + localTime + ", '%Y-%m-%d')",
		"weekly":  "DATE_FORMAT(DATE_SUB(DATE(" + localTime + "), INTERVAL WEEKDAY(" + localTime + ") DAY), '%Y-%m-%d')",
		"monthly": "DATE_FORMAT(" + localTime + ", '%Y-%m')",
	}[period]
	var rows []struct {
		Period               string
		Requests             uint64
		Errors               uint64
		InputTokens          uint64
		CacheReadTokens      uint64
		CacheWriteTokens     uint64
		CacheReadCostAmount  float64
		CacheWriteCostAmount float64
		OutputTokens         uint64
		TotalTokens          uint64
		CostAmount           float64
	}
	query := adminUsageQuery(repository.database.WithContext(ctx), filter).Select(grouping + " AS period, COUNT(*) AS requests, SUM(status_code >= 400) AS errors, COALESCE(SUM(input_tokens), 0) AS input_tokens, COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens, COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens, COALESCE(SUM(cache_read_cost_amount), 0) AS cache_read_cost_amount, COALESCE(SUM(cache_write_cost_amount), 0) AS cache_write_cost_amount, COALESCE(SUM(output_tokens), 0) AS output_tokens, COALESCE(SUM(total_tokens), 0) AS total_tokens, COALESCE(SUM(cost_amount), 0) AS cost_amount")
	if period != "total" {
		query = query.Group(grouping).Order("period ASC").Limit(1000)
	}
	err := query.Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	items := make([]domain.AdminUsagePoint, 0, len(rows))
	for _, row := range rows {
		items = append(items, domain.AdminUsagePoint{Period: row.Period, Requests: row.Requests, Errors: row.Errors, InputTokens: row.InputTokens, CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens, CacheReadCostAmount: row.CacheReadCostAmount, CacheWriteCostAmount: row.CacheWriteCostAmount, OutputTokens: row.OutputTokens, TotalTokens: row.TotalTokens, CostAmount: row.CostAmount})
	}
	return items, nil
}

func adminUsageQuery(database *gorm.DB, filter domain.AdminUsageFilter) *gorm.DB {
	query := database.Model(&adminUsageRecord{})
	if database.Statement.TableExpr == nil {
		query = query.Table("usage_logs_read AS usage_logs")
	}
	return applyAdminUsageFilter(query, filter)
}

func applyAdminUsageFilter(query *gorm.DB, filter domain.AdminUsageFilter) *gorm.DB {
	if filter.OrganizationID > 0 {
		query = query.Where("usage_logs.organization_id = ?", filter.OrganizationID)
	}
	if filter.APIKeyID > 0 {
		query = query.Where("usage_logs.api_key_id = ?", filter.APIKeyID)
	}
	if filter.ClientIP != "" {
		query = query.Where("usage_logs.client_ip = INET6_ATON(?)", filter.ClientIP)
	}
	if filter.Model != "" {
		query = query.Where("usage_logs.model = ?", filter.Model)
	}
	if filter.From != nil {
		query = query.Where("usage_logs.created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		query = query.Where("usage_logs.created_at < ?", *filter.To)
	}
	return query
}

var _ domain.AdminUsageRepository = (*AdminUsageRepository)(nil)
