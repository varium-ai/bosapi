package infrastructure

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestSystemSettingRepository_GORM(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()

	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewSystemSettingRepository(database)
	ctx := context.Background()

	// 1. Get setting - not found
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `settings` WHERE setting_key = ? ORDER BY `settings`.`setting_key` LIMIT ?")).
		WithArgs("gateway.endpoints", 1).
		WillReturnRows(sqlmock.NewRows([]string{"setting_key", "value_text", "is_secret", "updated_by", "updated_at"}))

	setting, err := repo.Get(ctx, "gateway.endpoints")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if setting != nil {
		t.Fatalf("expected nil setting, got: %+v", setting)
	}

	// 2. Get setting - found
	text := `{"prod_endpoint":"https://api.test.com","test_endpoint":"https://sandbox.test.com"}`
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `settings` WHERE setting_key = ? ORDER BY `settings`.`setting_key` LIMIT ?")).
		WithArgs("gateway.endpoints", 1).
		WillReturnRows(sqlmock.NewRows([]string{"setting_key", "value_text", "is_secret", "updated_by", "updated_at"}).
			AddRow("gateway.endpoints", text, false, uint64(1), time.Now()))

	setting, err = repo.Get(ctx, "gateway.endpoints")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if setting == nil || setting.ValueText != text {
		t.Fatalf("expected setting with value %s, got %+v", text, setting)
	}

	// 3. Delete setting
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `settings` WHERE setting_key = ?")).
		WithArgs("gateway.endpoints").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err = repo.Delete(ctx, "gateway.endpoints")
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}
}
