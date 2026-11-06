package application

import (
	"context"
	"fmt"

	"bosapi/backend/internal/admin/domain"
)

// ProviderModelService coordinates provider model catalog reads.
type ProviderModelService struct {
	repository domain.ProviderModelRepository
}

// NewProviderModelService constructs provider model catalog operations.
func NewProviderModelService(repository domain.ProviderModelRepository) *ProviderModelService {
	return &ProviderModelService{repository: repository}
}

// List returns a bounded page of models belonging to one provider.
func (service *ProviderModelService) List(ctx context.Context, providerID uint64, status string, page, pageSize int) ([]domain.ProviderModel, int64, error) {
	if providerID == 0 || page < 1 || pageSize < 1 || pageSize > 100 || status != "" && status != "active" && status != "inactive" {
		return nil, 0, ErrInvalidProvider
	}
	models, total, err := service.repository.ListProviderModels(ctx, providerID, status, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list provider models: %w", err)
	}
	return models, total, nil
}

// UpdateModelPricingInput defines the model pricing, capabilities, and status fields that can be updated.
type UpdateModelPricingInput struct {
	PriceInput      float64        `json:"price_input_per_1m"`
	PriceOutput     float64        `json:"price_output_per_1m"`
	PriceCacheRead  float64        `json:"price_cache_read_per_1m"`
	PriceCacheWrite float64        `json:"price_cache_write_per_1m"`
	Status          string         `json:"status"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
}

// UpdatePricing validates inputs and updates the provider model pricing and capabilities.
func (service *ProviderModelService) UpdatePricing(ctx context.Context, providerID, modelID uint64, input UpdateModelPricingInput) (domain.ProviderModel, error) {
	if providerID == 0 || modelID == 0 || input.PriceInput < 0 || input.PriceOutput < 0 || input.PriceCacheRead < 0 || input.PriceCacheWrite < 0 {
		return domain.ProviderModel{}, ErrInvalidProvider
	}
	if input.Status != "" && input.Status != "active" && input.Status != "inactive" {
		return domain.ProviderModel{}, ErrInvalidProvider
	}
	return service.repository.UpdateModelPricing(ctx, providerID, modelID, input.PriceInput, input.PriceOutput, input.PriceCacheRead, input.PriceCacheWrite, input.Status, input.Capabilities)
}
