package application

import (
	"context"
	"time"

	"bosapi/backend/internal/gateway/domain"
)

// BackgroundCoordinator centralizes optional asynchronous gateway capabilities.
type BackgroundCoordinator struct {
	services domain.BackgroundServices
}

// NewBackgroundCoordinator constructs a coordinator with normalized dependencies.
func NewBackgroundCoordinator(services domain.BackgroundServices) *BackgroundCoordinator {
	return &BackgroundCoordinator{services: services}
}

// Allow checks the configured rate limiter before a request enters the gateway.
func (coordinator *BackgroundCoordinator) Allow(ctx context.Context, key domain.RateLimitKey, now time.Time) (bool, error) {
	return coordinator.services.RateLimiter.Allow(ctx, key, now)
}

// RecordRateLimit records a completed request in the configured counter backend.
func (coordinator *BackgroundCoordinator) RecordRateLimit(ctx context.Context, key domain.RateLimitKey, now time.Time) error {
	return coordinator.services.RateLimiter.Record(ctx, key, now)
}

// PublishSecurityEvent queues a security event outside the request handler.
func (coordinator *BackgroundCoordinator) PublishSecurityEvent(ctx context.Context, event domain.SecurityEvent) error {
	return coordinator.services.SecurityEvents.Publish(ctx, event)
}

// PublishNotification submits an asynchronous notification.
func (coordinator *BackgroundCoordinator) PublishNotification(ctx context.Context, notification domain.Notification) error {
	return coordinator.services.Notifications.Publish(ctx, notification)
}
