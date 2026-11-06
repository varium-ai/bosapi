package application

import (
	"context"
	"errors"
	"fmt"

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
	orgs := make(map[uint64]struct{}, len(update.Organizations))
	for _, value := range update.Organizations {
		if value.OrganizationID == 0 || value.QPS < 0 || value.QPS > 100000000 || hasID(orgs, value.OrganizationID) {
			return ErrInvalidRateLimit
		}
		orgs[value.OrganizationID] = struct{}{}
	}
	keys := make(map[uint64]struct{}, len(update.Keys))
	for _, value := range update.Keys {
		if value.KeyID == 0 || !validRateLimit(value.RateLimitConfig, true) || hasID(keys, value.KeyID) {
			return ErrInvalidRateLimit
		}
		keys[value.KeyID] = struct{}{}
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
func hasID(values map[uint64]struct{}, id uint64) bool { _, exists := values[id]; return exists }
