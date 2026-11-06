// Package errorcode is the registry for error codes exposed or persisted by the backend.
// Add new codes here instead of introducing string literals at response call sites.
package errorcode

import "strconv"

// Code identifies a stable backend error condition.
type Code string

const (
	// Common HTTP API errors.
	InvalidRequest Code = "invalid_request"
	Unauthorized   Code = "unauthorized"
	Forbidden      Code = "forbidden"
	NotFound       Code = "not_found"
	InternalError  Code = "internal_error"
	Conflict       Code = "conflict"

	// Authentication and account errors.
	InvalidMFA             Code = "invalid_mfa"
	InvalidOTP             Code = "invalid_otp"
	InvalidCredentials     Code = "invalid_credentials"
	AuthenticationError    Code = "authentication_error"
	InactiveUser           Code = "inactive_user"
	InvalidOAuthSession    Code = "invalid_oauth_session"
	OAuthExchangeFailed    Code = "oauth_exchange_failed"
	AccountIdentityMissing Code = "account_identity_missing"

	// Provider and gateway errors.
	ProviderUnavailable           Code = "provider_unavailable"
	ProviderDiscoveryFailed       Code = "provider_discovery_failed"
	ProviderCapabilityUnavailable Code = "provider_capability_unavailable"
	ProviderAuthFailed            Code = "provider_auth_failed"
	ProviderAccessDenied          Code = "provider_access_denied"
	ProviderRateLimited           Code = "provider_rate_limited"
	ProviderInUse                 Code = "provider_in_use"
	InvalidProvider               Code = "invalid_provider"
	InvalidModel                  Code = "invalid_model"
	ModelNotAllowed               Code = "model_not_allowed"
	LiveUnavailable               Code = "live_unavailable"
	ImageTaskUnavailable          Code = "image_task_unavailable"
	UnsupportedConversion         Code = "unsupported_conversion"
	UnsupportedUpstreamFeature    Code = "unsupported_upstream_feature"
	UpstreamError                 Code = "upstream_error"
	UpstreamTimeout               Code = "upstream_timeout"
	StorageFailed                 Code = "storage_failed"
	SyncFailed                    Code = "sync_failed"

	// Request policy and quota errors.
	MethodNotAllowed           Code = "method_not_allowed"
	RequestTooLarge            Code = "request_too_large"
	UnsupportedMediaType       Code = "unsupported_media_type"
	UpgradeRequired            Code = "upgrade_required"
	PolicyNotAllowed           Code = "policy_not_allowed"
	QuotaExceeded              Code = "quota_exceeded"
	QuotaAccountingUnavailable Code = "quota_accounting_unavailable"
	RateLimited                Code = "rate_limited"
	RateLimiterUnavailable     Code = "rate_limiter_unavailable"
	CounterBackendUnavailable  Code = "counter_backend_unavailable"

	// Security events and inspection errors.
	SecurityBlocked               Code = "security_blocked"
	SecurityInspectionUnavailable Code = "security_inspection_unavailable"
	ToolCallInspectionUnavailable Code = "tool_call_inspection_unavailable"
	ToolCallInvalid               Code = "tool_call_invalid"
	UnauthorizedTool              Code = "unauthorized_tool"

	// Asynchronous report and webhook delivery errors.
	WorkerTimeout                     Code = "worker_timeout"
	InvalidPeriod                     Code = "invalid_period"
	AggregationFailed                 Code = "aggregation_failed"
	DeliveryFailed                    Code = "delivery_failed"
	AttemptsExhausted                 Code = "attempts_exhausted"
	ConfigurationLoadFailed           Code = "configuration_load_failed"
	SigningSecretUnavailable          Code = "signing_secret_unavailable"
	InvalidWebhookURL                 Code = "invalid_webhook_url"
	DeliveryInterrupted               Code = "delivery_interrupted"
	RequestTimeout                    Code = "request_timeout"
	ConnectionFailedOrAddressRejected Code = "connection_failed_or_address_rejected"
)

// HTTPStatus builds the stable webhook delivery code used for non-success HTTP responses.
func HTTPStatus(status int) Code {
	return Code("http_" + strconv.Itoa(status))
}
