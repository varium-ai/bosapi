package domain

import (
	"context"
	"time"
)

// KeyTemplateSettings contains reusable key policy, excluding names and credentials.
type KeyTemplateSettings struct {
	OrganizationID     uint64     `json:"organization_id"`
	UserID             uint64     `json:"-"` // Legacy settings are ignored; keys belong to organizations.
	AllowedProviderIDs []uint64   `json:"allowed_provider_ids"`
	AllowedModels      []string   `json:"allowed_models"`
	ExpiresAt          *time.Time `json:"expires_at"`
	QuotaAmount        *float64   `json:"quota_amount"`
	QuotaTokens        *int64     `json:"quota_tokens"`
	DailyQuotaAmount   *float64   `json:"daily_quota_amount"`
	WeeklyQuotaAmount  *float64   `json:"weekly_quota_amount"`
	MonthlyQuotaAmount *float64   `json:"monthly_quota_amount"`
	DailyQuotaTokens   *int64     `json:"daily_quota_tokens"`
	WeeklyQuotaTokens  *int64     `json:"weekly_quota_tokens"`
	MonthlyQuotaTokens *int64     `json:"monthly_quota_tokens"`
	RateLimit5H        *int64     `json:"rate_limit_5h"`
	RateLimit1D        *int64     `json:"rate_limit_1d"`
	RateLimit7D        *int64     `json:"rate_limit_7d"`
	IPAllowlist        []string   `json:"ip_allowlist"`
	IPBlocklist        []string   `json:"ip_blocklist"`
}

// KeyTemplate persists one creator's reusable configuration without generating a key.
type KeyTemplate struct {
	ID        uint64              `json:"id"`
	CreatedBy uint64              `json:"-"`
	Name      string              `json:"name"`
	Settings  KeyTemplateSettings `json:"settings"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

// KeyTemplateRepository requires creator scope for every operation, including administrators.
type KeyTemplateRepository interface {
	List(context.Context, uint64, int, int) ([]KeyTemplate, int64, error)
	Save(context.Context, *KeyTemplate) error
	Delete(context.Context, uint64, uint64) error
}
