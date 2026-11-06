package infrastructure

import (
	"context"
	"encoding/json"
	"sort"

	"bosapi/backend/internal/admin/domain"
	"gorm.io/gorm"
)

// PublicModelRepository reads public model publication through organizations and provider bindings.
type PublicModelRepository struct{ database *gorm.DB }

// NewPublicModelRepository constructs the public model catalog repository.
func NewPublicModelRepository(database *gorm.DB) *PublicModelRepository {
	return &PublicModelRepository{database: database}
}

// ListPublicModels returns active provider models explicitly admitted by an active organization.
func (repository *PublicModelRepository) ListPublicModels(ctx context.Context) ([]domain.PublicModel, error) {
	type row struct {
		ModelName        string
		Platform         string
		CapabilitiesJSON string
	}
	var rows []row
	err := repository.database.WithContext(ctx).Table("provider_models AS pm").
		Select("pm.model_name, a.platform, pm.capabilities_json").
		Joins("JOIN accounts AS a ON a.id = pm.account_id").
		Joins("JOIN organization_provider_bindings AS b ON b.account_id = a.id").
		Joins("JOIN organizations AS o ON o.id = b.organization_id").
		Where("pm.status = ? AND a.status = ? AND b.status = ? AND o.status = ?", "active", "active", "active", "active").
		Where("a.schedulable = ?", true).
		Where("(b.model_allowlist IS NULL OR JSON_TYPE(b.model_allowlist) = 'NULL' OR JSON_LENGTH(b.model_allowlist) = 0 OR JSON_CONTAINS(b.model_allowlist, JSON_QUOTE(pm.model_name)))").
		Distinct().Order("pm.model_name ASC, a.platform ASC").Limit(1000).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	unique := make(map[string]domain.PublicModel, len(rows))
	for _, row := range rows {
		key := row.Platform + "\x00" + row.ModelName
		if _, exists := unique[key]; exists {
			continue
		}
		var capabilities map[string]any
		if row.CapabilitiesJSON != "" {
			if err := json.Unmarshal([]byte(row.CapabilitiesJSON), &capabilities); err != nil {
				return nil, err
			}
		}
		unique[key] = domain.PublicModel{ID: row.ModelName, Platform: row.Platform, Capabilities: capabilities, Status: "active"}
	}
	items := make([]domain.PublicModel, 0, len(unique))
	for _, model := range unique {
		items = append(items, model)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ID == items[j].ID {
			return items[i].Platform < items[j].Platform
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

var _ domain.PublicModelRepository = (*PublicModelRepository)(nil)
