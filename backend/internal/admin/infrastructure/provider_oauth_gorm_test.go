package infrastructure

import (
	"context"
	"errors"
	"testing"

	"bosapi/backend/internal/admin/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestSubscriptionCredentialRotationUsesRowLockAndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT \\* FROM `accounts`.*FOR UPDATE").WithArgs(7, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "platform", "account_type", "credentials_ciphertext"}).AddRow(7, "anthropic", "oauth", []byte("encrypted-before")))
			if fail {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec("UPDATE `accounts` SET `credentials_ciphertext`=.*WHERE `id` = \\?").WithArgs([]byte("encrypted-after"), sqlmock.AnyArg(), 7).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			_, ciphertext, err := NewProviderRepository(db).UpdateCredentialLocked(context.Background(), 7, func(p domain.Provider, secret string) (string, error) {
				if p.ID != 7 || secret != "encrypted-before" {
					t.Fatal("wrong locked credential")
				}
				if fail {
					return "", errors.New("refresh failed")
				}
				return "encrypted-after", nil
			})
			if fail && err == nil || !fail && (err != nil || ciphertext != "encrypted-after") {
				t.Fatalf("result %q %v", ciphertext, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSubscriptionAccountAndCatalogRollbackTogether(t *testing.T) {
	for _, create := range []bool{true, false} {
		t.Run(map[bool]string{true: "create", false: "reauthorize"}[create], func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, DisableNestedTransaction: true})
			if err != nil {
				t.Fatal(err)
			}
			catalogFailure := errors.New("model catalog unavailable")
			mock.ExpectBegin()
			if create {
				mock.ExpectExec("INSERT INTO `accounts`").WillReturnResult(sqlmock.NewResult(7, 1))
			} else {
				mock.ExpectExec("UPDATE `accounts`").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery("SELECT \\* FROM `accounts`").WillReturnRows(sqlmock.NewRows([]string{"id", "platform", "account_type"}).AddRow(7, "anthropic", "oauth"))
			}
			mock.ExpectExec("UPDATE `provider_models`").WillReturnError(catalogFailure)
			mock.ExpectRollback()
			repository := NewProviderRepository(db)
			models := []domain.ProviderModel{{ModelName: "claude-test", Status: "active"}}
			if create {
				_, err = repository.CreateProviderWithModels(context.Background(), domain.Provider{Name: "subscription", Platform: "anthropic", AccountType: "oauth"}, "encrypted-new", models)
			} else {
				_, err = repository.UpdateProviderWithModels(context.Background(), 7, map[string]any{"name": "subscription"}, "encrypted-new", models)
			}
			if !errors.Is(err, catalogFailure) {
				t.Fatalf("catalog failure must roll back the account: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
