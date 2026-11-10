package domain

import "context"

// RateLimitConfig stores a concurrent-session limit in the legacy qps field, plus optional rolling token quotas.
type RateLimitConfig struct {
	QPS     int64  `json:"qps"`
	Quota5H *int64 `json:"quota_5h,omitempty"`
	Quota1D *int64 `json:"quota_1d,omitempty"`
	Quota7D *int64 `json:"quota_7d,omitempty"`
}

// OrganizationRateLimit associates a concurrent-session limit and optional calendar quotas with an organization.
type OrganizationRateLimit struct {
	OrganizationID uint64 `json:"organization_id"`
	QPS            int64  `json:"qps"`

	DailyQuotaTokens   *int64   `json:"daily_quota_tokens,omitempty"`
	WeeklyQuotaTokens  *int64   `json:"weekly_quota_tokens,omitempty"`
	MonthlyQuotaTokens *int64   `json:"monthly_quota_tokens,omitempty"`
	DailyQuotaAmount   *float64 `json:"daily_quota_amount,omitempty"`
	WeeklyQuotaAmount  *float64 `json:"weekly_quota_amount,omitempty"`
	MonthlyQuotaAmount *float64 `json:"monthly_quota_amount,omitempty"`
}

// KeyRateLimit associates a concurrent-session limit and quota settings with an API key.
type KeyRateLimit struct {
	KeyID uint64 `json:"key_id"`
	RateLimitConfig
}

// RateLimitUpdate is the administrator's atomic rate limit update request.
type RateLimitUpdate struct {
	Global        *RateLimitConfig        `json:"global"`
	Organizations []OrganizationRateLimit `json:"organizations"`
	Keys          []KeyRateLimit          `json:"keys"`
}

// RateLimitRepository persists global, organization, and key settings.
type RateLimitRepository interface {
	ReadRateLimits(context.Context, uint64, uint64) (RateLimitUpdate, error)
	WriteRateLimits(context.Context, RateLimitUpdate, uint64) error
}
