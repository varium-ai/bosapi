package domain

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrInvalidAnnouncement prevents malformed or unbounded content from being published.
var ErrInvalidAnnouncement = errors.New("invalid announcement")

// ErrAnnouncementNotFound distinguishes a missing announcement from a storage failure.
var ErrAnnouncementNotFound = errors.New("announcement not found")

// Announcement is a Markdown publication scoped globally or to one organization.
type Announcement struct {
	OrganizationID uint64    `json:"organization_id"`
	ID             uint64    `json:"id"`
	Title          string    `json:"title"`
	Body           string    `json:"body"`
	CreatedAt      time.Time `json:"created_at"`
}

// Validate bounds published content while preserving the author's Markdown formatting.
func (item Announcement) Validate() error {
	if !utf8.ValidString(item.Title) || !utf8.ValidString(item.Body) ||
		strings.TrimSpace(item.Title) == "" || utf8.RuneCountInString(item.Title) > 200 ||
		strings.TrimSpace(item.Body) == "" || len(item.Body) > 65536 {
		return ErrInvalidAnnouncement
	}
	return nil
}

// AnnouncementFilter separates overview readership from publications a manager owns.
type AnnouncementFilter struct {
	OrganizationID uint64
	IncludeGlobal  bool
}

// AnnouncementRepository keeps scoped announcement persistence independent of transport.
type AnnouncementRepository interface {
	List(context.Context, AnnouncementFilter, int, int) ([]Announcement, int64, error)
	Create(context.Context, *Announcement) error
	Update(context.Context, *Announcement) error
	Delete(context.Context, uint64, uint64) error
}
