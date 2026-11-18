package application

import (
	"context"
	"errors"
	"net/http"

	"bosapi/backend/internal/platform/subscription"
)

// SubscriptionUsage reuses the same OAuth credential rotation as gateway requests.
func (service *ProviderService) SubscriptionUsage(ctx context.Context, id uint64) (subscription.UsageSnapshot, error) {
	provider, err := service.Find(ctx, id)
	if err != nil {
		return subscription.UsageSnapshot{}, err
	}
	if provider.AccountType != "oauth" || !subscription.SupportsUsage(provider.Platform) {
		return subscription.UsageSnapshot{}, ErrInvalidProvider
	}
	provider, raw, err := service.Credential(ctx, id)
	if err != nil {
		return subscription.UsageSnapshot{}, err
	}
	if provider.AccountType != "oauth" || !subscription.SupportsUsage(provider.Platform) {
		return subscription.UsageSnapshot{}, ErrInvalidProvider
	}
	cred, err := subscription.Decode(raw, provider.Platform)
	if err != nil {
		return subscription.UsageSnapshot{}, err
	}
	snapshot, err := service.oauthClient.Usage(ctx, provider.Platform, cred)
	switch {
	case err == nil:
		return snapshot, nil
	case errors.Is(err, subscription.UsageStatusError(http.StatusUnauthorized)):
		return snapshot, ErrProviderAuthentication
	case errors.Is(err, subscription.UsageStatusError(http.StatusForbidden)):
		return snapshot, ErrProviderAuthorization
	case errors.Is(err, subscription.UsageStatusError(http.StatusTooManyRequests)):
		return snapshot, ErrProviderRateLimited
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, subscription.UsageStatusError(http.StatusRequestTimeout)), errors.Is(err, subscription.UsageStatusError(http.StatusGatewayTimeout)):
		return snapshot, ErrProviderTimeout
	default:
		return snapshot, subscription.ErrUsageUnavailable
	}
}
