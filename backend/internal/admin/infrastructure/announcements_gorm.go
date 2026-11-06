package infrastructure

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bosapi/backend/internal/admin/domain"
)

// AnnouncementRepository enforces publication ownership in every storage query.
type AnnouncementRepository struct{ database *gorm.DB }

// NewAnnouncementRepository binds publications to the shared database lifecycle.
func NewAnnouncementRepository(database *gorm.DB) *AnnouncementRepository {
	return &AnnouncementRepository{database: database}
}

// List returns newest publications first, including a stable order for equal timestamps.
func (repository *AnnouncementRepository) List(ctx context.Context, filter domain.AnnouncementFilter, page, size int) ([]domain.Announcement, int64, error) {
	query := repository.database.WithContext(ctx).Model(&domain.Announcement{})
	if filter.IncludeGlobal && filter.OrganizationID != 0 {
		query = query.Where("organization_id IN ?", []uint64{0, filter.OrganizationID})
	} else {
		query = query.Where("organization_id = ?", filter.OrganizationID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count announcements: %w", err)
	}
	items := make([]domain.Announcement, 0)
	if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list announcements: %w", err)
	}
	return items, total, nil
}

// Create uses bound parameters for both plain titles and Markdown source.
func (repository *AnnouncementRepository) Create(ctx context.Context, item *domain.Announcement) error {
	return repository.database.WithContext(ctx).Create(item).Error
}

// Delete reports absent publications so stale management actions can be retried accurately.
func (repository *AnnouncementRepository) Delete(ctx context.Context, organizationID, id uint64) error {
	result := repository.database.WithContext(ctx).Where("organization_id = ?", organizationID).Delete(&domain.Announcement{}, id)
	if result.Error != nil {
		return fmt.Errorf("delete announcement: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return domain.ErrAnnouncementNotFound
	}
	return nil
}

// Update locks the publication so unchanged edits succeed while missing publications remain distinguishable.
func (repository *AnnouncementRepository) Update(ctx context.Context, item *domain.Announcement) error {
	return repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing domain.Announcement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("organization_id = ?", item.OrganizationID).First(&existing, item.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrAnnouncementNotFound
			}
			return fmt.Errorf("load announcement: %w", err)
		}
		if err := tx.Model(&existing).Where("organization_id = ?", item.OrganizationID).Select("title", "body").Updates(item).Error; err != nil {
			return fmt.Errorf("update announcement: %w", err)
		}
		item.CreatedAt = existing.CreatedAt
		return nil
	})
}
