package application

import (
	"context"
	"testing"

	"bosapi/backend/internal/admin/domain"
)

type mockProviderModelRepository struct {
	updateCalled bool
	lastProvider uint64
	lastModel    uint64
	lastInput    float64
	lastOutput   float64
	lastStatus   string
}

func (m *mockProviderModelRepository) ListProviderModels(ctx context.Context, providerID uint64, status string, page, pageSize int) ([]domain.ProviderModel, int64, error) {
	return nil, 0, nil
}

func (m *mockProviderModelRepository) UpsertProviderModels(ctx context.Context, providerID uint64, models []domain.ProviderModel) error {
	return nil
}

func (m *mockProviderModelRepository) UpdateModelPricing(ctx context.Context, providerID, modelID uint64, priceInput, priceOutput, priceCacheRead, priceCacheWrite float64, status string, capabilities ...map[string]any) (domain.ProviderModel, error) {
	m.updateCalled = true
	m.lastProvider = providerID
	m.lastModel = modelID
	m.lastInput = priceInput
	m.lastOutput = priceOutput
	m.lastStatus = status
	return domain.ProviderModel{
		ID:          modelID,
		ProviderID:  providerID,
		ModelName:   "test-model",
		PriceInput:  priceInput,
		PriceOutput: priceOutput,
		Status:      status,
	}, nil
}

func TestUpdateModelPricing(t *testing.T) {
	repo := &mockProviderModelRepository{}
	svc := NewProviderModelService(repo)

	// Valid update
	model, err := svc.UpdatePricing(context.Background(), 1, 10, UpdateModelPricingInput{
		PriceInput:  0.05,
		PriceOutput: 0.10,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.updateCalled || model.PriceInput != 0.05 || model.PriceOutput != 0.10 {
		t.Fatalf("expected pricing updated, got %+v", model)
	}

	// Invalid negative price
	_, err = svc.UpdatePricing(context.Background(), 1, 10, UpdateModelPricingInput{
		PriceInput: -1,
	})
	if err == nil {
		t.Fatalf("expected error for negative input price")
	}

	// Invalid ID
	_, err = svc.UpdatePricing(context.Background(), 0, 10, UpdateModelPricingInput{
		PriceInput: 0.5,
	})
	if err == nil {
		t.Fatalf("expected error for zero provider ID")
	}
}
