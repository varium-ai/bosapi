package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
)

// ErrInvalidRequestLogFilter identifies malformed request log queries.
var ErrInvalidRequestLogFilter = errors.New("invalid request log filter")

// RequestLogService provides bounded request-chain lookups for administrators.
type RequestLogService struct{ repository domain.RequestLogRepository }

// NewRequestLogService constructs the request log query service.
func NewRequestLogService(repository domain.RequestLogRepository) *RequestLogService {
	return &RequestLogService{repository: repository}
}

// List returns one bounded page of entries for a validated request ID.
func (service *RequestLogService) List(ctx context.Context, filter domain.RequestLogFilter, offset, limit int) ([]domain.RequestLog, bool, error) {
	filter.RequestID = strings.TrimSpace(filter.RequestID)
	if !httpapi.ValidRequestID(filter.RequestID) || offset < 0 || offset > 1_000_000 || limit < 1 || limit > 500 {
		return nil, false, ErrInvalidRequestLogFilter
	}
	items, more, err := service.repository.ListRequestLogs(ctx, filter, offset, limit)
	if err != nil {
		return nil, false, fmt.Errorf("list request logs: %w", err)
	}
	return items, more, nil
}
