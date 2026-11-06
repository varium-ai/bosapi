package application

import (
	"context"
	"fmt"
	"strings"

	"bosapi/backend/internal/admin/domain"
)

// AnnouncementService validates publication requests before reaching persistence.
type AnnouncementService struct{ repository domain.AnnouncementRepository }

// NewAnnouncementService shares the same announcement store across login types.
func NewAnnouncementService(repository domain.AnnouncementRepository) *AnnouncementService {
	return &AnnouncementService{repository: repository}
}

// List bounds pagination for the shared overview sidebar.
func (service *AnnouncementService) List(ctx context.Context, filter domain.AnnouncementFilter, page, size int) ([]domain.Announcement, int64, error) {
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return nil, 0, domain.ErrInvalidAnnouncement
	}
	return service.repository.List(ctx, filter, page, size)
}

// Create preserves Markdown source so all viewers render the published content consistently.
func (service *AnnouncementService) Create(ctx context.Context, organizationID uint64, title, body string) (domain.Announcement, error) {
	item := domain.Announcement{OrganizationID: organizationID, Title: strings.TrimSpace(title), Body: body}
	if err := item.Validate(); err != nil {
		return domain.Announcement{}, err
	}
	if err := service.repository.Create(ctx, &item); err != nil {
		return domain.Announcement{}, fmt.Errorf("create announcement: %w", err)
	}
	return item, nil
}

// Delete rejects invalid identifiers before attempting to remove a publication.
func (service *AnnouncementService) Delete(ctx context.Context, organizationID, id uint64) error {
	if id == 0 {
		return domain.ErrInvalidAnnouncement
	}
	return service.repository.Delete(ctx, organizationID, id)
}

// Update preserves publication identity and Markdown formatting when correcting content.
func (service *AnnouncementService) Update(ctx context.Context, item domain.Announcement) (domain.Announcement, error) {
	item.Title = strings.TrimSpace(item.Title)
	if item.ID == 0 {
		return domain.Announcement{}, domain.ErrInvalidAnnouncement
	}
	if err := item.Validate(); err != nil {
		return domain.Announcement{}, err
	}
	if err := service.repository.Update(ctx, &item); err != nil {
		return domain.Announcement{}, fmt.Errorf("update announcement: %w", err)
	}
	return item, nil
}
