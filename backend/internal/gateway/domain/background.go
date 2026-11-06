package domain

import (
	"context"
	"time"

	"bosapi/backend/internal/platform/securitycontract"
	"bosapi/backend/internal/platform/securityevidence"
)

// RateLimitKey identifies the dimensions used by a gateway rate-limit decision.
type RateLimitKey struct {
	OrganizationID uint64
	APIKeyID       uint64
	Route          string
	Model          string
	Operation      string
}

// RateLimiter decides whether a request may proceed and reports usage to a counter backend.
// Implementations may use Redis or another distributed counter without changing request handlers.
type RateLimiter interface {
	Allow(context.Context, RateLimitKey, time.Time) (bool, error)
	Record(context.Context, RateLimitKey, time.Time) error
}

type RateLimitDecision struct {
	Allowed    bool
	Reason     RateLimitReason
	Limit      int64
	Remaining  int64
	RetryAfter time.Duration
	Degraded   bool
}

// RateLimitReason identifies the policy that denied admission.
type RateLimitReason string

const (
	RateLimitQPS             RateLimitReason = "qps"
	RateLimitGlobalQPS       RateLimitReason = "global_qps"
	RateLimitOrganizationQPS RateLimitReason = "organization_qps"
	RateLimitKeyQPS          RateLimitReason = "key_qps"
	RateLimitQuota5H         RateLimitReason = "quota_5h"
	RateLimitQuota1D         RateLimitReason = "quota_1d"
	RateLimitQuota7D         RateLimitReason = "quota_7d"
)

func (reason RateLimitReason) IsQuota() bool {
	return reason == RateLimitQuota5H || reason == RateLimitQuota1D || reason == RateLimitQuota7D
}

type RateLimitDecider interface {
	Decide(context.Context, RateLimitKey, time.Time) (RateLimitDecision, error)
}

// Trigger signal values are observability-only weak hints. They must remain
// stable because model analysis may use them later.
const (
	TriggerObserveHit        = "observe_hit"
	TriggerUntrustedUpstream = "untrusted_upstream"
	TriggerEncodedFragment   = "encoded_fragment"
	TriggerInstructionLike   = "instruction_like"
)

// AppendTriggerSignal adds one signal while keeping the result deduplicated and
// in the fixed order above.
func AppendTriggerSignal(signals []string, signal string) []string {
	for _, existing := range signals {
		if existing == signal {
			return signals
		}
	}
	for _, existing := range []string{TriggerObserveHit, TriggerUntrustedUpstream, TriggerEncodedFragment, TriggerInstructionLike} {
		if existing == signal {
			return append(signals, signal)
		}
	}
	return signals
}

// SecurityEvent is the gateway-safe event envelope sent to the asynchronous security sink.
type SecurityEvent struct {
	RequestID        string
	OrganizationID   uint64
	APIKeyID         uint64
	ClientIP         []byte
	RuleID           string
	Action           string
	HitCount         uint32
	HTTPStatus       int
	CreatedAt        time.Time
	ToolCall         *ToolCallMetadata
	RuleVersion      uint64
	RuleScope        string
	RuleSensitivity  string
	RuleName         string
	ProcessingResult string
	Evidence         []securityevidence.Evidence
}

// SecurityEventSink accepts security events without coupling request handling to storage.
type SecurityEventSink interface {
	Publish(context.Context, SecurityEvent) error
}

// SecurityInspection contains request data passed to the security pipeline.
type SecurityInspection struct {
	RequestID      string
	OrganizationID uint64
	APIKeyID       uint64
	Method         string
	Path           string
	Model          string
	Operation      string
	Body           []byte
	ToolCalls      []ToolCall
}

// SecurityInspector evaluates a request before it is forwarded upstream.
type SecurityInspector interface {
	Inspect(context.Context, SecurityInspection) (bool, error)
}

type SecurityInspectionResult struct {
	Allowed     bool
	RuleID      string
	Action      string
	MaskedMatch string
	HitCount    uint32
	Matches     []SecurityRuleMatch
	RuleName    string
	Evidence    []securityevidence.Evidence
	// Signals are weak evidence hints for future model analysis. They are
	// observability-only and must never influence allow, block, mask, or
	// routing decisions.
	Signals []string `json:"-"`
}

// SecurityRuleMatch contains safe metadata for each independently matched rule.
type SecurityRuleMatch struct {
	RuleID      string
	Sensitivity string
	Version     uint64
	Scope       string
	Action      string
	HitCount    uint32
	RuleName    string
	Evidence    []securityevidence.Evidence
}

type StructuredSecurityInspector interface {
	InspectResult(context.Context, SecurityInspection) (SecurityInspectionResult, error)
}

// SecurityContentInspector audits prompt or model content without changing it.
type SecurityContentInspector interface {
	InspectContent(context.Context, string, []byte) (SecurityInspectionResult, error)
}

// SensitiveContentSanitizer replaces credentials before content is sent upstream.
type SensitiveContentSanitizer interface {
	SanitizeContent(context.Context, string, []byte) ([]byte, bool, error)
}

// Notification is an asynchronous notification envelope for email, Feishu, or a message bus.
type Notification struct {
	Topic   string
	Key     string
	Payload []byte
}

// NotificationPublisher publishes notifications without exposing a concrete queue to gateway code.
type NotificationPublisher interface {
	Publish(context.Context, Notification) error
}

// BackgroundServices groups replaceable asynchronous gateway dependencies.
type BackgroundServices struct {
	RateLimiter      RateLimiter
	SecurityEvents   SecurityEventSink
	AdvancedSecurity securitycontract.AdvancedDetector
	Notifications    NotificationPublisher
}

// NoopRateLimiter preserves current behavior until a distributed limiter is configured.
type NoopRateLimiter struct{}

// Allow always permits a request because no counter backend is configured.
func (NoopRateLimiter) Allow(context.Context, RateLimitKey, time.Time) (bool, error) {
	return true, nil
}

// Record intentionally does nothing until a counter backend is configured.
func (NoopRateLimiter) Record(context.Context, RateLimitKey, time.Time) error { return nil }

// NoopSecurityEventSink drops events when asynchronous persistence is not configured.
type NoopSecurityEventSink struct{}

// Publish intentionally does nothing until an event sink is configured.
func (NoopSecurityEventSink) Publish(context.Context, SecurityEvent) error { return nil }

// NoopNotificationPublisher drops notifications when no queue is configured.
type NoopNotificationPublisher struct{}

// Publish intentionally does nothing until a notification backend is configured.
func (NoopNotificationPublisher) Publish(context.Context, Notification) error { return nil }

// defaultBackgroundServices returns safe defaults so request handling never depends on optional infrastructure.
func defaultBackgroundServices() BackgroundServices {
	return BackgroundServices{
		RateLimiter:      NoopRateLimiter{},
		SecurityEvents:   NoopSecurityEventSink{},
		AdvancedSecurity: securitycontract.NoopAdvancedDetector{},
		Notifications:    NoopNotificationPublisher{},
	}
}

// normalizeBackgroundServices fills omitted optional dependencies with safe no-op implementations.
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
	if services.Notifications == nil {
		services.Notifications = defaults.Notifications
	}
	return services
}
