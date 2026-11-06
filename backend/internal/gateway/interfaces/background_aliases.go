package interfaces

import (
	gatewaydomain "bosapi/backend/internal/gateway/domain"
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
)

// BackgroundServices aliases the gateway asynchronous dependency group.
type BackgroundServices = gatewaydomain.BackgroundServices

// RateLimitKey aliases the gateway rate-limit key.
type RateLimitKey = gatewaydomain.RateLimitKey

// RateLimiter aliases the gateway rate-limit port.
type RateLimiter = gatewaydomain.RateLimiter
type RateLimitDecision = gatewaydomain.RateLimitDecision
type RateLimitDecider = gatewaydomain.RateLimitDecider

// SecurityEvent aliases the gateway security event envelope.
type SecurityEvent = gatewaydomain.SecurityEvent

// SecurityEventSink aliases the gateway security event port.
type SecurityEventSink = gatewaydomain.SecurityEventSink

// SecurityInspection aliases the request envelope passed to security services.
type SecurityInspection = gatewaydomain.SecurityInspection

// SecurityInspector aliases the optional security inspection port.
type SecurityInspector = gatewaydomain.SecurityInspector
type SecurityInspectionResult = gatewaydomain.SecurityInspectionResult
type StructuredSecurityInspector = gatewaydomain.StructuredSecurityInspector
type SecurityContentInspector = gatewaydomain.SecurityContentInspector
type SensitiveContentSanitizer = gatewaydomain.SensitiveContentSanitizer
type ToolCall = gatewaydomain.ToolCall
type ToolCallMetadata = gatewaydomain.ToolCallMetadata
type ToolCallInspector = gatewaydomain.ToolCallInspector

// Notification aliases the gateway notification envelope.
type Notification = gatewaydomain.Notification

// NotificationPublisher aliases the gateway notification port.
type NotificationPublisher = gatewaydomain.NotificationPublisher

// NoopRateLimiter is the default local limiter.
type NoopRateLimiter = gatewayinfra.NoopRateLimiter

// NoopSecurityEventSink is the default local event sink.
type NoopSecurityEventSink = gatewayinfra.NoopSecurityEventSink

// NoopNotificationPublisher is the default local notification publisher.
type NoopNotificationPublisher = gatewayinfra.NoopNotificationPublisher

func defaultBackgroundServices() BackgroundServices {
	return gatewayinfra.DefaultBackgroundServices()
}

func normalizeBackgroundServices(services BackgroundServices) BackgroundServices {
	defaults := defaultBackgroundServices()
	if services.RateLimiter == nil {
		services.RateLimiter = defaults.RateLimiter
	}
	if services.SecurityEvents == nil {
		services.SecurityEvents = defaults.SecurityEvents
	}
	if services.AdvancedSecurity == nil {
		services.AdvancedSecurity = defaults.AdvancedSecurity
	}
	services.SecurityEvents = gatewayinfra.WithAdvancedSecurity(services.SecurityEvents, services.AdvancedSecurity)
	if services.Notifications == nil {
		services.Notifications = defaults.Notifications
	}
	return services
}
