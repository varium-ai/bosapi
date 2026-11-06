package domain

import "context"

// ProviderBinding describes one provider's organization-level availability.
type ProviderBinding struct {
	AccountID      uint64   `json:"account_id"`
	Priority       int      `json:"priority"`
	ModelAllowlist []string `json:"model_allowlist"`
	Status         string   `json:"status"`
}

// ProviderBindingRepository atomically replaces provider bindings for an organization.
type ProviderBindingRepository interface {
	ListProviderBindings(context.Context, uint64) ([]ProviderBinding, error)
	ReplaceProviderBindings(context.Context, uint64, []ProviderBinding) error
}
