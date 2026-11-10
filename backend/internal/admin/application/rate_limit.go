package application

import (
	"context"
	"errors"
	"fmt"
	"math"

	"bosapi/backend/internal/admin/domain"
)

// ErrInvalidRateLimit identifies invalid rate limit configuration.
var ErrInvalidRateLimit = errors.New("invalid rate limit")

// RateLimitService validates and persists rate limit settings.
type RateLimitService struct{ repository domain.RateLimitRepository }

// NewRateLimitService constructs rate limit operations.
func NewRateLimitService(repository domain.RateLimitRepository) *RateLimitService {
	return &RateLimitService{repository: repository}
}

// Read returns configured limits for the requested organization and key.
func (service *RateLimitService) Read(ctx context.Context, organizationID, keyID uint64) (domain.RateLimitUpdate, error) {
	result, err := service.repository.ReadRateLimits(ctx, organizationID, keyID)
	if err != nil {
		return domain.RateLimitUpdate{}, fmt.Errorf("read rate limits: %w", err)
	}
	return result, nil
}

// Write validates and persists a complete rate limit update.
func (service *RateLimitService) Write(ctx context.Context, update domain.RateLimitUpdate, updatedBy uint64) error {
	if update.Global != nil && !validRateLimit(*update.Global, true) {
		return ErrInvalidRateLimit
	}

	seenOrganizations := make(map[uint64]struct{}, len(update.Organizations))
	for _, organization := range update.Organizations {
		if organization.OrganizationID == 0 || organization.QPS < 0 || organization.QPS > 100000000 || !validOrganizationQuotas(organization) {
			return ErrInvalidRateLimit
		}
		if _, exists := seenOrganizations[organization.OrganizationID]; exists {
			return ErrInvalidRateLimit
		}
		seenOrganizations[organization.OrganizationID] = struct{}{}
	}

	seenKeys := make(map[uint64]struct{}, len(update.Keys))
	for _, key := range update.Keys {
		if key.KeyID == 0 || !validRateLimit(key.RateLimitConfig, true) {
			return ErrInvalidRateLimit
		}
		if _, exists := seenKeys[key.KeyID]; exists {
			return ErrInvalidRateLimit
		}
		seenKeys[key.KeyID] = struct{}{}
	}

	if update.Global == nil && len(update.Organizations) == 0 && len(update.Keys) == 0 {
		return ErrInvalidRateLimit
	}

	return service.repository.WriteRateLimits(ctx, update, updatedBy)
}

func validRateLimit(value domain.RateLimitConfig, allowQPSZero bool) bool {
	if value.QPS < 0 || value.QPS > 100000000 || !allowQPSZero && value.QPS == 0 {
		return false
	}
	for _, quota := range []*int64{value.Quota5H, value.Quota1D, value.Quota7D} {
		if quota != nil && *quota < 0 {
			return false
		}
	}
	return true
}

func validOrganizationQuotas(organization domain.OrganizationRateLimit) bool {
	for _, limit := range []*int64{organization.DailyQuotaTokens, organization.WeeklyQuotaTokens, organization.MonthlyQuotaTokens} {
		if limit != nil && *limit < 0 {
			return false
		}
	}
	for _, amount := range []*float64{organization.DailyQuotaAmount, organization.WeeklyQuotaAmount, organization.MonthlyQuotaAmount} {
		if amount != nil && (*amount < 0 || !isFiniteFloat(*amount)) {
			return false
		}
	}
	return true
}

func isFiniteFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
