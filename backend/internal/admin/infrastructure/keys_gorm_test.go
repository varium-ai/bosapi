package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestResetAdminKeyStoresRawDigestBytes(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}

	secret := "sk-gw-test-key"
	digest := sha256.Sum256([]byte(secret))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `api_keys` WHERE \\(id = \\? AND status <> \\?\\) AND organization_id = \\? ORDER BY `api_keys`.`id` LIMIT \\? FOR UPDATE").
		WithArgs(uint64(7), "deleted", uint64(3), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id"}).AddRow(7, 3))
	mock.ExpectExec("UPDATE `api_keys` SET `key_encrypted`=\\?,`key_hash`=\\?,`key_prefix`=\\?,`key_suffix`=\\?,`status`=\\?,`updated_at`=\\? WHERE `id` = \\?").
		WithArgs("new-ciphertext", string(digest[:]), "sk-gw", "-key", "active", sqlmock.AnyArg(), uint64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	organizationID, err := NewAdminKeyRepository(database).ResetAdminKey(context.Background(), 7, 3, hex.EncodeToString(digest[:]), "sk-gw", "new-ciphertext", "-key")
	if err != nil || organizationID != 3 {
		t.Fatalf("reset key: organization_id=%d err=%v", organizationID, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdminKeyClearsOldCiphertextAndReplacesSuffix(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("vendor-current-secret-NEW4"))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `api_keys` WHERE \\(id = \\? AND status <> \\?\\) AND organization_id = \\? ORDER BY `api_keys`.`id` LIMIT \\? FOR UPDATE").
		WithArgs(uint64(7), "deleted", uint64(3), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "key_encrypted", "key_suffix"}).AddRow(7, 3, "old-ciphertext", "OLD4"))
	mock.ExpectExec("UPDATE `api_keys` SET `key_encrypted`=\\?,`key_hash`=\\?,`key_prefix`=\\?,`key_suffix`=\\?,`status`=\\?,`updated_at`=\\? WHERE `id` = \\?").
		WithArgs(nil, string(digest[:]), "vend", "NEW4", "active", sqlmock.AnyArg(), uint64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	organizationID, err := NewAdminKeyRepository(database).ResetAdminKey(context.Background(), 7, 3, hex.EncodeToString(digest[:]), "vend", "", "NEW4")
	if err != nil || organizationID != 3 {
		t.Fatalf("reset key: organization_id=%d err=%v", organizationID, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
