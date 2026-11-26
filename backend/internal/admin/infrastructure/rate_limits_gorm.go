package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"bosapi/backend/internal/admin/domain"
	userinfra "bosapi/backend/internal/user/infrastructure"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type settingRecord struct {
	SettingKey       string  `gorm:"primaryKey;size:128"`
	ValueText        *string `gorm:"type:text"`
	SecretCiphertext []byte
	IsSecret         bool
	UpdatedBy        *uint64
	UpdatedAt        time.Time
}

func (settingRecord) TableName() string { return "settings" }

// RateLimitRepository stores JSON rate limit configurations in settings.
type RateLimitRepository struct{ database *gorm.DB }

// NewRateLimitRepository constructs the Gorm rate limit repository.
func NewRateLimitRepository(database *gorm.DB) *RateLimitRepository {
	return &RateLimitRepository{database: database}
}

// ReadRateLimits returns global and optionally scoped configuration values.
func (repository *RateLimitRepository) ReadRateLimits(ctx context.Context, organizationID, keyID uint64) (domain.RateLimitUpdate, error) {
	result := domain.RateLimitUpdate{}
	if err := repository.readSetting(ctx, "rate_limit.global", &result.Global); err != nil {
		return result, err
	}
	if organizationID > 0 {
		var value domain.OrganizationRateLimit
		if err := repository.readSetting(ctx, fmt.Sprintf("rate_limit.organization.%d", organizationID), &value); err != nil {
			return result, err
		}
		value.OrganizationID = organizationID
		result.Organizations = append(result.Organizations, value)
	}
	if keyID > 0 {
		if organizationID > 0 {
			var count int64
			if err := repository.database.WithContext(ctx).Model(&userinfra.APIKeyRecord{}).Where("id = ? AND organization_id = ? AND status <> ?", keyID, organizationID, "deleted").Count(&count).Error; err != nil {
				return result, err
			}
			if count == 0 {
				return result, gorm.ErrRecordNotFound
			}
		}
		var config domain.RateLimitConfig
		var record userinfra.APIKeyRecord
		query := repository.database.WithContext(ctx).Select("quota_reset_start_at").Where("id = ? AND status <> ?", keyID, "deleted")
		if organizationID > 0 {
			query = query.Where("organization_id = ?", organizationID)
		}
		if err := query.First(&record).Error; err != nil {
			return result, err
		}
		config.QuotaResetStartAt = record.QuotaResetStartAt
		if err := repository.readSetting(ctx, fmt.Sprintf("rate_limit.key.%d", keyID), &config); err != nil {
			return result, err
		}
		config.QuotaResetStartAt = record.QuotaResetStartAt
		result.Keys = append(result.Keys, domain.KeyRateLimit{KeyID: keyID, RateLimitConfig: config})
	}
	return result, nil
}

// WriteRateLimits atomically upserts configuration and verifies referenced resources.
func (repository *RateLimitRepository) WriteRateLimits(ctx context.Context, update domain.RateLimitUpdate, updatedBy uint64) error {
	return repository.writeRateLimits(ctx, update, updatedBy, 0)
}

// WriteRateLimitsForOrganization verifies every key belongs to the caller's organization.
func (repository *RateLimitRepository) WriteRateLimitsForOrganization(ctx context.Context, update domain.RateLimitUpdate, organizationID, updatedBy uint64) error {
	if organizationID == 0 || update.Global != nil || len(update.Organizations) != 0 || len(update.Keys) == 0 {
		return gorm.ErrRecordNotFound
	}
	return repository.writeRateLimits(ctx, update, updatedBy, organizationID)
}

func (repository *RateLimitRepository) writeRateLimits(ctx context.Context, update domain.RateLimitUpdate, updatedBy, organizationID uint64) error {
	return repository.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		for _, value := range update.Organizations {
			var count int64
			if err := transaction.Model(&OrganizationRecord{}).Where("id = ?", value.OrganizationID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return gorm.ErrRecordNotFound
			}
		}
		for _, value := range update.Keys {
			var count int64
			query := transaction.Model(&userinfra.APIKeyRecord{}).Where("id = ? AND status <> ?", value.KeyID, "deleted")
			if organizationID > 0 {
				query = query.Where("organization_id = ?", organizationID)
			}
			if err := query.Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return gorm.ErrRecordNotFound
			}
		}
		if update.Global != nil {
			if err := writeSetting(transaction, "rate_limit.global", update.Global, updatedBy); err != nil {
				return err
			}
		}
		for _, value := range update.Organizations {
			if err := writeSetting(transaction, fmt.Sprintf("rate_limit.organization.%d", value.OrganizationID), value, updatedBy); err != nil {
				return err
			}
		}
		for _, value := range update.Keys {
			if err := writeSetting(transaction, fmt.Sprintf("rate_limit.key.%d", value.KeyID), value.RateLimitConfig, updatedBy); err != nil {
				return err
			}
		}
		return nil
	})
}

func (repository *RateLimitRepository) readSetting(ctx context.Context, key string, target any) error {
	var records []settingRecord
	if err := repository.database.WithContext(ctx).Where("setting_key = ?", key).Limit(1).Find(&records).Error; err != nil {
		return err
	}
	if len(records) == 0 || records[0].ValueText == nil {
		return nil
	}
	return json.Unmarshal([]byte(*records[0].ValueText), target)
}

func writeSetting(transaction *gorm.DB, key string, value any, updatedBy uint64) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	text := string(encoded)
	record := settingRecord{SettingKey: key, ValueText: &text, UpdatedBy: &updatedBy, UpdatedAt: time.Now().UTC()}
	return transaction.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "setting_key"}}, DoUpdates: clause.AssignmentColumns([]string{"value_text", "updated_by", "updated_at"})}).Create(&record).Error
}

var _ domain.RateLimitRepository = (*RateLimitRepository)(nil)
