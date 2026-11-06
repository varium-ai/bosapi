package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"bosapi/backend/internal/admin/domain"
)

var ErrInvalidOrganization = errors.New("invalid organization")

// OrganizationService coordinates organization CRUD without reserved organizations.
type OrganizationService struct{ repository domain.OrganizationRepository }

// NewOrganizationService constructs organization use cases.
func NewOrganizationService(repository domain.OrganizationRepository) *OrganizationService {
	return &OrganizationService{repository: repository}
}

// List returns a bounded page of organizations and count.
func (service *OrganizationService) List(ctx context.Context, filter domain.OrganizationFilter, page, pageSize int) ([]domain.Organization, int64, error) {
	return service.repository.List(ctx, filter, page, pageSize)
}

// Find returns a tenant or its repository not-found error.
func (service *OrganizationService) Find(ctx context.Context, id uint64) (domain.Organization, error) {
	return service.repository.Find(ctx, id)
}

// Create validates and persists a new organization.
func (service *OrganizationService) Create(ctx context.Context, organization domain.Organization) (domain.Organization, error) {
	if err := validateProviderBindings(organization.InitialBindings); err != nil {
		return domain.Organization{}, ErrInvalidOrganization
	}
	if err := validateOrganization(organization, true); err != nil {
		return domain.Organization{}, err
	}
	organization.Name = strings.TrimSpace(organization.Name)
	if organization.Status == "" {
		organization.Status = "active"
	}
	if err := service.repository.Create(ctx, &organization); err != nil {
		return domain.Organization{}, fmt.Errorf("create organization: %w", err)
	}
	return organization, nil
}

// Update applies validated organization fields.
func (service *OrganizationService) Update(ctx context.Context, id uint64, updates map[string]any) (domain.Organization, error) {
	if id == 0 || len(updates) == 0 {
		return domain.Organization{}, ErrInvalidOrganization
	}
	current, err := service.repository.Find(ctx, id)
	if err != nil {
		return domain.Organization{}, fmt.Errorf("find organization before update: %w", err)
	}
	if name, ok := updates["name"].(string); ok && strings.TrimSpace(name) == "" {
		return domain.Organization{}, ErrInvalidOrganization
	}
	if status, ok := updates["status"].(string); ok && status != "active" && status != "disabled" {
		return domain.Organization{}, ErrInvalidOrganization
	}
	policy := current
	if value, provided := updates["allowed_provider_ids"]; provided {
		providers, ok := value.([]uint64)
		if !ok {
			return domain.Organization{}, ErrInvalidOrganization
		}
		policy.AllowedProviderIDs = providers
	}
	if value, provided := updates["allowed_models"]; provided {
		models, ok := value.([]string)
		if !ok {
			return domain.Organization{}, ErrInvalidOrganization
		}
		policy.AllowedModels = models
	}
	if err := validateOrganization(policy, false); err != nil {
		return domain.Organization{}, err
	}
	if err := service.repository.Update(ctx, id, updates); err != nil {
		return domain.Organization{}, fmt.Errorf("update organization: %w", err)
	}
	return service.repository.Find(ctx, id)
}

// Disable soft-disables an organization without deleting its history.
func (service *OrganizationService) Disable(ctx context.Context, id uint64) error {
	if _, err := service.repository.Find(ctx, id); err != nil {
		return fmt.Errorf("find organization before disabling: %w", err)
	}
	return service.repository.Update(ctx, id, map[string]any{"status": "disabled"})
}

func validateOrganization(organization domain.Organization, creating bool) error {
	if strings.TrimSpace(organization.Name) == "" || len(organization.Name) > 100 {
		return ErrInvalidOrganization
	}
	if organization.Status != "" && organization.Status != "active" && organization.Status != "disabled" {
		return ErrInvalidOrganization
	}
	if creating && organization.Status == "deleted" {
		return ErrInvalidOrganization
	}
	seen := make(map[uint64]bool, len(organization.AllowedProviderIDs))
	for _, providerID := range organization.AllowedProviderIDs {
		if providerID == 0 || seen[providerID] {
			return ErrInvalidOrganization
		}
		seen[providerID] = true
	}
	for _, model := range organization.AllowedModels {
		if strings.TrimSpace(model) == "" || len(model) > 191 {
			return ErrInvalidOrganization
		}
	}
	return nil
}
