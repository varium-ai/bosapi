package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
)

type announcementTestRepository struct {
	items          []domain.Announcement
	err            error
	calls          int
	filter         domain.AnnouncementFilter
	organizationID uint64
}

func (repository *announcementTestRepository) List(_ context.Context, filter domain.AnnouncementFilter, _, _ int) ([]domain.Announcement, int64, error) {
	repository.calls++
	repository.filter = filter
	return repository.items, int64(len(repository.items)), repository.err
}

func (repository *announcementTestRepository) Create(_ context.Context, item *domain.Announcement) error {
	repository.calls++
	item.ID = 7
	repository.items = append(repository.items, *item)
	return repository.err
}

func (repository *announcementTestRepository) Update(_ context.Context, item *domain.Announcement) error {
	repository.calls++
	item.CreatedAt = time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	repository.items = []domain.Announcement{*item}
	return repository.err
}

func (repository *announcementTestRepository) Delete(_ context.Context, organizationID, _ uint64) error {
	repository.calls++
	repository.organizationID = organizationID
	return repository.err
}

func TestAnnouncementPublicationPreservesMarkdown(t *testing.T) {
	repository := &announcementTestRepository{}
	service := NewAnnouncementService(repository)
	body := "\n# Update\n\n    indented code\n"
	item, err := service.Create(context.Background(), 0, "  Release  ", body)
	if err != nil || item.ID != 7 || item.Title != "Release" || item.Body != body {
		t.Fatalf("unexpected publication: %+v %v", item, err)
	}
	items, total, err := service.List(context.Background(), domain.AnnouncementFilter{}, 1, 5)
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("unexpected list: %+v %v", items, err)
	}
	if err := service.Delete(context.Background(), 0, 7); err != nil {
		t.Fatal(err)
	}
}

func TestAnnouncementServiceRejectsInvalidRequestsBeforeStorage(t *testing.T) {
	repository := &announcementTestRepository{}
	service := NewAnnouncementService(repository)
	if _, err := service.Create(context.Background(), 0, " ", "body"); !errors.Is(err, domain.ErrInvalidAnnouncement) {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), 0, 0); !errors.Is(err, domain.ErrInvalidAnnouncement) {
		t.Fatal(err)
	}
	for _, values := range [][2]int{{0, 5}, {-1, 5}, {1000001, 5}, {1, 0}, {1, 101}} {
		if _, _, err := service.List(context.Background(), domain.AnnouncementFilter{}, values[0], values[1]); !errors.Is(err, domain.ErrInvalidAnnouncement) {
			t.Fatalf("invalid pagination accepted: %v %v", values, err)
		}
	}
	if repository.calls != 0 {
		t.Fatal("invalid input reached persistence")
	}
}

func TestAnnouncementServicePreservesStorageFailures(t *testing.T) {
	repository := &announcementTestRepository{err: context.Canceled}
	service := NewAnnouncementService(repository)
	if _, err := service.Create(context.Background(), 0, "Title", "Body"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := service.List(context.Background(), domain.AnnouncementFilter{}, 1, 100); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAnnouncementServiceUpdate(t *testing.T) {
	for _, test := range []struct {
		name              string
		id                uint64
		title, body       string
		failure, expected error
	}{
		{name: "valid", id: 7, title: "  Revised  ", body: "\n# Updated\n"},
		{name: "zero_id", title: "Title", body: "Body", expected: domain.ErrInvalidAnnouncement},
		{name: "blank_title", id: 7, title: " ", body: "Body", expected: domain.ErrInvalidAnnouncement},
		{name: "blank_body", id: 7, title: "Title", body: " ", expected: domain.ErrInvalidAnnouncement},
		{name: "missing", id: 7, title: "Title", body: "Body", failure: domain.ErrAnnouncementNotFound, expected: domain.ErrAnnouncementNotFound},
		{name: "canceled", id: 7, title: "Title", body: "Body", failure: context.Canceled, expected: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &announcementTestRepository{err: test.failure}
			item, err := NewAnnouncementService(repository).Update(context.Background(), domain.Announcement{ID: test.id, OrganizationID: 2, Title: test.title, Body: test.body})
			if !errors.Is(err, test.expected) {
				t.Fatalf("got %v, want %v", err, test.expected)
			}
			if errors.Is(test.expected, domain.ErrInvalidAnnouncement) && repository.calls != 0 {
				t.Fatal("invalid edit reached persistence")
			}
			if test.expected == nil && (item.OrganizationID != 2 || item.ID != 7 || item.Title != "Revised" || item.Body != test.body || item.CreatedAt.IsZero()) {
				t.Fatalf("unexpected edited publication: %+v", item)
			}
		})
	}
}

func TestAnnouncementServicePreservesOrganizationOwnership(t *testing.T) {
	repository := &announcementTestRepository{}
	service := NewAnnouncementService(repository)
	item, err := service.Create(context.Background(), 23, "Title", "Body")
	if err != nil || item.OrganizationID != 23 || repository.items[0].OrganizationID != 23 {
		t.Fatalf("organization publication became global: %+v %v", item, err)
	}
	filter := domain.AnnouncementFilter{OrganizationID: 23, IncludeGlobal: true}
	if _, _, err := service.List(context.Background(), filter, 1, 5); err != nil || repository.filter != filter {
		t.Fatalf("readership scope was lost: %+v %v", repository.filter, err)
	}
	if err := service.Delete(context.Background(), 23, 7); err != nil || repository.organizationID != 23 {
		t.Fatalf("deletion scope was lost: %d %v", repository.organizationID, err)
	}
}
