package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"bosapi/backend/internal/admin/domain"
)

// ErrInvalidProviderBindings identifies invalid organization provider bindings.
var ErrInvalidProviderBindings = errors.New("invalid provider bindings")

// ProviderBindingService validates and replaces organization provider bindings.
type ProviderBindingService struct {
	repository domain.ProviderBindingRepository
}

// NewProviderBindingService constructs organization provider binding use cases.
func NewProviderBindingService(repository domain.ProviderBindingRepository) *ProviderBindingService {
	return &ProviderBindingService{repository: repository}
}

// List returns saved bindings, including paused bindings, without inventing defaults.
func (service *ProviderBindingService) List(ctx context.Context, organizationID uint64) ([]domain.ProviderBinding, error) {
	if organizationID == 0 {
		return nil, ErrInvalidProviderBindings
	}
	return service.repository.ListProviderBindings(ctx, organizationID)
}

// Replace validates and atomically replaces all configured bindings.
func (service *ProviderBindingService) Replace(ctx context.Context, organizationID uint64, bindings []domain.ProviderBinding) error {
	if organizationID == 0 {
		return ErrInvalidProviderBindings
	}
	if err := validateProviderBindings(bindings); err != nil {
		return err
	}
	if err := service.repository.ReplaceProviderBindings(ctx, organizationID, bindings); err != nil {
		return fmt.Errorf("replace organization provider bindings: %w", err)
	}
	return nil
}

func validateProviderBindings(bindings []domain.ProviderBinding) error {
	if len(bindings) > 500 {
		return ErrInvalidProviderBindings
	}
	seen := make(map[uint64]struct{}, len(bindings))
	for index := range bindings {
		binding := &bindings[index]
		if binding.AccountID == 0 || binding.Priority < -10000 || binding.Priority > 10000 || binding.Status != "active" && binding.Status != "disabled" {
			return ErrInvalidProviderBindings
		}
		if _, exists := seen[binding.AccountID]; exists {
			return ErrInvalidProviderBindings
		}
		seen[binding.AccountID] = struct{}{}
		for _, model := range binding.ModelAllowlist {
			if strings.TrimSpace(model) == "" || len(model) > 191 {
				return ErrInvalidProviderBindings
			}
		}
	}
	return nil
}
