package infrastructure

import (
	"encoding/json"
	"slices"

	"bosapi/backend/internal/admin/domain"
	"gorm.io/gorm"
)

func setBindingPolicyUpdates(updates map[string]any, bindings []domain.ProviderBinding) error {
	providers, models := bindingPolicySummary(bindings)
	providersJSON, err := json.Marshal(providers)
	if err != nil {
		return err
	}
	modelsJSON, err := json.Marshal(models)
	if err != nil {
		return err
	}
	updates["allowed_provider_ids"] = string(providersJSON)
	updates["allowed_models"] = string(modelsJSON)
	return nil
}

// Both write entry points use this helper inside a transaction holding the same
// organization row lock. Never call the public repository method from here:
// starting another transaction would break atomicity across the two tables.
func replaceProviderBindingRecords(tx *gorm.DB, organizationID uint64, bindings []domain.ProviderBinding) error {
	ids := make([]uint64, 0, len(bindings))
	for _, binding := range bindings {
		ids = append(ids, binding.AccountID)
	}
	if len(ids) > 0 {
		var count int64
		if err := tx.Model(&ProviderRecord{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(ids)) {
			return gorm.ErrRecordNotFound
		}
	}
	if err := tx.Where("organization_id = ?", organizationID).Delete(&ProviderBindingRecord{}).Error; err != nil {
		return err
	}
	for _, binding := range bindings {
		models, err := json.Marshal(binding.ModelAllowlist)
		if err != nil {
			return err
		}
		record := ProviderBindingRecord{OrganizationID: organizationID, AccountID: binding.AccountID, Priority: binding.Priority, ModelAllowlist: string(models), Status: binding.Status}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
	}
	return nil
}

// Global model edits are distributed to each selected channel. Retained channels
// keep their own model filters unless the global model selection actually changed.
// Paused channels retain their priority and policy until explicitly reselected.
func organizationPolicyBindings(tx *gorm.DB, providerIDs []uint64, models []string, existing []domain.ProviderBinding, modelsChanged bool) ([]domain.ProviderBinding, error) {
	items := make([]domain.ProviderBinding, 0, len(providerIDs)+len(existing))
	if len(providerIDs) == 0 {
		return items, nil
	}
	models = slices.Clone(models)
	if slices.Contains(models, "*") {
		models = []string{}
	}
	byID := make(map[uint64]domain.ProviderBinding, len(existing))
	for _, binding := range existing {
		byID[binding.AccountID] = binding
	}
	needsModels := len(models) > 0 && modelsChanged
	for _, id := range providerIDs {
		previous, found := byID[id]
		if !found || previous.Status != "active" {
			needsModels = needsModels || len(models) > 0
		}
	}
	available := make(map[uint64]map[string]bool)
	if needsModels {
		var rows []struct {
			AccountID uint64
			ModelName string
		}
		if err := tx.Table("provider_models").Select("account_id, model_name").Where("account_id IN ? AND status = ?", providerIDs, "active").Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			if available[row.AccountID] == nil {
				available[row.AccountID] = map[string]bool{}
			}
			available[row.AccountID][row.ModelName] = true
		}
	}
	selected := make(map[uint64]bool, len(providerIDs))
	for _, id := range providerIDs {
		selected[id] = true
		binding, found := byID[id]
		if modelsChanged || !found || binding.Status != "active" {
			binding.ModelAllowlist = []string{}
			for _, model := range models {
				if available[id][model] {
					binding.ModelAllowlist = append(binding.ModelAllowlist, model)
				}
			}
			// Empty means unrestricted, so a channel with no matching selected
			// model must retain the explicit filter instead of gaining all models.
			if len(models) > 0 && len(binding.ModelAllowlist) == 0 {
				binding.ModelAllowlist = slices.Clone(models)
			}
		}
		binding.AccountID, binding.Status = id, "active"
		items = append(items, binding)
	}
	for _, binding := range existing {
		if binding.Status != "active" && !selected[binding.AccountID] {
			items = append(items, binding)
		}
	}
	return items, nil
}

func sameProviderIDs(a, b []uint64) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func sameModels(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
