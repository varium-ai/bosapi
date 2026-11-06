package infrastructure

import (
	"context"
	"encoding/json"
	"sort"

	"bosapi/backend/internal/admin/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProviderBindingRecord maps organization provider availability to Gorm.
type ProviderBindingRecord struct {
	OrganizationID uint64 `gorm:"primaryKey"`
	AccountID      uint64 `gorm:"primaryKey"`
	Priority       int    `gorm:"not null;default:0"`
	ModelAllowlist string `gorm:"type:json"`
	Status         string `gorm:"size:24;not null;default:active"`
}

// TableName maps bindings to the organization provider binding table.
func (ProviderBindingRecord) TableName() string { return "organization_provider_bindings" }

// ProviderBindingRepository persists organization provider binding changes.
type ProviderBindingRepository struct{ database *gorm.DB }

// NewProviderBindingRepository constructs a Gorm-backed binding repository.
func NewProviderBindingRepository(database *gorm.DB) *ProviderBindingRepository {
	return &ProviderBindingRepository{database: database}
}

func (repository *ProviderBindingRepository) ListProviderBindings(ctx context.Context, organizationID uint64) ([]domain.ProviderBinding, error) {
	var organization OrganizationRecord
	if err := repository.database.WithContext(ctx).First(&organization, organizationID).Error; err != nil {
		return nil, err
	}
	var records []ProviderBindingRecord
	if err := repository.database.WithContext(ctx).Where("organization_id = ?", organizationID).Order("priority DESC, account_id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	if len(records) == 0 {
		policy, err := organizationDomain(organization)
		if err != nil {
			return nil, err
		}
		// Old organizations may have an explicit whitelist without binding rows.
		// Show that policy without persisting anything during a read.
		if len(policy.AllowedProviderIDs) == 0 && len(policy.AllowedModels) > 0 {
			if err := repository.database.WithContext(ctx).Model(&ProviderRecord{}).
				Where("status = ? AND schedulable = ?", "active", true).Order("id ASC").Pluck("id", &policy.AllowedProviderIDs).Error; err != nil {
				return nil, err
			}
		}
		return organizationPolicyBindings(repository.database.WithContext(ctx), policy.AllowedProviderIDs, policy.AllowedModels, nil, true)
	}
	items := make([]domain.ProviderBinding, 0, len(records))
	for _, record := range records {
		item, err := providerBindingDomain(record)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func providerBindingDomain(record ProviderBindingRecord) (domain.ProviderBinding, error) {
	models := make([]string, 0)
	if record.ModelAllowlist != "" {
		if err := json.Unmarshal([]byte(record.ModelAllowlist), &models); err != nil {
			return domain.ProviderBinding{}, err
		}
	}
	if models == nil {
		models = []string{}
	}
	return domain.ProviderBinding{AccountID: record.AccountID, Priority: record.Priority, Status: record.Status, ModelAllowlist: models}, nil
}

// ReplaceProviderBindings replaces bindings and their organization policy summary
// together. Locking the tenant serializes simultaneous binding saves.
func (repository *ProviderBindingRepository) ReplaceProviderBindings(ctx context.Context, organizationID uint64, bindings []domain.ProviderBinding) error {
	return repository.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		var organization OrganizationRecord
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).First(&organization, organizationID).Error; err != nil {
			return err
		}
		if err := replaceProviderBindingRecords(transaction, organizationID, bindings); err != nil {
			return err
		}
		updates := map[string]any{}
		if err := setBindingPolicyUpdates(updates, bindings); err != nil {
			return err
		}
		return transaction.Model(&OrganizationRecord{}).Where("id = ?", organizationID).Updates(updates).Error
	})
}

// Empty model allowlists impose no organization-wide filter. Individual binding
// allowlists still enforce channel-specific restrictions in the gateway.
func bindingPolicySummary(bindings []domain.ProviderBinding) ([]uint64, []string) {
	providerIDs := make([]uint64, 0, len(bindings))
	models := make(map[string]struct{})
	unrestricted := false
	for _, binding := range bindings {
		if binding.Status != "active" {
			continue
		}
		providerIDs = append(providerIDs, binding.AccountID)
		if len(binding.ModelAllowlist) == 0 {
			unrestricted = true
		}
		for _, model := range binding.ModelAllowlist {
			models[model] = struct{}{}
		}
	}
	sort.Slice(providerIDs, func(i, j int) bool { return providerIDs[i] < providerIDs[j] })
	modelNames := make([]string, 0, len(models))
	if !unrestricted {
		for model := range models {
			modelNames = append(modelNames, model)
		}
		sort.Strings(modelNames)
	}
	return providerIDs, modelNames
}

var _ domain.ProviderBindingRepository = (*ProviderBindingRepository)(nil)
