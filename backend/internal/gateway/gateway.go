// Package gateway exposes the gateway lifecycle while keeping transport adapters in interfaces.
package gateway

import (
	gatewayapp "bosapi/backend/internal/gateway/application"
	gatewayinterfaces "bosapi/backend/internal/gateway/interfaces"
)

// Config aliases the application startup configuration.
type Config = gatewayapp.Config

// Gateway aliases the interface adapter lifecycle owned by the gateway package.
type Gateway = gatewayinterfaces.Gateway

// LoadConfig loads validated gateway startup configuration.
func LoadConfig() (Config, error) { return gatewayinterfaces.LoadConfig() }

// New opens the gateway with default asynchronous dependencies.
func New(config Config) (*Gateway, error) { return gatewayinterfaces.New(config) }

// NewWithBackgroundServices opens the gateway with injected asynchronous dependencies.
func NewWithBackgroundServices(config Config, services BackgroundServices) (*Gateway, error) {
	return gatewayinterfaces.NewWithBackgroundServices(config, services)
}

// BackgroundServices aliases the configurable asynchronous ports.
type BackgroundServices = gatewayinterfaces.BackgroundServices

// RateLimitKey aliases the rate-limit dimensions.
type RateLimitKey = gatewayinterfaces.RateLimitKey

// RateLimiter aliases the rate-limit port.
type RateLimiter = gatewayinterfaces.RateLimiter

// SecurityEvent aliases the security event envelope.
type SecurityEvent = gatewayinterfaces.SecurityEvent

// SecurityEventSink aliases the security event port.
type SecurityEventSink = gatewayinterfaces.SecurityEventSink

// SecurityInspection aliases the security request envelope.
type SecurityInspection = gatewayinterfaces.SecurityInspection

// SecurityInspector aliases the security inspection port.
type SecurityInspector = gatewayinterfaces.SecurityInspector

type ToolCall = gatewayinterfaces.ToolCall
type ToolCallMetadata = gatewayinterfaces.ToolCallMetadata
type ToolCallInspector = gatewayinterfaces.ToolCallInspector

// Notification aliases the notification envelope.
type Notification = gatewayinterfaces.Notification

// NotificationPublisher aliases the notification port.
type NotificationPublisher = gatewayinterfaces.NotificationPublisher

// NoopRateLimiter aliases the default local limiter.
type NoopRateLimiter = gatewayinterfaces.NoopRateLimiter

// NoopSecurityEventSink aliases the default local security sink.
type NoopSecurityEventSink = gatewayinterfaces.NoopSecurityEventSink

// NoopNotificationPublisher aliases the default local notification publisher.
type NoopNotificationPublisher = gatewayinterfaces.NoopNotificationPublisher
