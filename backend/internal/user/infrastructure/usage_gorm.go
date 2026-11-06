package infrastructure

import (
	"context"
	"strings"
	"time"

	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/internal/user/domain"
	"gorm.io/gorm"
)

// UsageRecord maps safe gateway request metadata to its dedicated table.
type UsageRecord struct {
	ID                   uint64  `gorm:"primaryKey"`
	RequestID            string  `gorm:"size:128;not null;uniqueIndex:uq_usage_request_usage,priority:1"`
	UsageID              string  `gorm:"size:64;not null;uniqueIndex:uq_usage_request_usage,priority:2"`
	UserID               *uint64 `gorm:"index"`
	OrganizationID       uint64  `gorm:"index;not null"`
	APIKeyID             uint64  `gorm:"index;not null"`
	Model                string  `gorm:"size:191;not null"`
	UpstreamModel        *string `gorm:"size:191"`
	Endpoint             string  `gorm:"size:128;not null"`
	StatusCode           uint16  `gorm:"not null"`
	ErrorCode            string  `gorm:"size:64;not null;default:''"`
	InputTokens          uint64  `gorm:"not null"`
	CacheReadTokens      uint64  `gorm:"column:cache_read_tokens;not null"`
	CacheWriteTokens     uint64  `gorm:"column:cache_write_tokens;not null"`
	CacheReadCostAmount  float64 `gorm:"column:cache_read_cost_amount;type:decimal(20,8);not null"`
	CacheWriteCostAmount float64 `gorm:"column:cache_write_cost_amount;type:decimal(20,8);not null"`
	OutputTokens         uint64  `gorm:"not null"`
	TotalTokens          uint64  `gorm:"not null"`
	CostAmount           float64 `gorm:"type:decimal(20,8);not null"`
	ClientIP             []byte  `gorm:"type:varbinary(16)"`
	IsStream             bool
	DurationMillis       uint32    `gorm:"column:duration_ms"`
	FirstTokenMillis     *uint32   `gorm:"column:first_token_ms"`
	CreatedAt            time.Time `gorm:"index;not null"`
}

// GatewayUsageRecord contains usage metadata collected by the gateway adapter.
type GatewayUsageRecord struct {
	RequestID            string
	UsageID              string
	OrganizationID       uint64
	APIKeyID             uint64
	Model                string
	Endpoint             string
	StatusCode           uint16
	ErrorCode            string
	InputTokens          uint64
	CacheReadTokens      uint64
	CacheWriteTokens     uint64
	CacheReadCostAmount  float64
	CacheWriteCostAmount float64
	OutputTokens         uint64
	TotalTokens          uint64
	CostAmount           float64
	ClientIP             []byte
	IsStream             bool
	DurationMillis       uint32
	FirstTokenMillis     *uint32
	CreatedAt            time.Time
}

func (UsageRecord) TableName() string { return "usage_logs" }

// RecordGatewayUsage persists one gateway request for admin and owner-scoped usage views.
func (repository *KeyRepository) RecordGatewayUsage(ctx context.Context, value GatewayUsageRecord) error {
	var key APIKeyRecord
	if err := repository.database.WithContext(ctx).Select("id", "user_id", "organization_id").Where("id = ? AND organization_id = ?", value.APIKeyID, value.OrganizationID).First(&key).Error; err != nil {
		return err
	}
	record := UsageRecord{
		RequestID: value.RequestID, UsageID: value.UsageID, OrganizationID: value.OrganizationID, APIKeyID: value.APIKeyID,
		Model: value.Model, Endpoint: value.Endpoint, StatusCode: value.StatusCode, ErrorCode: value.ErrorCode,
		InputTokens: value.InputTokens, CacheReadTokens: value.CacheReadTokens, CacheWriteTokens: value.CacheWriteTokens,
		CacheReadCostAmount: value.CacheReadCostAmount, CacheWriteCostAmount: value.CacheWriteCostAmount,
		OutputTokens: value.OutputTokens, TotalTokens: value.TotalTokens,
		CostAmount: value.CostAmount, ClientIP: value.ClientIP, IsStream: value.IsStream, DurationMillis: value.DurationMillis, FirstTokenMillis: value.FirstTokenMillis, CreatedAt: value.CreatedAt,
	}
	return repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := insertUsageRecord(tx, &record)
		return err
	})
}

// ListUsage returns usage rows constrained to the authenticated identity.
func (repository *KeyRepository) ListUsage(ctx context.Context, filter domain.UsageFilter, page, pageSize int) ([]domain.UsageRecord, int64, error) {
	query := usageQuery(repository.database.WithContext(ctx), filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []UsageRecord
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	items := make([]domain.UsageRecord, 0, len(records))
	for _, record := range records {
		items = append(items, usageDomain(record))
	}
	return items, total, nil
}

// FindUsage returns one usage row only when its key belongs to the authenticated identity.
func (repository *KeyRepository) FindUsage(ctx context.Context, filter domain.UsageFilter, id uint64) (domain.UsageRecord, error) {
	var record UsageRecord
	if err := usageQuery(repository.database.WithContext(ctx), filter).Where("id = ?", id).First(&record).Error; err != nil {
		return domain.UsageRecord{}, err
	}
	return usageDomain(record), nil
}

// SummarizeUsage aggregates safe usage counters for the authenticated identity.
func (repository *KeyRepository) SummarizeUsage(ctx context.Context, filter domain.UsageFilter) (domain.UsageTotals, error) {
	var result struct {
		Requests     uint64
		Errors       uint64
		InputTokens  uint64
		OutputTokens uint64
		TotalTokens  uint64
		CostAmount   float64
	}
	err := usageQuery(repository.database.WithContext(ctx), filter).Select("COUNT(*) AS requests, COALESCE(SUM(status_code >= 400), 0) AS errors, COALESCE(SUM(input_tokens), 0) AS input_tokens, COALESCE(SUM(output_tokens), 0) AS output_tokens, COALESCE(SUM(total_tokens), 0) AS total_tokens, COALESCE(SUM(cost_amount), 0) AS cost_amount").Scan(&result).Error
	return domain.UsageTotals{Requests: result.Requests, Errors: result.Errors, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, TotalTokens: result.TotalTokens, CostAmount: result.CostAmount, Incomplete: false}, err
}

// TrendUsage aggregates request metadata into daily user-visible counters.
func (repository *KeyRepository) TrendUsage(ctx context.Context, filter domain.UsageFilter) ([]domain.UsageTrendPoint, error) {
	var rows []struct {
		InputTokens, OutputTokens uint64
		Date                      string
		Requests                  uint64
		Errors                    uint64
		TotalTokens               uint64
		CostAmount                float64
	}
	day := "DATE_FORMAT(" + usagetime.SQLTimestamp + ", '%Y-%m-%d')"
	err := usageQuery(repository.database.WithContext(ctx), filter).Select(day + " AS date, COUNT(*) AS requests, SUM(status_code >= 400) AS errors, SUM(input_tokens) AS input_tokens, SUM(output_tokens) AS output_tokens, SUM(total_tokens) AS total_tokens, SUM(cost_amount) AS cost_amount").Group(day).Order("date ASC").Limit(366).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	points := make([]domain.UsageTrendPoint, 0, len(rows))
	for _, row := range rows {
		points = append(points, domain.UsageTrendPoint{Date: row.Date, Requests: row.Requests, Errors: row.Errors, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, TotalTokens: row.TotalTokens, CostAmount: row.CostAmount})
	}
	return points, nil
}

// UsageByModel returns a bounded model usage breakdown for a user.
func (repository *KeyRepository) UsageByModel(ctx context.Context, filter domain.UsageFilter, limit int) ([]domain.UsageModelTotal, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	var rows []struct {
		InputTokens, OutputTokens uint64
		Model                     string
		Requests                  uint64
		TotalTokens               uint64
		CostAmount                float64
	}
	err := usageQuery(repository.database.WithContext(ctx), filter).Select("model, COUNT(*) AS requests, SUM(input_tokens) AS input_tokens, SUM(output_tokens) AS output_tokens, SUM(total_tokens) AS total_tokens, SUM(cost_amount) AS cost_amount").Group("model").Order("requests DESC").Limit(limit).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	items := make([]domain.UsageModelTotal, 0, len(rows))
	for _, row := range rows {
		items = append(items, domain.UsageModelTotal{Model: row.Model, Requests: row.Requests, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, TotalTokens: row.TotalTokens, CostAmount: row.CostAmount})
	}
	return items, nil
}

// DailyUsageByKey aggregates daily counters after constraining the key to its owner.
func (repository *KeyRepository) DailyUsageByKey(ctx context.Context, filter domain.UsageFilter, days int) ([]domain.UsageDailyPoint, error) {
	// Include today and the preceding days-1 complete Shanghai calendar days.
	start := usagetime.DayStart(time.Now()).AddDate(0, 0, 1-days)
	filter.From = &start
	type row struct {
		Date                          string
		InputTokens, OutputTokens     uint64
		Requests, Errors, TotalTokens uint64
		CostAmount                    float64
	}
	var rows []row
	day := "DATE_FORMAT(" + usagetime.SQLTimestamp + ", '%Y-%m-%d')"
	err := usageQuery(repository.database.WithContext(ctx), filter).
		Select(day + " AS date, COUNT(*) AS requests, SUM(status_code >= 400) AS errors, SUM(input_tokens) AS input_tokens, SUM(output_tokens) AS output_tokens, SUM(total_tokens) AS total_tokens, SUM(cost_amount) AS cost_amount").
		Group(day).Order("date ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	items := make([]domain.UsageDailyPoint, 0, len(rows))
	for _, value := range rows {
		items = append(items, domain.UsageDailyPoint{Date: value.Date, Requests: value.Requests, Errors: value.Errors, InputTokens: value.InputTokens, OutputTokens: value.OutputTokens, TotalTokens: value.TotalTokens, CostAmount: value.CostAmount})
	}
	return items, nil
}

// OwnsAPIKey verifies a non-deleted key belongs to the authenticated organization.
func (repository *KeyRepository) OwnsAPIKey(ctx context.Context, id, organizationID uint64) (bool, error) {
	if id == 0 || organizationID == 0 {
		return false, nil
	}
	var count int64
	err := repository.database.WithContext(ctx).Model(&APIKeyRecord{}).Where("id = ? AND organization_id = ? AND status <> ?", id, organizationID, "deleted").Count(&count).Error
	return count == 1, err
}

// OwnsAPIKeys verifies every requested key is non-deleted and belongs to the authenticated organization.
func (repository *KeyRepository) OwnsAPIKeys(ctx context.Context, ids []uint64, organizationID uint64) (bool, error) {
	if len(ids) == 0 || organizationID == 0 {
		return false, nil
	}
	var count int64
	err := repository.database.WithContext(ctx).Model(&APIKeyRecord{}).Where("id IN ? AND organization_id = ? AND status <> ?", ids, organizationID, "deleted").Count(&count).Error
	return count == int64(len(ids)), err
}

// UsageByKeys aggregates daily usage for an already organization-validated key set.
func (repository *KeyRepository) UsageByKeys(ctx context.Context, filter domain.UsageFilter, ids []uint64) ([]domain.APIKeyUsagePoint, error) {
	type row struct {
		APIKeyID                      uint64
		InputTokens, OutputTokens     uint64
		Date                          string
		Requests, Errors, TotalTokens uint64
		CostAmount                    float64
	}
	var rows []row
	day := "DATE_FORMAT(" + usagetime.SQLTimestamp + ", '%Y-%m-%d')"
	err := repository.database.WithContext(ctx).Model(&UsageRecord{}).Table("usage_logs_read AS usage_logs").
		Where("organization_id = ? AND organization_id > 0 AND api_key_id IN ?", filter.OrganizationID, ids).
		Scopes(func(database *gorm.DB) *gorm.DB {
			if filter.From != nil {
				database = database.Where("created_at >= ?", *filter.From)
			}
			if filter.To != nil {
				database = database.Where("created_at < ?", *filter.To)
			}
			return database
		}).
		Select("api_key_id, " + day + " AS date, COUNT(*) AS requests, SUM(status_code >= 400) AS errors, SUM(input_tokens) AS input_tokens, SUM(output_tokens) AS output_tokens, SUM(total_tokens) AS total_tokens, SUM(cost_amount) AS cost_amount").
		Group("api_key_id, " + day).Order("date ASC, api_key_id ASC").Limit(10000).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	items := make([]domain.APIKeyUsagePoint, 0, len(rows))
	for _, value := range rows {
		items = append(items, domain.APIKeyUsagePoint{APIKeyID: value.APIKeyID, Date: value.Date, Requests: value.Requests, Errors: value.Errors, InputTokens: value.InputTokens, OutputTokens: value.OutputTokens, TotalTokens: value.TotalTokens, CostAmount: value.CostAmount})
	}
	return items, nil
}

// ListUsageErrors returns owner-scoped failed-request metadata with stable error codes.
func (repository *KeyRepository) ListUsageErrors(ctx context.Context, filter domain.UsageFilter, code string, page, size int) ([]domain.UsageError, int64, error) {
	query := usageQuery(repository.database.WithContext(ctx), filter).Where("status_code >= ?", 400)
	if code != "" {
		query = query.Where("error_code = ? OR (error_code = '' AND CASE WHEN status_code = 429 THEN 'rate_limited' WHEN status_code >= 500 THEN 'upstream_error' ELSE 'invalid_request' END = ?)", code, code)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []UsageRecord
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	items := make([]domain.UsageError, 0, len(records))
	for _, record := range records {
		items = append(items, usageErrorDomain(record))
	}
	return items, total, nil
}

// FindUsageError returns one failed request only within the authenticated owner's scope.
func (repository *KeyRepository) FindUsageError(ctx context.Context, filter domain.UsageFilter, id uint64) (domain.UsageError, error) {
	var record UsageRecord
	if err := usageQuery(repository.database.WithContext(ctx), filter).Where("id = ? AND status_code >= ?", id, 400).First(&record).Error; err != nil {
		return domain.UsageError{}, err
	}
	return usageErrorDomain(record), nil
}

func usageErrorDomain(record UsageRecord) domain.UsageError {
	code := record.ErrorCode
	if code == "" && record.StatusCode == 429 {
		code = string(errorcode.RateLimited)
	} else if code == "" && record.StatusCode >= 500 {
		code = string(errorcode.UpstreamError)
	} else if code == "" {
		code = string(errorcode.InvalidRequest)
	}
	return domain.UsageError{ID: record.ID, Time: record.CreatedAt, RequestID: record.RequestID, APIKeyID: record.APIKeyID, Model: record.Model, HTTPStatus: record.StatusCode, Outcome: "failed", ErrorCode: code, DurationMillis: record.DurationMillis}
}

func usageQuery(database *gorm.DB, filter domain.UsageFilter) *gorm.DB {
	query := database.Model(&UsageRecord{}).Table("usage_logs_read AS usage_logs").Where("organization_id = ? AND organization_id > 0", filter.OrganizationID)
	if filter.APIKeyID > 0 {
		query = query.Where("api_key_id = ?", filter.APIKeyID)
	}
	if model := strings.TrimSpace(filter.Model); model != "" {
		query = query.Where("model = ?", model)
	}
	if filter.From != nil {
		query = query.Where("created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		query = query.Where("created_at < ?", *filter.To)
	}
	return query
}

func usageDomain(record UsageRecord) domain.UsageRecord {
	return domain.UsageRecord{ID: record.ID, RequestID: record.RequestID, APIKeyID: record.APIKeyID, Model: record.Model, UpstreamModel: record.UpstreamModel, Endpoint: record.Endpoint, StatusCode: record.StatusCode, InputTokens: record.InputTokens, CacheReadTokens: record.CacheReadTokens, CacheWriteTokens: record.CacheWriteTokens, CacheReadCostAmount: record.CacheReadCostAmount, CacheWriteCostAmount: record.CacheWriteCostAmount, OutputTokens: record.OutputTokens, TotalTokens: record.TotalTokens, CostAmount: record.CostAmount, IsStream: record.IsStream, DurationMillis: record.DurationMillis, FirstTokenMillis: record.FirstTokenMillis, CreatedAt: record.CreatedAt}
}
