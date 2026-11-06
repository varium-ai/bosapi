package application

import (
	"context"
	"fmt"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
)

// AdminErrorService provides sanitized error dashboard queries.
type AdminErrorService struct{ repository domain.AdminErrorRepository }

// NewAdminErrorService constructs the error dashboard service.
func NewAdminErrorService(repository domain.AdminErrorRepository) *AdminErrorService {
	return &AdminErrorService{repository: repository}
}

// List returns a bounded page of failed request metadata.
func (service *AdminErrorService) List(ctx context.Context, filter domain.AdminErrorFilter, limit int) ([]domain.AdminError, bool, error) {
	if limit < 1 || limit > 100 || !validAdminErrorCode(filter.ErrorCode) {
		return nil, false, ErrInvalidAdminUsageFilter
	}
	if !filter.From.IsZero() || !filter.To.IsZero() {
		if filter.From.IsZero() || filter.To.IsZero() || !filter.From.Before(filter.To) || filter.To.Sub(filter.From) > 24*time.Hour {
			return nil, false, ErrInvalidAdminUsageFilter
		}
	}
	items, more, err := service.repository.ListAdminErrors(ctx, filter, limit)
	if err != nil {
		return nil, false, fmt.Errorf("list administrative errors: %w", err)
	}
	return items, more, nil
}

func validAdminErrorCode(code string) bool {
	if len(code) > 64 {
		return false
	}
	switch errorcode.Code(code) {
	case errorcode.SecurityBlocked, errorcode.SecurityInspectionUnavailable, errorcode.ToolCallInspectionUnavailable, errorcode.ToolCallInvalid, errorcode.UnauthorizedTool:
		return false
	default:
		return true
	}
}
