package infrastructure

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"bosapi/backend/internal/admin/domain"
)

func TestAnnouncementRepositoryPersistsMarkdownWithBoundParameters(t *testing.T) {
	for _, organizationID := range []uint64{0, 7} {
		db, mock := newOrganizationSyncDatabase(t)
		item := domain.Announcement{OrganizationID: organizationID, Title: "'; DROP TABLE announcements; --", Body: "# Release\n\n**Bold**"}
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO `announcements` ").WithArgs(item.OrganizationID, item.Title, item.Body, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(7, 1))
		mock.ExpectCommit()
		if err := NewAnnouncementRepository(db).Create(context.Background(), &item); err != nil || item.ID != 7 || item.CreatedAt.IsZero() {
			t.Fatalf("unexpected create: %+v %v", item, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnnouncementRepositoryFiltersBeforeCountingAndPagination(t *testing.T) {
	for _, test := range []struct {
		name   string
		filter domain.AnnouncementFilter
	}{
		{"global_reader", domain.AnnouncementFilter{IncludeGlobal: true}},
		{"global_manager", domain.AnnouncementFilter{}},
		{"organization_reader", domain.AnnouncementFilter{OrganizationID: 7, IncludeGlobal: true}},
		{"organization_manager", domain.AnnouncementFilter{OrganizationID: 7}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock := newOrganizationSyncDatabase(t)
			if test.filter.IncludeGlobal && test.filter.OrganizationID != 0 {
				mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `announcements` WHERE organization_id IN (?,?)")).WithArgs(uint64(0), uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(6))
				mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `announcements` WHERE organization_id IN (?,?) ORDER BY id DESC LIMIT ? OFFSET ?")).WithArgs(uint64(0), uint64(7), 5, 5).WillReturnRows(announcementRows().AddRow(1, 7, "Title", "# Body", time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)))
			} else {
				mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `announcements` WHERE organization_id = ?")).WithArgs(test.filter.OrganizationID).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(6))
				mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `announcements` WHERE organization_id = ? ORDER BY id DESC LIMIT ? OFFSET ?")).WithArgs(test.filter.OrganizationID, 5, 5).WillReturnRows(announcementRows().AddRow(1, test.filter.OrganizationID, "Title", "# Body", time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)))
			}
			items, total, err := NewAnnouncementRepository(db).List(context.Background(), test.filter, 2, 5)
			if err != nil || total != 6 || len(items) != 1 || items[0].Body != "# Body" {
				t.Fatalf("unexpected list: %+v total=%d error=%v", items, total, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func announcementRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "organization_id", "title", "body", "created_at"})
}

func TestAnnouncementRepositoryListFailures(t *testing.T) {
	for _, stage := range []string{"count", "list", "empty"} {
		t.Run(stage, func(t *testing.T) {
			db, mock := newOrganizationSyncDatabase(t)
			count := mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `announcements` WHERE organization_id = ?")).WithArgs(uint64(7))
			if stage == "count" {
				count.WillReturnError(context.DeadlineExceeded)
			} else {
				count.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				query := mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `announcements` WHERE organization_id = ? ORDER BY id DESC LIMIT ?")).WithArgs(uint64(7), 5)
				if stage == "list" {
					query.WillReturnError(context.DeadlineExceeded)
				} else {
					query.WillReturnRows(announcementRows())
				}
			}
			items, _, err := NewAnnouncementRepository(db).List(context.Background(), domain.AnnouncementFilter{OrganizationID: 7}, 1, 5)
			if stage == "empty" {
				if err != nil || items == nil || len(items) != 0 {
					t.Fatalf("expected a non-nil empty list: %+v %v", items, err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) || items != nil {
				t.Fatalf("failure returned partial content: %+v %v", items, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAnnouncementRepositoryDeletion(t *testing.T) {
	for _, organizationID := range []uint64{0, 7, 99} {
		for _, stage := range []string{"deleted", "missing_or_foreign", "failure"} {
			t.Run(stage, func(t *testing.T) {
				db, mock := newOrganizationSyncDatabase(t)
				mock.ExpectBegin()
				statement := mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `announcements` WHERE organization_id = ? AND `announcements`.`id` = ?")).WithArgs(organizationID, uint64(7))
				var expected error
				switch stage {
				case "failure":
					expected = context.Canceled
					statement.WillReturnError(expected)
					mock.ExpectRollback()
				case "missing_or_foreign":
					expected = domain.ErrAnnouncementNotFound
					statement.WillReturnResult(sqlmock.NewResult(0, 0))
					mock.ExpectCommit()
				default:
					statement.WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit()
				}
				if err := NewAnnouncementRepository(db).Delete(context.Background(), organizationID, 7); !errors.Is(err, expected) {
					t.Fatalf("got %v, want %v", err, expected)
				}
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestAnnouncementRepositoryUpdate(t *testing.T) {
	published := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	for _, organizationID := range []uint64{0, 7, 99} {
		for _, stage := range []string{"updated", "unchanged", "missing_or_foreign", "read_failure", "write_failure"} {
			t.Run(stage, func(t *testing.T) {
				db, mock := newOrganizationSyncDatabase(t)
				item := domain.Announcement{ID: 7, OrganizationID: organizationID, Title: "'; DROP TABLE announcements; --", Body: "# Revised\n"}
				mock.ExpectBegin()
				query := mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `announcements` WHERE organization_id = ? AND `announcements`.`id` = ? ORDER BY `announcements`.`id` LIMIT ? FOR UPDATE")).WithArgs(organizationID, uint64(7), 1)
				var expected error
				switch stage {
				case "read_failure":
					expected = context.Canceled
					query.WillReturnError(expected)
				case "missing_or_foreign":
					expected = domain.ErrAnnouncementNotFound
					query.WillReturnRows(announcementRows())
				default:
					query.WillReturnRows(announcementRows().AddRow(7, organizationID, "Original", "Body", published))
					update := mock.ExpectExec(regexp.QuoteMeta("UPDATE `announcements` SET `title`=?,`body`=? WHERE organization_id = ? AND `id` = ?")).WithArgs(item.Title, item.Body, organizationID, uint64(7))
					if stage == "write_failure" {
						expected = context.Canceled
						update.WillReturnError(expected)
					} else {
						rows := int64(1)
						if stage == "unchanged" {
							rows = 0
						}
						update.WillReturnResult(sqlmock.NewResult(0, rows))
					}
				}
				if expected != nil {
					mock.ExpectRollback()
				} else {
					mock.ExpectCommit()
				}
				if err := NewAnnouncementRepository(db).Update(context.Background(), &item); !errors.Is(err, expected) {
					t.Fatalf("got %v, want %v", err, expected)
				}
				if expected == nil && (!item.CreatedAt.Equal(published) || item.OrganizationID != organizationID) {
					t.Fatalf("publication metadata changed: %+v", item)
				}
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
