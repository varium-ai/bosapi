package domain

import "time"

// Provider stores gateway provider credentials and routing metadata.
type Provider struct {
	ID           int               `json:"id"`
	Name         string            `json:"name"`
	Platform     string            `json:"platform"`
	Type         string            `json:"type"`
	Status       string            `json:"status"`
	Models       []string          `json:"models"`
	Capabilities map[string]any    `json:"capabilities,omitempty"`
	Pricing      map[string]any    `json:"pricing,omitempty"`
	ModelMapping map[string]string `json:"model_mapping,omitempty"`
	APIFormat    string            `json:"-"`
	BaseURL      string            `json:"base_url,omitempty"`
	APIKey       string            `json:"api_key,omitempty"`
	AccessToken  string            `json:"access_token,omitempty"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	AccountID    string            `json:"account_id,omitempty"`
	ProjectID    string            `json:"project_id,omitempty"`
	ExpiresAt    time.Time         `json:"token_expires_at,omitempty"`
}

// APIKey stores a hash-only gateway key and its provider binding.
type APIKey struct {
	ID                                                      int        `json:"id"`
	Name                                                    string     `json:"name"`
	OrgID                                                   int        `json:"org_id"`
	Prefix                                                  string     `json:"key_prefix"`
	Hash                                                    string     `json:"hash,omitempty"`
	Status                                                  string     `json:"status"`
	SecurityInspectionUpstreamEnabled                       *bool      `json:"security_inspection_upstream_enabled,omitempty"`
	SecurityInspectionDownstreamEnabled                     *bool      `json:"security_inspection_downstream_enabled,omitempty"`
	ProviderID                                              int        `json:"provider_id"`
	AllowedProviderIDs                                      []uint64   `json:"allowed_provider_ids,omitempty"`
	AllowedModels                                           []string   `json:"allowed_models,omitempty"`
	QuotaAmount                                             *float64   `json:"quota_amount,omitempty"`
	QuotaTokens                                             *int64     `json:"quota_tokens,omitempty"`
	QuotaUsedAmount                                         float64    `json:"quota_used_amount,omitempty"`
	QuotaUsedTokens                                         int64      `json:"quota_used_tokens,omitempty"`
	DailyQuotaAmount, WeeklyQuotaAmount, MonthlyQuotaAmount *float64   `json:"-"`
	DailyQuotaTokens, WeeklyQuotaTokens, MonthlyQuotaTokens *int64     `json:"-"`
	DailyUsedAmount, WeeklyUsedAmount, MonthlyUsedAmount    float64    `json:"-"`
	DailyUsedTokens, WeeklyUsedTokens, MonthlyUsedTokens    int64      `json:"-"`
	RateLimit5H                                             *int64     `json:"rate_limit_5h,omitempty"`
	RateLimit1D                                             *int64     `json:"rate_limit_1d,omitempty"`
	RateLimit7D                                             *int64     `json:"rate_limit_7d,omitempty"`
	ExpiresAt                                               *time.Time `json:"expires_at,omitempty"`
	IPAllowlist                                             []string   `json:"ip_allowlist,omitempty"`
	IPBlocklist                                             []string   `json:"ip_blocklist,omitempty"`
}

type Usage struct {
	Requests     int64                 `json:"requests"`
	Successes    int64                 `json:"successes"`
	Errors       int64                 `json:"errors"`
	DurationMS   int64                 `json:"duration_ms"`
	InputTokens  int64                 `json:"input_tokens"`
	OutputTokens int64                 `json:"output_tokens"`
	Cost         float64               `json:"cost"`
	Models       map[string]ModelUsage `json:"models"`
	Daily        map[string]DailyUsage `json:"daily,omitempty"`
}

type DailyUsage struct {
	Requests     int64   `json:"requests"`
	Successes    int64   `json:"successes"`
	Errors       int64   `json:"errors"`
	DurationMS   int64   `json:"duration_ms"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

type ModelUsage struct {
	Requests     int64   `json:"requests"`
	Successes    int64   `json:"successes"`
	Errors       int64   `json:"errors"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

// GatewayData is the encrypted local gateway aggregate.
type GatewayData struct {
	Providers []Provider       `json:"providers"`
	Keys      []APIKey         `json:"keys"`
	Usage     map[string]Usage `json:"usage,omitempty"`
}
