package infrastructure

import (
	"context"
	"errors"
	"testing"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/edition"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestOrganizationCreationEnforcesCommunityCapacity(t *testing.T) {
	for _, test := range []struct {
		name     string
		existing int
	}{{"second organization", 1}, {"third organization", 2}} {
		t.Run(test.name, func(t *testing.T) {
			db, mock := newOrganizationSyncDatabase(t)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT setting_key FROM settings.*FOR UPDATE").WithArgs("community.capacity_lock").
				WillReturnRows(sqlmock.NewRows([]string{"setting_key"}).AddRow("community.capacity_lock"))
			mock.ExpectQuery("SELECT count.*FROM `organizations` WHERE status <> \\?").WithArgs("deleted").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(test.existing))
			if test.existing < edition.MaxOrganizations {
				mock.ExpectExec("INSERT INTO `organizations`").WillReturnResult(sqlmock.NewResult(2, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			organization := domain.Organization{Name: "second organization", Status: "active"}
			err := NewOrganizationRepository(db).Create(context.Background(), &organization)
			if test.existing < edition.MaxOrganizations {
				if err != nil || organization.ID != 2 {
					t.Fatalf("second organization: id=%d err=%v", organization.ID, err)
				}
			} else if !errors.Is(err, edition.ErrLimitExceeded) || organization.ID != 0 {
				t.Fatalf("third organization must not be inserted: id=%d err=%v", organization.ID, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
