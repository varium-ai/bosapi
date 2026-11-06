package infrastructure

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/edition"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// OrganizationRecord maps organization tenant settings to Gorm.
type OrganizationRecord struct {
	ID                 uint64 `gorm:"primaryKey"`
	Name               string `gorm:"size:128;uniqueIndex;not null"`
	Description        string `gorm:"size:512"`
	Status             string `gorm:"size:24;index;not null;default:active"`
	AllowedProviderIDs string `gorm:"type:json"`
	AllowedModels      string `gorm:"type:json"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// TableName maps records to the tenant table shared with the gateway.
func (OrganizationRecord) TableName() string { return "organizations" }

// OrganizationRepository implements tenant persistence using Gorm.
type OrganizationRepository struct{ database *gorm.DB }

// NewOrganizationRepository constructs a Gorm-backed organization repository.
func NewOrganizationRepository(database *gorm.DB) *OrganizationRepository {
	return &OrganizationRepository{database: database}
}

// List returns non-deleted organizations matching bounded filters.
func (repository *OrganizationRepository) List(ctx context.Context, filter domain.OrganizationFilter, page, pageSize int) ([]domain.Organization, int64, error) {
	query := repository.database.WithContext(ctx).Model(&OrganizationRecord{})
	if filter.ID > 0 {
		query = query.Where("id = ?", filter.ID)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		trimmed := strings.TrimPrefix(search, "Org #")
		trimmed = strings.TrimPrefix(trimmed, "org #")
		trimmed = strings.TrimPrefix(trimmed, "#")
		trimmed = strings.TrimSpace(trimmed)
		if id, err := strconv.ParseUint(trimmed, 10, 64); err == nil && id > 0 {
			query = query.Where("name LIKE ? OR id = ?", "%"+strings.ReplaceAll(search, "%", "\\%")+"%", id)
		} else {
			query = query.Where("name LIKE ?", "%"+strings.ReplaceAll(search, "%", "\\%")+"%")
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []OrganizationRecord
	if err := query.Order("id ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	organizations := make([]domain.Organization, 0, len(records))
	for _, record := range records {
		organization, err := organizationDomain(record)
		if err != nil {
			return nil, 0, err
		}
		organizations = append(organizations, organization)
	}
	if err := repository.applyBindingPolicies(ctx, organizations); err != nil {
		return nil, 0, err
	}
	if err := repository.applyKeyPresence(ctx, organizations); err != nil {
		return nil, 0, err
	}
	return organizations, total, nil
}

// Find returns a non-deleted tenant.
func (repository *OrganizationRepository) Find(ctx context.Context, id uint64) (domain.Organization, error) {
	var record OrganizationRecord
	if err := repository.database.WithContext(ctx).First(&record, id).Error; err != nil {
		return domain.Organization{}, err
	}
	organization, err := organizationDomain(record)
	if err != nil {
		return domain.Organization{}, err
	}
	items := []domain.Organization{organization}
	if err := repository.applyBindingPolicies(ctx, items); err != nil {
		return domain.Organization{}, err
	}
	if err := repository.applyKeyPresence(ctx, items); err != nil {
		return domain.Organization{}, err
	}
	return items[0], nil
}

// Key presence is organization-scoped, including disabled keys. One grouped read
// covers the current page without loading key records or their credentials.
func (repository *OrganizationRepository) applyKeyPresence(ctx context.Context, organizations []domain.Organization) error {
	if len(organizations) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(organizations))
	for _, organization := range organizations {
		ids = append(ids, organization.ID)
	}
	var initialized []uint64
	if err := repository.database.WithContext(ctx).Table("api_keys").
		Where("organization_id IN ?", ids).Distinct().Pluck("organization_id", &initialized).Error; err != nil {
		return err
	}
	for i := range organizations {
		organizations[i].HasAPIKeys = slices.Contains(initialized, organizations[i].ID)
	}
	return nil
}

// Saved bindings determine effective policy; paused bindings must never look like
// an unrestricted organization. Legacy allowlists apply only without saved bindings.
func (repository *OrganizationRepository) applyBindingPolicies(ctx context.Context, organizations []domain.Organization) error {
	if len(organizations) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(organizations))
	for i := range organizations {
		ids = append(ids, organizations[i].ID)
	}
	var records []ProviderBindingRecord
	if err := repository.database.WithContext(ctx).Where("organization_id IN ?", ids).Order("account_id ASC").Find(&records).Error; err != nil {
		return err
	}
	return repository.applyBindingPolicyRecords(ctx, organizations, records)
}

func (repository *OrganizationRepository) applyBindingPolicyRecords(ctx context.Context, organizations []domain.Organization, records []ProviderBindingRecord) error {
	byID := make(map[uint64]*domain.Organization, len(organizations))
	ids := make([]uint64, 0, len(organizations))
	for i := range organizations {
		ids = append(ids, organizations[i].ID)
		byID[organizations[i].ID] = &organizations[i]
	}
	hasActive := false
	for _, record := range records {
		binding, err := providerBindingDomain(record)
		if err != nil {
			return err
		}
		organization := byID[record.OrganizationID]
		if !organization.HasProviderBindings {
			organization.HasProviderBindings = true
			organization.AllowedProviderIDs = []uint64{}
			organization.AllowedModels = []string{}
		}
		if binding.Status != "active" {
			continue
		}
		organization.HasActiveBindings = true
		hasActive = true
		organization.AllowedProviderIDs = append(organization.AllowedProviderIDs, binding.AccountID)
	}
	if !hasActive {
		return nil
	}
	var models []struct {
		OrganizationID uint64
		ModelName      string
	}
	if err := repository.database.WithContext(ctx).Table("provider_models AS pm").
		Select("b.organization_id, pm.model_name").
		Joins("JOIN accounts AS a ON a.id = pm.account_id").
		Joins("JOIN organization_provider_bindings AS b ON b.account_id = a.id").
		Where("b.organization_id IN ? AND b.status = ? AND pm.status = ? AND a.status = ? AND a.schedulable = ?", ids, "active", "active", "active", true).
		Where("(b.model_allowlist IS NULL OR JSON_TYPE(b.model_allowlist) = 'NULL' OR JSON_LENGTH(b.model_allowlist) = 0 OR JSON_CONTAINS(b.model_allowlist, JSON_QUOTE(pm.model_name)))").
		Where("pm.model_name <> '' AND pm.model_name <> '*'").
		Distinct().Order("b.organization_id ASC, pm.model_name ASC").Scan(&models).Error; err != nil {
		return err
	}
	for _, model := range models {
		organization := byID[model.OrganizationID]
		organization.AllowedModels = append(organization.AllowedModels, model.ModelName)
	}
	return nil
}

// Create stores initial organization policy and explicit bindings atomically.
func (repository *OrganizationRepository) Create(ctx context.Context, organization *domain.Organization) error {
	providers, err := json.Marshal(organization.AllowedProviderIDs)
	if err != nil {
		return err
	}
	models, err := json.Marshal(organization.AllowedModels)
	if err != nil {
		return err
	}
	record := OrganizationRecord{Name: organization.Name, Description: organization.Description, Status: organization.Status, AllowedProviderIDs: string(providers), AllowedModels: string(models)}
	var createdBindings []domain.ProviderBinding
	err = repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := edition.CheckCapacity(tx, "organizations", 1); err != nil {
			return err
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		if len(organization.AllowedProviderIDs) == 0 && len(organization.InitialBindings) == 0 {
			return nil
		}
		bindings := organization.InitialBindings
		if bindings == nil {
			var err error
			bindings, err = organizationPolicyBindings(tx, organization.AllowedProviderIDs, organization.AllowedModels, nil, true)
			if err != nil {
				return err
			}
		}
		createdBindings = bindings
		if err := replaceProviderBindingRecords(tx, record.ID, bindings); err != nil {
			return err
		}
		updates := map[string]any{}
		if err := setBindingPolicyUpdates(updates, bindings); err != nil {
			return err
		}
		return tx.Model(&OrganizationRecord{}).Where("id = ?", record.ID).Updates(updates).Error
	})
	if err != nil {
		return err
	}
	organization.ID, organization.CreatedAt, organization.UpdatedAt = record.ID, record.CreatedAt, record.UpdatedAt
	if len(createdBindings) > 0 {
		organization.AllowedProviderIDs, organization.AllowedModels = bindingPolicySummary(createdBindings)
	}
	organization.HasProviderBindings = len(createdBindings) > 0
	for _, binding := range createdBindings {
		if binding.Status == "active" {
			organization.HasActiveBindings = true
			break
		}
	}
	return nil
}

// Update synchronizes policy edits with bindings while preserving priority and
// per-channel policies on unchanged selections. Both writers lock the same row.
func (repository *OrganizationRepository) Update(ctx context.Context, id uint64, updates map[string]any) error {
	allowed := map[string]struct{}{"name": {}, "description": {}, "status": {}, "allowed_provider_ids": {}, "allowed_models": {}}
	values := make(map[string]any, len(updates))
	for field, value := range updates {
		if _, ok := allowed[field]; !ok {
			return gorm.ErrInvalidData
		}
		values[field] = value
	}
	_, providersChanged := updates["allowed_provider_ids"]
	_, modelsProvided := updates["allowed_models"]
	if !providersChanged && !modelsProvided {
		result := repository.database.WithContext(ctx).Model(&OrganizationRecord{}).Where("id = ?", id).Updates(values)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	}
	return repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record OrganizationRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, id).Error; err != nil {
			return err
		}
		original, err := organizationDomain(record)
		if err != nil {
			return err
		}
		var records []ProviderBindingRecord
		if err := tx.Where("organization_id = ?", id).Order("account_id ASC").Find(&records).Error; err != nil {
			return err
		}
		existing := make([]domain.ProviderBinding, 0, len(records))
		for _, item := range records {
			binding, err := providerBindingDomain(item)
			if err != nil {
				return err
			}
			existing = append(existing, binding)
		}
		currentItems := []domain.Organization{original}
		if err := (&OrganizationRepository{database: tx}).applyBindingPolicyRecords(ctx, currentItems, records); err != nil {
			return err
		}
		current := currentItems[0]
		providerIDs := slices.Clone(current.AllowedProviderIDs)
		modelNames := slices.Clone(original.AllowedModels)
		if providersChanged {
			raw, err := json.Marshal(updates["allowed_provider_ids"])
			if err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &providerIDs); err != nil {
				return err
			}
		}
		if modelsProvided {
			raw, err := json.Marshal(updates["allowed_models"])
			if err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &modelNames); err != nil {
				return err
			}
		}
		changeModels := modelsProvided && !sameModels(modelNames, current.AllowedModels)
		changeProviders := !sameProviderIDs(providerIDs, current.AllowedProviderIDs)
		bindings := existing
		if changeModels || changeProviders || len(records) == 0 && len(providerIDs) > 0 {
			bindings, err = organizationPolicyBindings(tx, providerIDs, modelNames, existing, changeModels)
			if err != nil {
				return err
			}
			if err := replaceProviderBindingRecords(tx, id, bindings); err != nil {
				return err
			}
		}
		if len(bindings) > 0 {
			if err := setBindingPolicyUpdates(values, bindings); err != nil {
				return err
			}
		} else {
			// An empty channel whitelist keeps the legacy "all channels" mode,
			// including any global model filter, rather than snapshotting providers.
			providersJSON, err := json.Marshal(providerIDs)
			if err != nil {
				return err
			}
			modelsJSON, err := json.Marshal(modelNames)
			if err != nil {
				return err
			}
			values["allowed_provider_ids"] = string(providersJSON)
			values["allowed_models"] = string(modelsJSON)
		}
		return tx.Model(&OrganizationRecord{}).Where("id = ?", id).Updates(values).Error
	})
}

func organizationDomain(record OrganizationRecord) (domain.Organization, error) {
	var providers []uint64
	var models []string
	if record.AllowedProviderIDs != "" {
		if err := json.Unmarshal([]byte(record.AllowedProviderIDs), &providers); err != nil {
			return domain.Organization{}, err
		}
	}
	if record.AllowedModels != "" {
		if err := json.Unmarshal([]byte(record.AllowedModels), &models); err != nil {
			return domain.Organization{}, err
		}
	}
	return domain.Organization{ID: record.ID, Name: record.Name, Description: record.Description, Status: record.Status, AllowedProviderIDs: providers, AllowedModels: models, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, nil
}
