package domain

import (
	"context"
	"time"
)

// ProviderModel is a discovered model exposed within provider administration.
type ProviderModel struct {
	ID              uint64         `json:"id"`
	ProviderID      uint64         `json:"provider_id"`
	ModelName       string         `json:"model_name"`
	Capabilities    map[string]any `json:"capabilities"`
	PriceInput      float64        `json:"price_input_per_1m"`
	PriceOutput     float64        `json:"price_output_per_1m"`
	PriceCacheRead  float64        `json:"price_cache_read_per_1m"`
	PriceCacheWrite float64        `json:"price_cache_write_per_1m"`
	Status          string         `json:"status"`
	DiscoveredAt    *time.Time     `json:"discovered_at"`
}

// ProviderModelRepository defines bounded provider model reads.
type ProviderModelRepository interface {
	ListProviderModels(context.Context, uint64, string, int, int) ([]ProviderModel, int64, error)
	UpsertProviderModels(context.Context, uint64, []ProviderModel) error
	UpdateModelPricing(ctx context.Context, providerID, modelID uint64, priceInput, priceOutput, priceCacheRead, priceCacheWrite float64, status string, capabilities ...map[string]any) (ProviderModel, error)
}
