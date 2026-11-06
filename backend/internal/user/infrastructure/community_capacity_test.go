package infrastructure

import (
	"context"
	"errors"
	"strings"
	"testing"

	"bosapi/backend/internal/platform/edition"
	"bosapi/backend/internal/user/domain"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestKeyCreationEnforcesCommunityCapacityBeforeAnyInsertion(t *testing.T) {
	for _, test := range []struct {
		name               string
		existing, incoming int
		batch, denied      bool
	}{
		{name: "tenth key", existing: 9, incoming: 1},
		{name: "eleventh key", existing: 10, incoming: 1, denied: true},
		{name: "batch fills capacity", existing: 8, incoming: 2, batch: true},
		{name: "whole batch rejected", existing: 9, incoming: 2, batch: true, denied: true},
		{name: "batch larger than installation limit", incoming: 11, batch: true, denied: true},
		{name: "existing over-limit data", existing: 12, incoming: 1, denied: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, mock := quotaRepository(t)
			keys := make([]*domain.APIKey, test.incoming)
			for index := range keys {
				keys[index] = &domain.APIKey{OrganizationID: 3, Name: "capacity fixture", Status: "active", KeyHash: strings.Repeat("a", 64)}
			}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT setting_key FROM settings.*FOR UPDATE").WithArgs("community.capacity_lock").
				WillReturnRows(sqlmock.NewRows([]string{"setting_key"}).AddRow("community.capacity_lock"))
			mock.ExpectQuery("SELECT count.*FROM `api_keys` WHERE status <> \\?").WithArgs("deleted").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(test.existing))
			if test.denied {
				// No organization reads or INSERT statements may run for an oversized batch.
				mock.ExpectRollback()
			} else {
				mock.ExpectQuery("SELECT id, status FROM `organizations`").WithArgs(uint64(3), 1).
					WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(3, "active"))
				mock.ExpectExec("INSERT INTO `api_keys`").WillReturnResult(sqlmock.NewResult(100, int64(test.incoming)))
				mock.ExpectCommit()
			}
			var err error
			if test.batch {
				err = repository.CreateMany(context.Background(), keys)
			} else {
				err = repository.Create(context.Background(), keys[0])
			}
			if errors.Is(err, edition.ErrLimitExceeded) != test.denied || !test.denied && err != nil {
				t.Fatalf("unexpected result: %v", err)
			}
			for _, key := range keys {
				if test.denied && key.ID != 0 || !test.denied && key.ID == 0 {
					t.Fatalf("unexpected assigned id=%d, denied=%v", key.ID, test.denied)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
