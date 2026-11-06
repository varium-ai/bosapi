package domain

import (
	"time"
	"unicode/utf8"
)

// APIKey represents an organization-owned gateway credential using a non-reversible digest.
type APIKey struct {
	ID                                                      uint64
	Name                                                    string
	KeyHash                                                 string
	KeyPrefix                                               string
	KeySuffix                                               string
	KeyEncrypted                                            string
	UserID                                                  uint64 // Legacy metadata, ignored for access and billing.
	OrganizationID                                          uint64
	AllowedProviderIDs                                      []uint64
	AllowedModels                                           []string
	Status                                                  string
	SecurityInspectionUpstreamEnabled                       bool
	SecurityInspectionDownstreamEnabled                     bool
	ExpiresAt                                               *time.Time
	QuotaAmount                                             *float64
	QuotaTokens                                             *int64
	QuotaUsedAmount                                         float64
	QuotaUsedTokens                                         int64
	UsageTodayAmount, Usage30dAmount                        *float64
	UsageStatsStatus                                        string
	UsageStatsAsOf                                          *time.Time
	DailyQuotaAmount, WeeklyQuotaAmount, MonthlyQuotaAmount *float64
	DailyQuotaTokens, WeeklyQuotaTokens, MonthlyQuotaTokens *int64
	DailyUsedAmount, WeeklyUsedAmount, MonthlyUsedAmount    float64
	DailyUsedTokens, WeeklyUsedTokens, MonthlyUsedTokens    int64
	RateLimit5H                                             *int64
	RateLimit1D                                             *int64
	RateLimit7D                                             *int64
	IPAllowlist                                             []string
	IPBlocklist                                             []string
	LastUsedAt                                              *time.Time
	CreatedAt                                               time.Time
	UpdatedAt                                               time.Time
	DeletedAt                                               *time.Time
}

// SafeAPIKey excludes the full credential and digest from all ordinary responses.
type SafeAPIKey struct {
	UsageStatsStatus                    string     `json:"usage_stats_status,omitempty"`
	UsageStatsAsOf                      *time.Time `json:"usage_stats_as_of,omitempty"`
	IPAllowlist                         []string   `json:"ip_allowlist"`
	IPBlocklist                         []string   `json:"ip_blocklist"`
	ID                                  uint64     `json:"id"`
	Name                                string     `json:"name"`
	KeyPrefix                           string     `json:"key_prefix"`
	MaskedKey                           string     `json:"masked_key"`
	KeySuffix                           string     `json:"key_suffix,omitempty"`
	AllowedProviderIDs                  []uint64   `json:"allowed_provider_ids"`
	AllowedModels                       []string   `json:"allowed_models"`
	OrganizationID                      uint64     `json:"organization_id"`
	Status                              string     `json:"status"`
	SecurityInspectionUpstreamEnabled   bool       `json:"security_inspection_upstream_enabled"`
	SecurityInspectionDownstreamEnabled bool       `json:"security_inspection_downstream_enabled"`
	SecurityInspectionEnabled           bool       `json:"security_inspection_enabled"`
	ExpiresAt                           *time.Time `json:"expires_at"`
	QuotaAmount                         *float64   `json:"quota_amount"`
	QuotaTokens                         *int64     `json:"quota_tokens"`
	QuotaUsedAmount                     float64    `json:"quota_used_amount"`
	QuotaUsedTokens                     int64      `json:"quota_used_tokens"`
	UsageTodayAmount                    *float64   `json:"usage_today_amount"`
	Usage30dAmount                      *float64   `json:"usage_30d_amount"`
	DailyQuotaAmount                    *float64   `json:"daily_quota_amount"`
	WeeklyQuotaAmount                   *float64   `json:"weekly_quota_amount"`
	MonthlyQuotaAmount                  *float64   `json:"monthly_quota_amount"`
	DailyQuotaTokens                    *int64     `json:"daily_quota_tokens"`
	WeeklyQuotaTokens                   *int64     `json:"weekly_quota_tokens"`
	MonthlyQuotaTokens                  *int64     `json:"monthly_quota_tokens"`
	DailyUsedAmount                     float64    `json:"daily_used_amount"`
	WeeklyUsedAmount                    float64    `json:"weekly_used_amount"`
	MonthlyUsedAmount                   float64    `json:"monthly_used_amount"`
	DailyUsedTokens                     int64      `json:"daily_used_tokens"`
	WeeklyUsedTokens                    int64      `json:"weekly_used_tokens"`
	MonthlyUsedTokens                   int64      `json:"monthly_used_tokens"`
	RateLimit5H                         *int64     `json:"rate_limit_5h"`
	RateLimit1D                         *int64     `json:"rate_limit_1d"`
	RateLimit7D                         *int64     `json:"rate_limit_7d"`
	LastUsedAt                          *time.Time `json:"last_used_at"`
	CreatedAt                           time.Time  `json:"created_at"`
}

// Redacted returns the safe view shared by list and detail endpoints.
func (key APIKey) Redacted() SafeAPIKey {
	return SafeAPIKey{UsageStatsStatus: key.UsageStatsStatus, UsageStatsAsOf: key.UsageStatsAsOf,
		IPAllowlist: key.IPAllowlist, IPBlocklist: key.IPBlocklist, ID: key.ID, Name: key.Name, KeyPrefix: key.KeyPrefix, KeySuffix: key.KeySuffix, MaskedKey: key.KeyPrefix + "••••••••",
		AllowedProviderIDs: key.AllowedProviderIDs, AllowedModels: key.AllowedModels, OrganizationID: key.OrganizationID, Status: key.Status,
		SecurityInspectionUpstreamEnabled: key.SecurityInspectionUpstreamEnabled, SecurityInspectionDownstreamEnabled: key.SecurityInspectionDownstreamEnabled,
		SecurityInspectionEnabled: key.SecurityInspectionUpstreamEnabled && key.SecurityInspectionDownstreamEnabled, ExpiresAt: key.ExpiresAt,
		QuotaAmount: key.QuotaAmount, QuotaTokens: key.QuotaTokens, QuotaUsedAmount: key.QuotaUsedAmount, QuotaUsedTokens: key.QuotaUsedTokens,
		UsageTodayAmount: key.UsageTodayAmount, Usage30dAmount: key.Usage30dAmount,
		DailyQuotaAmount: key.DailyQuotaAmount, WeeklyQuotaAmount: key.WeeklyQuotaAmount, MonthlyQuotaAmount: key.MonthlyQuotaAmount,
		DailyQuotaTokens: key.DailyQuotaTokens, WeeklyQuotaTokens: key.WeeklyQuotaTokens, MonthlyQuotaTokens: key.MonthlyQuotaTokens,
		DailyUsedAmount: key.DailyUsedAmount, WeeklyUsedAmount: key.WeeklyUsedAmount, MonthlyUsedAmount: key.MonthlyUsedAmount,
		DailyUsedTokens: key.DailyUsedTokens, WeeklyUsedTokens: key.WeeklyUsedTokens, MonthlyUsedTokens: key.MonthlyUsedTokens,
		RateLimit5H: key.RateLimit5H, RateLimit1D: key.RateLimit1D, RateLimit7D: key.RateLimit7D, LastUsedAt: key.LastUsedAt, CreatedAt: key.CreatedAt,
	}
}

// SafeKeySuffix preserves at least one hidden character between the public prefix and last four characters.
func SafeKeySuffix(raw, prefix string) string {
	runes := []rune(raw)
	if len(runes) <= utf8.RuneCountInString(prefix)+4 {
		return ""
	}
	return string(runes[len(runes)-4:])
}
