package domain

import (
	"context"
	"time"
)

// Provider represents an upstream account without exposing credential material.
type Provider struct {
	ID               uint64            `json:"id"`
	Name             string            `json:"name"`
	Platform         string            `json:"platform"`
	AccountType      string            `json:"account_type"`
	APIFormat        string            `json:"api_format"`
	BaseURL          string            `json:"base_url"`
	Status           string            `json:"status"`
	Priority         int               `json:"priority"`
	ConcurrencyLimit *uint32           `json:"concurrency_limit"`
	Schedulable      bool              `json:"schedulable"`
	RateLimitRPM     *uint32           `json:"rate_limit_rpm"`
	Capabilities     map[string]any    `json:"capabilities"`
	Pricing          map[string]any    `json:"pricing"`
	ModelMapping     map[string]string `json:"model_mapping,omitempty"`
	CreatedByUserID  *uint64           `json:"created_by_user_id,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	VerifiedAt       *time.Time        `json:"verified_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// ProviderFilter contains bounded provider list filters.
type ProviderFilter struct {
	OrganizationID uint64
	Platform       string
	Status         string
	Search         string
	Actor          ProviderActor
}

// ProviderActor identifies the administrator requesting access to a provider.
type ProviderActor struct {
	UserID         uint64
	OrganizationID uint64
	Role           string
}

// ProviderRepository defines provider account persistence operations.
type ProviderRepository interface {
	ListProviders(context.Context, ProviderFilter, int, int) ([]Provider, int64, error)
	FindProvider(context.Context, uint64) (Provider, error)
	CreateProvider(context.Context, Provider, string) (Provider, error)
	UpdateProvider(context.Context, uint64, map[string]any, string) (Provider, error)
	DisableProvider(context.Context, uint64) error
	DeleteProvider(context.Context, uint64) error
	SetStatus(context.Context, uint64, string) error
	ProviderCredential(context.Context, uint64) (Provider, string, error)
	AuthorizeProvider(context.Context, uint64, ProviderActor) error
}
