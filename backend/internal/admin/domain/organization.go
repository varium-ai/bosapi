package domain

import (
	"context"
	"time"
)

// Organization is the tenant boundary used to scope users, keys, and providers.
type Organization struct {
	InitialBindings     []ProviderBinding `json:"-"`
	ID                  uint64            `json:"id"`
	Name                string            `json:"name"`
	Description         string            `json:"description"`
	Status              string            `json:"status"`
	AllowedProviderIDs  []uint64          `json:"allowed_provider_ids"`
	AllowedModels       []string          `json:"allowed_models"`
	HasActiveBindings   bool              `json:"has_active_bindings"`
	HasProviderBindings bool              `json:"has_provider_bindings"`
	HasAPIKeys          bool              `json:"has_api_keys"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

// OrganizationRepository defines tenant persistence operations.
type OrganizationRepository interface {
	List(context.Context, OrganizationFilter, int, int) ([]Organization, int64, error)
	Find(context.Context, uint64) (Organization, error)
	Create(context.Context, *Organization) error
	Update(context.Context, uint64, map[string]any) error
}

// OrganizationFilter carries validated organization list constraints.
type OrganizationFilter struct {
	ID     uint64
	Status string
	Search string
}
