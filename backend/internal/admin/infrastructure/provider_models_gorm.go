package infrastructure

import (
	"context"
	"encoding/json"
	"time"

	"bosapi/backend/internal/admin/domain"
	"gorm.io/gorm"
)

type providerModelRecord struct {
	ID               uint64  `gorm:"primaryKey"`
	AccountID        uint64  `gorm:"column:account_id;not null;uniqueIndex:uq_provider_model"`
	ModelName        string  `gorm:"size:191;not null;uniqueIndex:uq_provider_model"`
	CapabilitiesJSON string  `gorm:"column:capabilities_json;type:json"`
	PriceInput       float64 `gorm:"column:price_input_per_1m"`
	PriceOutput      float64 `gorm:"column:price_output_per_1m"`
	PriceCacheRead   float64 `gorm:"column:price_cache_read_per_1m"`
	PriceCacheWrite  float64 `gorm:"column:price_cache_write_per_1m"`
	Status           string  `gorm:"size:16;not null;default:active"`
	DiscoveredAt     *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (providerModelRecord) TableName() string { return "provider_models" }

// ProviderModelRepository reads model metadata without accessing provider credentials.
type ProviderModelRepository struct{ database *gorm.DB }

// NewProviderModelRepository constructs a Gorm-backed provider model repository.
func NewProviderModelRepository(database *gorm.DB) *ProviderModelRepository {
	return &ProviderModelRepository{database: database}
}

// ListProviderModels returns models filtered by provider and optional status.
func (repository *ProviderModelRepository) ListProviderModels(ctx context.Context, providerID uint64, status string, page, pageSize int) ([]domain.ProviderModel, int64, error) {
	query := repository.database.WithContext(ctx).Model(&providerModelRecord{}).Where("account_id = ?", providerID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []providerModelRecord
	if err := query.Order("model_name ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	items := make([]domain.ProviderModel, 0, len(records))
	for _, record := range records {
		var capabilities map[string]any
		if record.CapabilitiesJSON != "" {
			if err := json.Unmarshal([]byte(record.CapabilitiesJSON), &capabilities); err != nil {
				return nil, 0, err
			}
		}
		items = append(items, domain.ProviderModel{ID: record.ID, ProviderID: record.AccountID, ModelName: record.ModelName, Capabilities: capabilities, PriceInput: record.PriceInput, PriceOutput: record.PriceOutput, PriceCacheRead: record.PriceCacheRead, PriceCacheWrite: record.PriceCacheWrite, Status: record.Status, DiscoveredAt: record.DiscoveredAt})
	}
	return items, total, nil
}

// UpsertProviderModels activates discovered models and deactivates models missing from the latest result.
func (repository *ProviderModelRepository) UpsertProviderModels(ctx context.Context, providerID uint64, models []domain.ProviderModel) error {
	return repository.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		now := time.Now().UTC()
		seen := make([]string, 0, len(models))
		for _, model := range models {
			capabilities, err := json.Marshal(model.Capabilities)
			if err != nil {
				return err
			}
			values := map[string]any{
				"capabilities_json": string(capabilities),
				"status":            "active",
				"discovered_at":     now,
				"updated_at":        now,
			}
			if model.PriceInput > 0 || model.PriceOutput > 0 || model.PriceCacheRead > 0 || model.PriceCacheWrite > 0 {
				values["price_input_per_1m"] = model.PriceInput
				values["price_output_per_1m"] = model.PriceOutput
				values["price_cache_read_per_1m"] = model.PriceCacheRead
				values["price_cache_write_per_1m"] = model.PriceCacheWrite
			}
			result := transaction.Model(&providerModelRecord{}).Where("account_id = ? AND model_name = ?", providerID, model.ModelName).Updates(values)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				record := providerModelRecord{AccountID: providerID, ModelName: model.ModelName, CapabilitiesJSON: string(capabilities), PriceInput: model.PriceInput, PriceOutput: model.PriceOutput, PriceCacheRead: model.PriceCacheRead, PriceCacheWrite: model.PriceCacheWrite, Status: "active", DiscoveredAt: &now}
				if err := transaction.Create(&record).Error; err != nil {
					return err
				}
			}
			seen = append(seen, model.ModelName)
		}
		query := transaction.Model(&providerModelRecord{}).Where("account_id = ?", providerID)
		if len(seen) > 0 {
			query = query.Where("model_name NOT IN ?", seen)
		}
		return query.Update("status", "inactive").Error
	})
}

// UpdateModelPricing updates individual model pricing and synchronizes to the provider's pricing JSON.
func (repository *ProviderModelRepository) UpdateModelPricing(ctx context.Context, providerID, modelID uint64, priceInput, priceOutput, priceCacheRead, priceCacheWrite float64, status string, capsInput ...map[string]any) (domain.ProviderModel, error) {
	var record providerModelRecord
	if err := repository.database.WithContext(ctx).Where("id = ? AND account_id = ?", modelID, providerID).First(&record).Error; err != nil {
		return domain.ProviderModel{}, err
	}
	now := time.Now().UTC()
	updates := map[string]any{
		"price_input_per_1m":       priceInput,
		"price_output_per_1m":      priceOutput,
		"price_cache_read_per_1m":  priceCacheRead,
		"price_cache_write_per_1m": priceCacheWrite,
		"updated_at":               now,
	}
	if status != "" {
		updates["status"] = status
	}
	if len(capsInput) > 0 && capsInput[0] != nil {
		capsBytes, err := json.Marshal(capsInput[0])
		if err == nil {
			updates["capabilities_json"] = string(capsBytes)
			record.CapabilitiesJSON = string(capsBytes)
		}
	}
	if err := repository.database.WithContext(ctx).Model(&record).Updates(updates).Error; err != nil {
		return domain.ProviderModel{}, err
	}

	// Synchronize to accounts.pricing_json so gateway immediately uses model-specific pricing
	var providerRec ProviderRecord
	if err := repository.database.WithContext(ctx).First(&providerRec, providerID).Error; err == nil {
		var pricing map[string]any
		if providerRec.PricingJSON != "" {
			_ = json.Unmarshal([]byte(providerRec.PricingJSON), &pricing)
		}
		if pricing == nil {
			pricing = make(map[string]any)
		}
		modelsMap, ok := pricing["models"].(map[string]any)
		if !ok {
			modelsMap = make(map[string]any)
		}
		modelsMap[record.ModelName] = map[string]any{
			"input":       priceInput,
			"output":      priceOutput,
			"cache_read":  priceCacheRead,
			"cache_write": priceCacheWrite,
		}
		pricing["models"] = modelsMap
		if encoded, err := json.Marshal(pricing); err == nil {
			_ = repository.database.WithContext(ctx).Model(&providerRec).Update("pricing_json", string(encoded)).Error
		}
	}

	record.PriceInput = priceInput
	record.PriceOutput = priceOutput
	record.PriceCacheRead = priceCacheRead
	record.PriceCacheWrite = priceCacheWrite
	if status != "" {
		record.Status = status
	}

	var capabilities map[string]any
	if record.CapabilitiesJSON != "" {
		_ = json.Unmarshal([]byte(record.CapabilitiesJSON), &capabilities)
	}

	return domain.ProviderModel{
		ID:              record.ID,
		ProviderID:      record.AccountID,
		ModelName:       record.ModelName,
		Capabilities:    capabilities,
		PriceInput:      record.PriceInput,
		PriceOutput:     record.PriceOutput,
		PriceCacheRead:  record.PriceCacheRead,
		PriceCacheWrite: record.PriceCacheWrite,
		Status:          record.Status,
		DiscoveredAt:    record.DiscoveredAt,
	}, nil
}

var _ domain.ProviderModelRepository = (*ProviderModelRepository)(nil)
