package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"bosapi/backend/internal/admin/domain"
)

// ErrInvalidAdminUsageFilter identifies malformed administrative usage filters.
var ErrInvalidAdminUsageFilter = errors.New("invalid admin usage filter")

// AdminUsageService coordinates bounded administrative usage queries.
type AdminUsageService struct{ repository domain.AdminUsageRepository }

// NewAdminUsageService constructs administrative usage operations.
func NewAdminUsageService(repository domain.AdminUsageRepository) *AdminUsageService {
	return &AdminUsageService{repository: repository}
}

// List returns one page of body-free usage records.
func (service *AdminUsageService) List(ctx context.Context, filter domain.AdminUsageFilter, page, size int) ([]domain.AdminUsageRecord, int64, error) {
	if err := validateAdminUsage(filter); err != nil || page < 1 || size < 1 || size > 100 {
		return nil, 0, ErrInvalidAdminUsageFilter
	}
	items, total, err := service.repository.ListAdminUsage(ctx, filter, page, size)
	if err != nil {
		return nil, 0, fmt.Errorf("list administrative usage: %w", err)
	}
	return items, total, nil
}

// Aggregate returns total usage or usage grouped by day, ISO week, or calendar month.
func (service *AdminUsageService) Aggregate(ctx context.Context, filter domain.AdminUsageFilter, period string) ([]domain.AdminUsagePoint, error) {
	if err := validateAdminUsage(filter); err != nil || period != "total" && period != "daily" && period != "weekly" && period != "monthly" {
		return nil, ErrInvalidAdminUsageFilter
	}
	items, err := service.repository.AggregateAdminUsage(ctx, filter, period)
	if err != nil {
		return nil, fmt.Errorf("aggregate administrative usage: %w", err)
	}
	for index := range items {
		items[index].CacheHitRate = cacheHitRate(items[index].InputTokens, items[index].CacheReadTokens)
	}
	return items, nil
}

func cacheHitRate(inputTokens, cacheReadTokens uint64) float64 {
	if inputTokens == 0 {
		return 0
	}
	return float64(cacheReadTokens) / float64(inputTokens)
}

func (service *AdminUsageService) GroupByIP(ctx context.Context, filter domain.AdminUsageFilter, page, size int) ([]domain.AdminUsageIP, int64, error) {
	if _, valid := domain.IPUsageWindow(filter.IPWindowMinutes); !valid {
		return nil, 0, ErrInvalidAdminUsageFilter
	}
	if validateAdminUsage(filter) != nil || filter.APIKeyID == 0 || page < 1 || size < 1 || size > 100 {
		return nil, 0, ErrInvalidAdminUsageFilter
	}
	items, total, err := service.repository.GroupAdminUsageByIP(ctx, filter, page, size)
	if err != nil {
		return nil, 0, fmt.Errorf("group administrative usage by ip: %w", err)
	}
	return items, total, nil
}

func (service *AdminUsageService) GroupByIPSnapshot(ctx context.Context, filter domain.AdminUsageFilter, page, size int) (domain.AdminUsageIPSnapshot, error) {
	if _, valid := domain.IPUsageWindow(filter.IPWindowMinutes); !valid {
		return domain.AdminUsageIPSnapshot{}, ErrInvalidAdminUsageFilter
	}
	if validateAdminUsage(filter) != nil || filter.APIKeyID == 0 || page < 1 || size < 1 || size > 100 {
		return domain.AdminUsageIPSnapshot{}, ErrInvalidAdminUsageFilter
	}
	if source, ok := service.repository.(interface {
		GroupAdminUsageByIPSnapshot(context.Context, domain.AdminUsageFilter, int, int) (domain.AdminUsageIPSnapshot, error)
	}); ok {
		return source.GroupAdminUsageByIPSnapshot(ctx, filter, page, size)
	}
	items, total, err := service.GroupByIP(ctx, filter, page, size)
	return domain.AdminUsageIPSnapshot{Items: items, Total: total}, err
}

func validateAdminUsage(filter domain.AdminUsageFilter) error {
	if filter.Model != "" && len(filter.Model) > 191 {
		return ErrInvalidAdminUsageFilter
	}
	if filter.From != nil && filter.To != nil && !filter.From.Before(*filter.To) {
		return ErrInvalidAdminUsageFilter
	}
	if filter.From != nil && filter.From.After(time.Now().Add(time.Minute)) {
		return ErrInvalidAdminUsageFilter
	}
	return nil
}

func (service *AdminUsageService) AggregateSnapshot(ctx context.Context, filter domain.AdminUsageFilter, period string) (domain.AdminUsageAggregateSnapshot, error) {
	if err := validateAdminUsage(filter); err != nil || (period != "total" && period != "daily" && period != "weekly" && period != "monthly") {
		return domain.AdminUsageAggregateSnapshot{}, ErrInvalidAdminUsageFilter
	}
	if source, ok := service.repository.(interface {
		AggregateAdminUsageSnapshot(context.Context, domain.AdminUsageFilter, string) (domain.AdminUsageAggregateSnapshot, error)
	}); ok {
		return source.AggregateAdminUsageSnapshot(ctx, filter, period)
	}
	items, err := service.Aggregate(ctx, filter, period)
	return domain.AdminUsageAggregateSnapshot{Items: items}, err
}
