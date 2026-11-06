package infrastructure

import (
	"context"
	"strings"

	"gorm.io/gorm"
)

// AvailableModel contains safe discovery metadata for an available upstream model.
type AvailableModel struct {
	ID        string `gorm:"column:model_name"`
	Platform  string
	APIFormat string
}

// ModelCatalogRepository reads models available to the authenticated organization.
type ModelCatalogRepository struct{ database *gorm.DB }

func NewModelCatalogRepository(database *gorm.DB) *ModelCatalogRepository {
	return &ModelCatalogRepository{database: database}
}

// ListAvailableModels includes explicitly assigned channels.
func (repository *ModelCatalogRepository) ListAvailableModels(ctx context.Context, organizationID uint64, formats ...string) ([]AvailableModel, error) {
	return repository.ListAvailableModelsForKey(ctx, organizationID, nil, nil, formats...)
}

func (repository *ModelCatalogRepository) ListAvailableModelsForKey(ctx context.Context, organizationID uint64, providers []uint64, models []string, formats ...string) ([]AvailableModel, error) {
	items := make([]AvailableModel, 0)
	if organizationID == 0 {
		return items, nil
	}
	query := repository.database.WithContext(ctx).Table("provider_models AS pm").
		Select("pm.model_name, a.platform, a.api_format").
		Joins("JOIN accounts AS a ON a.id = pm.account_id").
		Joins("JOIN organizations AS o ON o.id = ?", organizationID).
		Joins("LEFT JOIN organization_provider_bindings AS b ON b.account_id = a.id AND b.organization_id = o.id AND b.status = ?", "active").
		Where("o.id = ?", organizationID).
		Where("pm.status = ? AND a.status = ? AND o.status = ?", "active", "active", "active").
		Where("a.schedulable = ?", true).
		Where("(b.model_allowlist IS NULL OR JSON_TYPE(b.model_allowlist) = 'NULL' OR JSON_LENGTH(b.model_allowlist) = 0 OR JSON_CONTAINS(b.model_allowlist, JSON_QUOTE(pm.model_name)) OR (a.api_format = 'gemini' AND (JSON_CONTAINS(b.model_allowlist, JSON_QUOTE(CASE WHEN a.api_format = 'gemini' AND pm.model_name LIKE 'models/%' THEN SUBSTRING(pm.model_name, 8) ELSE pm.model_name END)) OR JSON_CONTAINS(b.model_allowlist, JSON_QUOTE(CONCAT('models/', CASE WHEN a.api_format = 'gemini' AND pm.model_name LIKE 'models/%' THEN SUBSTRING(pm.model_name, 8) ELSE pm.model_name END))))))").
		Distinct().Order("pm.model_name ASC, a.platform ASC")
	if len(formats) > 0 {
		query = query.Where("a.api_format IN ?", formats)
	}
	query = query.Where("b.account_id IS NOT NULL")
	if len(providers) > 0 {
		query = query.Where("a.id IN ?", providers)
	}
	if len(models) > 0 {
		normalized := make([]string, len(models))
		for i, model := range models {
			normalized[i] = strings.TrimPrefix(model, "models/")
		}
		query = query.Where("pm.model_name IN ? OR (a.api_format = 'gemini' AND CASE WHEN pm.model_name LIKE 'models/%' THEN SUBSTRING(pm.model_name, 8) ELSE pm.model_name END IN ?)", models, normalized)
	}
	err := query.Scan(&items).Error
	return items, err
}
