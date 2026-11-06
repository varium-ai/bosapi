package infrastructure

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"bosapi/backend/internal/admin/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProviderRecord maps encrypted upstream account configuration to Gorm.
type ProviderRecord struct {
	ID                    uint64 `gorm:"primaryKey"`
	Name                  string `gorm:"size:128;not null"`
	Platform              string `gorm:"size:32;not null;index:idx_account_platform_status"`
	AccountType           string `gorm:"size:32;not null"`
	APIFormat             string `gorm:"size:32;not null;default:openai_chat"`
	BaseURL               string `gorm:"size:512;not null"`
	CredentialsCiphertext []byte `gorm:"not null"`
	Status                string `gorm:"size:24;not null;default:active;index:idx_account_platform_status"`
	Priority              int    `gorm:"not null;default:0"`
	ConcurrencyLimit      *uint32
	Schedulable           bool `gorm:"not null;default:true"`
	RateLimitRPM          *uint32
	CapabilitiesJSON      string  `gorm:"type:json"`
	PricingJSON           string  `gorm:"type:json"`
	ModelMappingJSON      string  `gorm:"type:json"`
	CreatedByUserID       *uint64 `gorm:"index:idx_accounts_visibility"`
	CreatedAt             time.Time
	VerifiedAt            *time.Time
	UpdatedAt             time.Time
}

// TableName maps provider records to the accounts table.
func (ProviderRecord) TableName() string { return "accounts" }

// ProviderRepository persists provider metadata and encrypted credentials.
type ProviderRepository struct{ database *gorm.DB }

// NewProviderRepository constructs a provider repository backed by Gorm.
func NewProviderRepository(database *gorm.DB) *ProviderRepository {
	return &ProviderRepository{database: database}
}

// ListProviders returns a filtered page without credential ciphertext.
func (repository *ProviderRepository) ListProviders(ctx context.Context, filter domain.ProviderFilter, page, pageSize int) ([]domain.Provider, int64, error) {
	query := repository.database.WithContext(ctx).Model(&ProviderRecord{})
	query = applyProviderVisibility(query, filter.Actor)
	if filter.OrganizationID > 0 {
		query = applyProviderVisibility(query, domain.ProviderActor{Role: "org_admin", UserID: 1, OrganizationID: filter.OrganizationID})
	}
	if filter.Platform != "" {
		query = query.Where("platform = ?", filter.Platform)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Search != "" {
		query = query.Where("name LIKE ?", "%"+strings.ReplaceAll(filter.Search, "%", "\\%")+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []ProviderRecord
	if err := query.Order("id ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	providers := make([]domain.Provider, 0, len(records))
	for _, record := range records {
		item, err := providerDomain(record)
		if err != nil {
			return nil, 0, err
		}
		providers = append(providers, item)
	}
	return providers, total, nil
}

// AuthorizeProvider returns not found for providers outside the actor's visibility boundary.
func (repository *ProviderRepository) AuthorizeProvider(ctx context.Context, id uint64, actor domain.ProviderActor) error {
	var count int64
	query := repository.database.WithContext(ctx).Model(&ProviderRecord{}).Where("accounts.id = ?", id)
	if err := applyProviderVisibility(query, actor).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func applyProviderVisibility(query *gorm.DB, actor domain.ProviderActor) *gorm.DB {
	switch actor.Role {
	case "super_admin":
		return query
	case "org_admin":
		if actor.UserID == 0 || actor.OrganizationID == 0 {
			return query.Where("1 = 0")
		}
		return query.Where(
			"EXISTS (SELECT 1 FROM organization_provider_bindings opb WHERE opb.account_id = accounts.id AND opb.organization_id = ?)",
			actor.OrganizationID,
		)
	default:
		return query.Where("1 = 0")
	}
}

// FindProvider returns safe provider metadata by identifier.
func (repository *ProviderRepository) FindProvider(ctx context.Context, id uint64) (domain.Provider, error) {
	var record ProviderRecord
	if err := repository.database.WithContext(ctx).First(&record, id).Error; err != nil {
		return domain.Provider{}, err
	}
	return providerDomain(record)
}

// ProviderCredential returns safe metadata and encrypted credential bytes for internal adapter calls.
func (repository *ProviderRepository) ProviderCredential(ctx context.Context, id uint64) (domain.Provider, string, error) {
	var record ProviderRecord
	if err := repository.database.WithContext(ctx).First(&record, id).Error; err != nil {
		return domain.Provider{}, "", err
	}
	provider, err := providerDomain(record)
	return provider, string(record.CredentialsCiphertext), err
}

// UpdateCredentialLocked serializes token rotation with a database row lock.
func (repository *ProviderRepository) UpdateCredentialLocked(ctx context.Context, id uint64, update func(domain.Provider, string) (string, error)) (provider domain.Provider, ciphertext string, err error) {
	err = repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record ProviderRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, id).Error; err != nil {
			return err
		}
		var err error
		provider, err = providerDomain(record)
		if err != nil {
			return err
		}
		ciphertext, err = update(provider, string(record.CredentialsCiphertext))
		if err != nil {
			return err
		}
		if ciphertext == string(record.CredentialsCiphertext) {
			return nil
		}
		return tx.Model(&record).Update("credentials_ciphertext", []byte(ciphertext)).Error
	})
	return
}

// CreateProvider stores a provider with encrypted credential material.
func (repository *ProviderRepository) CreateProvider(ctx context.Context, provider domain.Provider, ciphertext string) (domain.Provider, error) {
	capabilities, err := json.Marshal(provider.Capabilities)
	if err != nil {
		return domain.Provider{}, err
	}
	pricing, err := json.Marshal(provider.Pricing)
	if err != nil {
		return domain.Provider{}, err
	}
	var modelMappingJSON string
	if provider.ModelMapping != nil {
		encoded, err := json.Marshal(provider.ModelMapping)
		if err != nil {
			return domain.Provider{}, err
		}
		modelMappingJSON = string(encoded)
	}
	record := ProviderRecord{Name: provider.Name, Platform: provider.Platform, AccountType: provider.AccountType, APIFormat: provider.APIFormat, BaseURL: provider.BaseURL, CredentialsCiphertext: []byte(ciphertext), Status: provider.Status, Priority: provider.Priority, ConcurrencyLimit: provider.ConcurrencyLimit, Schedulable: provider.Schedulable, RateLimitRPM: provider.RateLimitRPM, CapabilitiesJSON: string(capabilities), PricingJSON: string(pricing), ModelMappingJSON: modelMappingJSON, CreatedByUserID: provider.CreatedByUserID}
	if err := repository.database.WithContext(ctx).Create(&record).Error; err != nil {
		return domain.Provider{}, err
	}
	return providerDomain(record)
}

// CreateProviderWithModels makes a newly authorized account and its catalog
// visible together, so model-write failures cannot leave an unusable account.
func (repository *ProviderRepository) CreateProviderWithModels(ctx context.Context, provider domain.Provider, ciphertext string, models []domain.ProviderModel) (created domain.Provider, err error) {
	err = repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		created, err = NewProviderRepository(tx).CreateProvider(ctx, provider, ciphertext)
		if err != nil {
			return err
		}
		for index := range models {
			models[index].ProviderID = created.ID
		}
		return NewProviderModelRepository(tx).UpsertProviderModels(ctx, created.ID, models)
	})
	return
}

// UpdateProviderWithModels retains the previous account and catalog if either
// part of a subscription edit or reauthorization fails.
func (repository *ProviderRepository) UpdateProviderWithModels(ctx context.Context, id uint64, updates map[string]any, ciphertext string, models []domain.ProviderModel) (updated domain.Provider, err error) {
	err = repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		updated, err = NewProviderRepository(tx).UpdateProvider(ctx, id, updates, ciphertext)
		if err != nil {
			return err
		}
		return NewProviderModelRepository(tx).UpsertProviderModels(ctx, id, models)
	})
	return
}

// UpdateProvider applies only allowlisted metadata and optional encrypted credentials.
func (repository *ProviderRepository) UpdateProvider(ctx context.Context, id uint64, updates map[string]any, ciphertext string) (domain.Provider, error) {
	if ciphertext != "" {
		updates["credentials_ciphertext"] = []byte(ciphertext)
	}
	for _, field := range []string{"capabilities_json", "pricing_json", "model_mapping_json"} {
		if value, ok := updates[field]; ok {
			encoded, err := json.Marshal(value)
			if err != nil {
				return domain.Provider{}, err
			}
			updates[field] = string(encoded)
		}
	}
	result := repository.database.WithContext(ctx).Model(&ProviderRecord{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return domain.Provider{}, result.Error
	}
	if result.RowsAffected == 0 {
		return domain.Provider{}, gorm.ErrRecordNotFound
	}
	return repository.FindProvider(ctx, id)
}

// DisableProvider disables an account while retaining associated history.
func (repository *ProviderRepository) DisableProvider(ctx context.Context, id uint64) error {
	result := repository.database.WithContext(ctx).Model(&ProviderRecord{}).Where("id = ?", id).Updates(map[string]any{"status": "disabled", "schedulable": false})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DeleteProvider permanently deletes a provider account and cleans up associated models and bindings.
func (repository *ProviderRepository) DeleteProvider(ctx context.Context, id uint64) error {
	return repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Clean up discovered models
		if err := tx.Where("account_id = ?", id).Delete(&providerModelRecord{}).Error; err != nil {
			return err
		}
		// Clean up organization provider bindings
		if err := tx.Where("account_id = ?", id).Delete(&ProviderBindingRecord{}).Error; err != nil {
			return err
		}
		// Delete account
		result := tx.Where("id = ?", id).Delete(&ProviderRecord{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// SetStatus updates provider status and schedulable flag.
func (repository *ProviderRepository) SetStatus(ctx context.Context, id uint64, status string) error {
	schedulable := status == "active"
	result := repository.database.WithContext(ctx).Model(&ProviderRecord{}).Where("id = ?", id).Updates(map[string]any{
		"status":      status,
		"schedulable": schedulable,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func providerDomain(record ProviderRecord) (domain.Provider, error) {
	provider := domain.Provider{ID: record.ID, Name: record.Name, Platform: record.Platform, AccountType: record.AccountType, APIFormat: record.APIFormat, BaseURL: record.BaseURL, Status: record.Status, Priority: record.Priority, ConcurrencyLimit: record.ConcurrencyLimit, Schedulable: record.Schedulable, RateLimitRPM: record.RateLimitRPM, CreatedByUserID: record.CreatedByUserID, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, VerifiedAt: record.VerifiedAt}
	if record.CapabilitiesJSON != "" {
		if err := json.Unmarshal([]byte(record.CapabilitiesJSON), &provider.Capabilities); err != nil {
			return domain.Provider{}, err
		}
	}
	if record.PricingJSON != "" {
		if err := json.Unmarshal([]byte(record.PricingJSON), &provider.Pricing); err != nil {
			return domain.Provider{}, err
		}
	}
	if record.ModelMappingJSON != "" {
		if err := json.Unmarshal([]byte(record.ModelMappingJSON), &provider.ModelMapping); err != nil {
			return domain.Provider{}, err
		}
	}
	return provider, nil
}

var _ domain.ProviderRepository = (*ProviderRepository)(nil)

// RecordProviderVerification never approves configuration changed during a test.
// UpdateColumn deliberately preserves the configuration modification timestamp.
func (repository *ProviderRepository) RecordProviderVerification(ctx context.Context, id uint64, version time.Time, passed bool) error {
	var verified any
	if passed {
		verified = time.Now().UTC()
	}
	return repository.database.WithContext(ctx).Model(&ProviderRecord{}).
		Where("id = ? AND updated_at = ?", id, version).UpdateColumn("verified_at", verified).Error
}
