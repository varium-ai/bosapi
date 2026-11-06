package application

import (
	"context"
	"fmt"
	"time"

	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/internal/user/domain"
)

// UsageService coordinates ownership-scoped usage queries.
type UsageService struct{ repository domain.UsageRepository }

// NewUsageService constructs usage operations with an injected repository.
func NewUsageService(repository domain.UsageRepository) *UsageService {
	return &UsageService{repository: repository}
}

// List returns a bounded page of request metadata for one authenticated owner.
func (service *UsageService) List(ctx context.Context, filter domain.UsageFilter, page, pageSize int) ([]domain.UsageRecord, int64, error) {
	items, total, err := service.repository.ListUsage(ctx, filter, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list usage: %w", err)
	}
	return items, total, nil
}

// Find returns one usage row constrained by its owner and organization.
func (service *UsageService) Find(ctx context.Context, filter domain.UsageFilter, id uint64) (domain.UsageRecord, error) {
	item, err := service.repository.FindUsage(ctx, filter, id)
	if err != nil {
		return domain.UsageRecord{}, fmt.Errorf("find usage: %w", err)
	}
	return item, nil
}

// Summarize returns aggregate counters for a bounded date interval.
func (service *UsageService) Summarize(ctx context.Context, filter domain.UsageFilter) (domain.UsageTotals, error) {
	totals, err := service.repository.SummarizeUsage(ctx, filter)
	if err != nil {
		return domain.UsageTotals{}, fmt.Errorf("summarize usage: %w", err)
	}
	return totals, nil
}

// Trend returns day-bucketed counters for the authenticated user.
func (service *UsageService) Trend(ctx context.Context, filter domain.UsageFilter) ([]domain.UsageTrendPoint, error) {
	points, err := service.repository.TrendUsage(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("query usage trend: %w", err)
	}
	return points, nil
}

// ByModel returns bounded model aggregates for the authenticated user.
func (service *UsageService) ByModel(ctx context.Context, filter domain.UsageFilter, limit int) ([]domain.UsageModelTotal, error) {
	items, err := service.repository.UsageByModel(ctx, filter, limit)
	if err != nil {
		return nil, fmt.Errorf("query usage by model: %w", err)
	}
	return items, nil
}

// DailyByKey returns bounded daily usage counters for one owned API key.
func (service *UsageService) DailyByKey(ctx context.Context, filter domain.UsageFilter, days int) ([]domain.UsageDailyPoint, error) {
	items, err := service.repository.DailyUsageByKey(ctx, filter, days)
	if err != nil {
		return nil, fmt.Errorf("query daily key usage: %w", err)
	}
	return items, nil
}

// OwnsKey checks organization ownership before exposing key-specific usage.
func (service *UsageService) OwnsKey(ctx context.Context, id, organizationID uint64) (bool, error) {
	return service.repository.OwnsAPIKey(ctx, id, organizationID)
}

// ByKeys validates ownership and returns daily aggregates for every requested key.
func (service *UsageService) ByKeys(ctx context.Context, filter domain.UsageFilter, ids []uint64) ([]domain.APIKeyUsagePoint, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, domain.ErrInvalidUsageFilter
	}
	seen := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			return nil, domain.ErrInvalidUsageFilter
		}
		if _, exists := seen[id]; exists {
			return nil, domain.ErrInvalidUsageFilter
		}
		seen[id] = struct{}{}
	}
	owned, err := service.repository.OwnsAPIKeys(ctx, ids, filter.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("validate usage key ownership: %w", err)
	}
	if !owned {
		return nil, domain.ErrInvalidUsageFilter
	}
	items, err := service.repository.UsageByKeys(ctx, filter, ids)
	if err != nil {
		return nil, fmt.Errorf("query usage by api keys: %w", err)
	}
	return items, nil
}

// ListErrors returns a bounded page of sanitized failed request metadata.
func (service *UsageService) ListErrors(ctx context.Context, filter domain.UsageFilter, code string, page, size int) ([]domain.UsageError, int64, error) {
	if page < 1 || size < 1 || size > 100 || len(code) > 64 {
		return nil, 0, domain.ErrInvalidUsageFilter
	}
	items, total, err := service.repository.ListUsageErrors(ctx, filter, code, page, size)
	if err != nil {
		return nil, 0, fmt.Errorf("list usage errors: %w", err)
	}
	return items, total, nil
}

// FindError returns one sanitized failed request owned by the authenticated identity.
func (service *UsageService) FindError(ctx context.Context, filter domain.UsageFilter, id uint64) (domain.UsageError, error) {
	item, err := service.repository.FindUsageError(ctx, filter, id)
	if err != nil {
		return domain.UsageError{}, fmt.Errorf("find usage error: %w", err)
	}
	return item, nil
}

// NormalizeDateRange converts inclusive business dates to an exclusive UTC range.
func NormalizeDateRange(from, to string, now time.Time) (*time.Time, *time.Time, error) {
	var start, end *time.Time
	if from != "" {
		value, err := parseBusinessDay(from)
		if err != nil {
			return nil, nil, domain.ErrInvalidUsageFilter
		}
		value = value.UTC()
		start = &value
	}
	if to != "" {
		value, err := parseBusinessDay(to)
		if err != nil {
			return nil, nil, domain.ErrInvalidUsageFilter
		}
		value = value.AddDate(0, 0, 1).UTC()
		end = &value
	}
	if start != nil && end != nil && !start.Before(*end) || start != nil && start.After(now.UTC()) {
		return nil, nil, domain.ErrInvalidUsageFilter
	}
	return start, end, nil
}

func parseBusinessDay(value string) (time.Time, error) {
	parsed, err := time.ParseInLocation("2006-01-02", value, usagetime.Location())
	if err != nil {
		return time.Time{}, domain.ErrInvalidUsageFilter
	}
	return parsed, nil
}
