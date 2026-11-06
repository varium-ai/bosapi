package infrastructure

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"regexp"
	"testing"

	"bosapi/backend/internal/user/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm"
)

func TestUpdateKeyIPListsJSON(t *testing.T) {
	for _, mode := range []string{"admin", "owned", "owned with models"} {
		for _, field := range []string{"ip_allowlist", "ip_blocklist"} {
			if mode == "admin" && field == "ip_blocklist" {
				continue
			}
			for _, test := range []struct {
				name   string
				values []string
				json   string
			}{
				{"single", []string{"192.0.2.0/24"}, `["192.0.2.0/24"]`},
				{"multiple", []string{"192.0.2.1", "2001:db8::/32"}, `["192.0.2.1","2001:db8::/32"]`},
				{"empty", []string{}, `[]`},
				{"nil", nil, `null`},
			} {
				t.Run(mode+"/"+field+"/"+test.name, func(t *testing.T) {
					repository, mock := quotaRepository(t)
					updates := map[string]any{field: test.values}
					mock.ExpectBegin()
					if mode != "owned" {
						query := mock.ExpectQuery("SELECT .* FROM `api_keys` WHERE .*id = .* FOR UPDATE")
						if mode == "admin" {
							query.WithArgs(uint64(7), "deleted", uint64(2), 1)
						} else {
							query.WithArgs(uint64(7), uint64(2), "deleted", 1)
						}
						query.WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "status"}).AddRow(7, 2, "active"))
					}
					statement := "UPDATE `api_keys` SET "
					args := []driver.Value{}
					if mode == "owned with models" {
						updates["allowed_models"] = []string{"model-a"}
						statement += "`allowed_models`=?,"
						args = append(args, `["model-a"]`)
					}
					statement += "`" + field + "`=?,`updated_at`=? WHERE "
					args = append(args, test.json, sqlmock.AnyArg())
					if mode == "owned" {
						statement += "id = ? AND organization_id = ? AND organization_id > 0 AND status <> ?"
						args = append(args, uint64(7), uint64(2), "deleted")
					} else {
						statement += "`id` = ?"
						args = append(args, uint64(7))
					}
					mock.ExpectExec(regexp.QuoteMeta(statement)).WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit()
					var err error
					if mode == "admin" {
						err = repository.UpdateAdminKey(context.Background(), 7, 2, updates)
					} else {
						err = repository.UpdateOwned(context.Background(), 7, 3, 2, updates)
					}
					if err != nil {
						t.Fatal(err)
					}
					if err := mock.ExpectationsWereMet(); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestEncodeKeyAccessUpdatesRejectsInvalidIPLists(t *testing.T) {
	for _, field := range []string{"ip_allowlist", "ip_blocklist"} {
		t.Run(field, func(t *testing.T) {
			repository, mock := quotaRepository(t)
			if err := encodeKeyAccessUpdates(repository.database, 2, map[string]any{field: "192.0.2.0/24"}); !errors.Is(err, gorm.ErrInvalidData) {
				t.Fatalf("non-list IP update accepted: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBatchKeysValidateEveryChannelsPolicy(t *testing.T) {
	repository, mock := quotaRepository(t)
	hash := sha256.Sum256([]byte("fixture"))
	keys := []*domain.APIKey{
		{OrganizationID: 3, KeyHash: hex.EncodeToString(hash[:]), AllowedProviderIDs: []uint64{7}},
		{OrganizationID: 3, KeyHash: hex.EncodeToString(hash[:]), AllowedProviderIDs: []uint64{8}},
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT setting_key FROM settings").WithArgs("community.capacity_lock").WillReturnRows(sqlmock.NewRows([]string{"setting_key"}).AddRow("community.capacity_lock"))
	mock.ExpectQuery("SELECT count.*FROM `api_keys`").WithArgs("deleted").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT id, status FROM `organizations`").WithArgs(uint64(3), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(3, "active"))
	// Both policies must stay within channels assigned to the organization.
	for _, item := range []struct{ id, count int }{{7, 1}, {8, 0}} {
		mock.ExpectQuery("SELECT count.*accounts.*EXISTS.*organization_provider_bindings").WithArgs(item.id, uint64(3), "active").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(item.count))
	}
	mock.ExpectRollback()
	if err := repository.CreateMany(context.Background(), keys); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("unauthorized channel in second key was accepted: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
