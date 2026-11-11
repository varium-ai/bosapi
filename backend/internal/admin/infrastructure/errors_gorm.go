package infrastructure

import (
	"context"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"gorm.io/gorm"
)

// AdminErrorRepository reads failed request metadata from the usage ledger.
type AdminErrorRepository struct{ database *gorm.DB }

// NewAdminErrorRepository constructs the administrative error repository.
func NewAdminErrorRepository(database *gorm.DB) *AdminErrorRepository {
	return &AdminErrorRepository{database: database}
}

// ListAdminErrors returns a keyset-paginated page and whether more rows remain.
func (repository *AdminErrorRepository) ListAdminErrors(ctx context.Context, filter domain.AdminErrorFilter, limit int) ([]domain.AdminError, bool, error) {
	query := adminErrorPageQuery(repository.database.WithContext(ctx), filter, limit)
	var records []adminUsageRecord
	if err := query.Find(&records).Error; err != nil {
		return nil, false, err
	}
	more := len(records) > limit
	if more {
		records = records[:limit]
	}
	items := make([]domain.AdminError, 0, len(records))
	for _, record := range records {
		code := record.ErrorCode
		if code == "" && record.StatusCode == 429 {
			code = string(errorcode.RateLimited)
		} else if code == "" && record.StatusCode >= 500 {
			code = string(errorcode.UpstreamError)
		} else if code == "" {
			code = string(errorcode.InvalidRequest)
		}
		items = append(items, domain.AdminError{ID: record.ID, Time: record.CreatedAt, RequestID: record.RequestID, OrganizationID: record.OrganizationID, APIKeyID: record.APIKeyID, HTTPStatus: record.StatusCode, Outcome: "failed", ErrorCode: code, Model: record.Model, Endpoint: record.Endpoint, DurationMillis: record.DurationMillis})
	}
	return items, more, nil
}

// Apply every predicate and LIMIT inside each physical branch. Limiting only the
// UNION view forces MySQL to materialize and sort the entire matching history.
// The mover preserves IDs and moves rows atomically, so UNION ALL is safe here.
func adminErrorBranch(database *gorm.DB, table string, filter domain.AdminErrorFilter, limit int) *gorm.DB {
	query := database.Table(table+" AS usage_logs").Where("status_code >= ?", 400)
	if !filter.From.IsZero() {
		query = query.Where("created_at >= ? AND created_at < ?", filter.From, filter.To)
	}
	if filter.OrganizationID > 0 {
		query = query.Where("organization_id = ?", filter.OrganizationID)
	}
	if filter.APIKeyID > 0 {
		query = query.Where("api_key_id = ?", filter.APIKeyID)
	}
	if filter.Model != "" {
		query = query.Where("model = ?", filter.Model)
	}
	if filter.ErrorCode != "" {
		legacyStatus := "CASE WHEN status_code = 429 THEN 'rate_limited' WHEN status_code >= 500 THEN 'upstream_error' ELSE 'invalid_request' END"
		query = query.Where("error_code = ? OR (error_code = '' AND "+legacyStatus+" = ?)", filter.ErrorCode, filter.ErrorCode)
	}
	query = query.Where("error_code NOT IN ?", []string{
		string(errorcode.SecurityBlocked), string(errorcode.SecurityInspectionUnavailable),
		string(errorcode.ToolCallInspectionUnavailable), string(errorcode.ToolCallInvalid), string(errorcode.UnauthorizedTool),
	})
	if filter.BeforeID > 0 {
		query = query.Where("created_at < ? OR (created_at = ? AND id < ?)", filter.Before, filter.Before, filter.BeforeID)
	}
	return query.Select("id, request_id, organization_id, api_key_id, status_code, error_code, model, endpoint, duration_ms, created_at").
		Order("created_at DESC, id DESC").Limit(limit)
}

func adminErrorPageQuery(database *gorm.DB, filter domain.AdminErrorFilter, limit int) *gorm.DB {
	return database.Table("((?) UNION ALL (?)) AS usage_errors",
		adminErrorBranch(database, "usage_logs_v2", filter, limit+1),
		adminErrorBranch(database, "usage_logs", filter, limit+1)).
		Order("created_at DESC, id DESC").Limit(limit + 1)
}

var _ domain.AdminErrorRepository = (*AdminErrorRepository)(nil)
