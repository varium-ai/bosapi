package application

import (
	"context"
	"fmt"
	"strings"

	"bosapi/backend/internal/admin/domain"
)

// DailyByOrganization returns paged organization totals with an explicit business date range.
func (service *AdminUsageService) DailyByOrganization(ctx context.Context, filter domain.AdminOrganizationUsageFilter, page, size int) (domain.AdminOrganizationUsageDashboard, error) {
	filter.Search = strings.TrimSpace(filter.Search)
	if err := validateAdminOrganizationUsage(filter, page, size); err != nil {
		return domain.AdminOrganizationUsageDashboard{}, err
	}
	result, err := service.repository.DailyAdminOrganizationUsage(ctx, filter, page, size)
	if err != nil {
		return domain.AdminOrganizationUsageDashboard{}, fmt.Errorf("aggregate daily user usage: %w", err)
	}
	return result, nil
}

func validateAdminOrganizationUsage(filter domain.AdminOrganizationUsageFilter, page, size int) error {
	if err := validateAdminUsage(filter.AdminUsageFilter); err != nil {
		return err
	}
	if filter.From == nil || filter.To == nil || filter.To.Sub(*filter.From).Hours() > 366*24 {
		return ErrInvalidAdminUsageFilter
	}
	if page < 1 || size < 1 || size > 100 || len(filter.Search) > 320 {
		return ErrInvalidAdminUsageFilter
	}
	return nil
}
