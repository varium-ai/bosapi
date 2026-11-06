package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm"

	"bosapi/backend/internal/admin/domain"
)

func TestKeyTemplateRepositoryListAlwaysUsesCreatorPredicate(t *testing.T) {
	db, mock := newOrganizationSyncDatabase(t)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `api_key_templates` WHERE created_by = \\?").WithArgs(uint64(19), uint64(19), "super_admin").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	now := time.Now()
	settings := `{"organization_id":3,"user_id":2,"quota_tokens":0}`
	mock.ExpectQuery("SELECT \\* FROM `api_key_templates` WHERE created_by = \\?.* ORDER BY id DESC LIMIT \\?").WithArgs(uint64(19), uint64(19), "super_admin", 20).WillReturnRows(sqlmock.NewRows([]string{"id", "created_by", "name", "settings", "created_at", "updated_at"}).AddRow(7, 19, "private", settings, now, now))
	items, total, err := NewKeyTemplateRepository(db).List(context.Background(), 19, 1, 20)
	if err != nil || total != 1 || len(items) != 1 || items[0].CreatedBy != 19 || *items[0].Settings.QuotaTokens != 0 {
		t.Fatalf("unexpected private list: %+v %v", items, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKeyTemplateRepositoryRejectsForeignUpdateAndDelete(t *testing.T) {
	db, mock := newOrganizationSyncDatabase(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `api_key_templates` WHERE id = \\? AND created_by = \\?.*FOR UPDATE").WithArgs(uint64(7), uint64(19), uint64(19), "super_admin", 1).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	repository := NewKeyTemplateRepository(db)
	if err := repository.Save(context.Background(), &domain.KeyTemplate{ID: 7, CreatedBy: 19}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign update: %v", err)
	}
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `api_key_templates` WHERE id = \\? AND created_by = \\?").WithArgs(uint64(7), uint64(19), uint64(19), "super_admin").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	if err := repository.Delete(context.Background(), 19, 7); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKeyTemplateRepositoryCreatesPolicyWithoutCredentialMaterial(t *testing.T) {
	db, mock := newOrganizationSyncDatabase(t)
	item := domain.KeyTemplate{CreatedBy: 19, Name: "private'; DROP TABLE api_key_templates; --", Settings: domain.KeyTemplateSettings{OrganizationID: 3, UserID: 2}}
	encoded, err := json.Marshal(item.Settings)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `organizations` WHERE id = \\? AND status = \\?").WithArgs(uint64(3), "active", 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
	mock.ExpectExec("INSERT INTO `api_key_templates`").WithArgs(uint64(19), item.Name, string(encoded), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()
	if err := NewKeyTemplateRepository(db).Save(context.Background(), &item); err != nil || item.ID != 7 {
		t.Fatalf("save failed: %+v %v", item, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKeyTemplateRepositoryUpdatesAndDeletesOnlyOwnedRecords(t *testing.T) {
	db, mock := newOrganizationSyncDatabase(t)
	now := time.Now()
	item := domain.KeyTemplate{ID: 7, CreatedBy: 19, Name: "updated", Settings: domain.KeyTemplateSettings{OrganizationID: 3, UserID: 2}}
	encoded, err := json.Marshal(item.Settings)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `api_key_templates` WHERE id = \\? AND created_by = \\?.*FOR UPDATE").WithArgs(uint64(7), uint64(19), uint64(19), "super_admin", 1).WillReturnRows(sqlmock.NewRows([]string{"id", "created_by", "name", "settings", "created_at", "updated_at"}).AddRow(7, 19, "old", `{}`, now, now))
	mock.ExpectQuery("SELECT \\* FROM `organizations` WHERE id = \\? AND status = \\?").WithArgs(uint64(3), "active", 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
	mock.ExpectExec("UPDATE `api_key_templates` SET `name`=\\?,`settings`=\\?,`updated_at`=\\? WHERE id = \\? AND created_by = \\?").WithArgs("updated", string(encoded), sqlmock.AnyArg(), uint64(7), uint64(19)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	repository := NewKeyTemplateRepository(db)
	if err := repository.Save(context.Background(), &item); err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `api_key_templates` WHERE id = \\? AND created_by = \\?").WithArgs(uint64(7), uint64(19), uint64(19), "super_admin").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repository.Delete(context.Background(), 19, 7); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKeyTemplateRepositoryReadErrorsDoNotReturnPartialPrivateData(t *testing.T) {
	for _, stage := range []string{"count", "read", "decode"} {
		t.Run(stage, func(t *testing.T) {
			db, mock := newOrganizationSyncDatabase(t)
			failure := errors.New("read failed")
			count := mock.ExpectQuery("SELECT count\\(\\*\\) FROM `api_key_templates` WHERE created_by = \\?").WithArgs(uint64(19), uint64(19), "super_admin")
			if stage == "count" {
				count.WillReturnError(failure)
			} else {
				count.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
				read := mock.ExpectQuery("SELECT \\* FROM `api_key_templates` WHERE created_by = \\?").WithArgs(uint64(19), uint64(19), "super_admin", 20)
				if stage == "read" {
					read.WillReturnError(failure)
				} else {
					read.WillReturnRows(sqlmock.NewRows([]string{"id", "created_by", "name", "settings"}).AddRow(7, 19, "invalid", `invalid`))
				}
			}
			items, _, err := NewKeyTemplateRepository(db).List(context.Background(), 19, 1, 20)
			if err == nil || items != nil {
				t.Fatalf("partial data returned: %+v %v", items, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKeyTemplateRepositoryRejectsUnavailableOrganization(t *testing.T) {
	db, mock := newOrganizationSyncDatabase(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `organizations` WHERE id = \\? AND status = \\?").WithArgs(uint64(3), "active", 1).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	item := domain.KeyTemplate{CreatedBy: 19, Name: "invalid owner", Settings: domain.KeyTemplateSettings{OrganizationID: 3, UserID: 2}}
	if err := NewKeyTemplateRepository(db).Save(context.Background(), &item); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unavailable organization accepted: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKeyTemplateRepositoryDeletePropagatesStorageFailure(t *testing.T) {
	db, mock := newOrganizationSyncDatabase(t)
	failure := errors.New("delete failed")
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `api_key_templates` WHERE id = \\? AND created_by = \\?").WithArgs(uint64(7), uint64(19), uint64(19), "super_admin").WillReturnError(failure)
	mock.ExpectRollback()
	if err := NewKeyTemplateRepository(db).Delete(context.Background(), 19, 7); !errors.Is(err, failure) {
		t.Fatal("delete lost storage failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKeyTemplatesRespectCurrentOrganizationAfterAdministratorMoves(t *testing.T) {
	db, _ := newOrganizationSyncDatabase(t)
	statement := keyTemplateScope(db.Model(&keyTemplateRecord{}), 19).Session(&gorm.Session{DryRun: true}).Find(&[]keyTemplateRecord{}).Statement
	for _, required := range []string{"created_by = ?", "creator.id = ?", "creator.role = ?", "creator.organization_id > 0", "JSON_EXTRACT(api_key_templates.settings, '$.organization_id')", "CAST(creator.organization_id AS CHAR)"} {
		if !strings.Contains(statement.SQL.String(), required) {
			t.Fatalf("missing organization scope %s: %s", required, statement.SQL.String())
		}
	}
}
