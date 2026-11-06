package infrastructure

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bosapi/backend/internal/admin/domain"
)

type keyTemplateRecord struct {
	ID        uint64
	CreatedBy uint64
	Name      string
	Settings  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TableName keeps private template configuration separate from generated credentials.
func (keyTemplateRecord) TableName() string { return "api_key_templates" }

// KeyTemplateRepository applies creator predicates to all template reads and mutations.
type KeyTemplateRepository struct{ database *gorm.DB }

// NewKeyTemplateRepository binds template persistence to the shared database lifecycle.
func NewKeyTemplateRepository(database *gorm.DB) *KeyTemplateRepository {
	return &KeyTemplateRepository{database: database}
}

// List paginates only the authenticated creator's records.
func (repository *KeyTemplateRepository) List(ctx context.Context, creator uint64, page, size int) ([]domain.KeyTemplate, int64, error) {
	query := keyTemplateScope(repository.database.WithContext(ctx).Model(&keyTemplateRecord{}), creator)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []keyTemplateRecord
	if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	items := make([]domain.KeyTemplate, 0, len(records))
	for _, record := range records {
		item := domain.KeyTemplate{ID: record.ID, CreatedBy: record.CreatedBy, Name: record.Name, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
		if err := json.Unmarshal([]byte(record.Settings), &item.Settings); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, nil
}

// Save verifies creator access and the configured organization before persisting.
func (repository *KeyTemplateRepository) Save(ctx context.Context, item *domain.KeyTemplate) error {
	return repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record keyTemplateRecord
		if item.ID != 0 {
			if err := keyTemplateScope(tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", item.ID), item.CreatedBy).First(&record).Error; err != nil {
				return err
			}
		}
		var organization struct{ ID uint64 }
		if err := tx.Table("organizations").Where("id = ? AND status = ?", item.Settings.OrganizationID, "active").Take(&organization).Error; err != nil {
			return err
		}
		encoded, err := json.Marshal(item.Settings)
		if err != nil {
			return err
		}
		record.Name, record.Settings = item.Name, string(encoded)
		if item.ID == 0 {
			record.CreatedBy = item.CreatedBy
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Model(&keyTemplateRecord{}).Where("id = ? AND created_by = ?", item.ID, item.CreatedBy).Select("name", "settings", "updated_at").Updates(&record).Error; err != nil {
				return err
			}
		}
		item.ID, item.CreatedAt, item.UpdatedAt = record.ID, record.CreatedAt, record.UpdatedAt
		return nil
	})
}

// Delete cannot remove another user's template, including when the caller is a super administrator.
func (repository *KeyTemplateRepository) Delete(ctx context.Context, creator, id uint64) error {
	result := keyTemplateScope(repository.database.WithContext(ctx).Where("id = ?", id), creator).Delete(&keyTemplateRecord{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Moving an administrator does not expose templates for their former organization.
func keyTemplateScope(query *gorm.DB, creator uint64) *gorm.DB {
	return query.Where("created_by = ?", creator).Where("EXISTS (SELECT 1 FROM users AS creator WHERE creator.id = ? AND (creator.role = ? OR (creator.organization_id > 0 AND JSON_UNQUOTE(JSON_EXTRACT(api_key_templates.settings, '$.organization_id')) = CAST(creator.organization_id AS CHAR))))", creator, "super_admin")
}
