package infrastructure

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	adminDomain "bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/user/application"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestValidateAPIKeyOrganizationDoesNotRequireMembers(t *testing.T) {
	for _, status := range []string{"active", "disabled"} {
		t.Run(status, func(t *testing.T) {
			repository, mock := quotaRepository(t)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT id, status FROM `organizations` WHERE id = \\? ORDER BY").WithArgs(uint64(2), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(2, status))
			if status == "active" {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err := repository.database.Transaction(func(tx *gorm.DB) error { return validateAPIKeyOrganization(tx, 2) })
			if status == "active" && err != nil || status != "active" && !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("status=%s err=%v", status, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUpdateAdminKeyRejectsUserChangesWithoutSQL(t *testing.T) {
	repository, mock := quotaRepository(t)
	if err := repository.UpdateAdminKey(context.Background(), 7, 0, map[string]any{"user_id": uint64(4)}); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("user change accepted: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFindGatewayKeyRequiresFullSecret(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewKeyRepository(database)
	now := time.Now().UTC()
	secret := "sk-gw-" + strings.Repeat("a", 64)
	prefix := secret[:24]
	fullHash := sha256.Sum256([]byte(secret))
	prefixHash := sha256.Sum256([]byte(prefix))
	keyQuery := "FROM `api_keys` WHERE key_hash = \\? AND status = \\?"
	keyColumns := []string{"id", "name", "key_hash", "key_prefix", "key_suffix", "user_id", "organization_id", "status"}

	mock.ExpectQuery(keyQuery).WithArgs(prefixHash[:], "active", now, 1).
		WillReturnRows(sqlmock.NewRows(keyColumns))
	if _, found, err := repository.FindGatewayKey(context.Background(), prefix, now); err != nil || found {
		t.Fatalf("public prefix authenticated: found=%v err=%v", found, err)
	}

	mock.ExpectQuery(keyQuery).WithArgs(fullHash[:], "active", now, 1).
		WillReturnRows(sqlmock.NewRows(keyColumns).AddRow(7, "test key", fullHash[:], prefix, "aaaa", 3, 2, "active"))
	mock.ExpectQuery("SELECT status FROM `organizations` WHERE id = \\?").WithArgs(uint64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))
	key, found, err := repository.FindGatewayKey(context.Background(), secret, now)
	if err != nil || !found || key.ID != 7 {
		t.Fatalf("full secret rejected: found=%v key_id=%d err=%v", found, key.ID, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAPIKeyRecordRateLimitColumns(t *testing.T) {
	parsed, err := schema.Parse(&APIKeyRecord{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse api key schema: %v", err)
	}
	if field := parsed.LookUpField("KeyHash"); field == nil || field.TagSettings["TYPE"] != "varbinary(128)" {
		t.Fatalf("KeyHash type = %+v, want VARBINARY(128)", field)
	}
	for _, test := range []struct{ field, column string }{
		{"RateLimit5H", "rate_limit_5h"},
		{"RateLimit1D", "rate_limit_1d"},
		{"RateLimit7D", "rate_limit_7d"},
	} {
		t.Run(test.field, func(t *testing.T) {
			if field := parsed.LookUpField(test.field); field == nil || field.DBName != test.column {
				t.Fatalf("field %s = %+v, want column %s", test.field, field, test.column)
			}
		})
	}
}

func TestUpdateOwnedReadsBackConfiguredRateLimits(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mock.ExpectClose()
		if err := sqlDatabase.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewKeyRepository(database)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `api_keys` SET `rate_limit_1d`=\\?,`rate_limit_5h`=\\?,`rate_limit_7d`=\\?,`updated_at`=\\? WHERE id = \\? AND organization_id = \\? AND organization_id > 0 AND status <> \\?").
		WithArgs(int64(200), int64(100), int64(300), sqlmock.AnyArg(), uint64(7), uint64(2), "deleted").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repository.UpdateOwned(ctx, 7, 3, 2, map[string]any{"rate_limit_5h": int64(100), "rate_limit_1d": int64(200), "rate_limit_7d": int64(300)}); err != nil {
		t.Fatalf("update rate limits: %v", err)
	}
	mock.ExpectQuery("FROM `api_keys` WHERE id = \\? AND organization_id = \\? AND organization_id > 0 AND status <> \\?").
		WithArgs(uint64(7), uint64(2), "deleted", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "rate_limit_5h", "rate_limit_1d", "rate_limit_7d"}).AddRow(7, 100, 200, 300))
	key, err := repository.FindOwned(ctx, 7, 3, 2)
	if err != nil {
		t.Fatalf("read configured key: %v", err)
	}
	for _, test := range []struct {
		name string
		got  *int64
		want int64
	}{
		{"5h", key.RateLimit5H, 100},
		{"1d", key.RateLimit1D, 200},
		{"7d", key.RateLimit7D, 300},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.got == nil || *test.got != test.want {
				t.Fatalf("rate limit = %v, want %d", test.got, test.want)
			}
		})
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteOwnedRemovesOnlyTheOrganizationsKey(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewKeyRepository(database)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `api_keys` WHERE id = \\? AND organization_id = \\? AND organization_id > 0").
		WithArgs(uint64(7), uint64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repository.DeleteOwned(context.Background(), 7, 3, 2); err != nil {
		t.Fatalf("delete owned key: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAdminKeysPermanentlyRemovesKeysWithinOrganization(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewKeyRepository(database)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `api_keys` WHERE id IN \\(\\?,\\?\\) AND organization_id = \\?").
		WithArgs(uint64(7), uint64(8), uint64(2)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	count, err := repository.DeleteAdminKeys(context.Background(), []uint64{7, 8}, 2)
	if err != nil || count != 2 {
		t.Fatalf("delete admin keys: count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListAdminKeysSearchNumericMatchesIDOrName(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewKeyRepository(database)
	ctx := context.Background()

	hash := sha256.Sum256([]byte("5"))
	hashBytes := string(hash[:])
	keyColumns := []string{"id", "name", "key_hash", "key_prefix", "user_id", "organization_id", "status", "created_at"}
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `api_keys` WHERE status <> \\? AND \\(name LIKE \\? OR id = \\? OR key_prefix LIKE \\? OR key_hash = \\?\\)").
		WithArgs("deleted", "%5%", uint64(5), "%5%", hashBytes).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT \\* FROM `api_keys` WHERE status <> \\? AND \\(name LIKE \\? OR id = \\? OR key_prefix LIKE \\? OR key_hash = \\?\\) ORDER BY created_at DESC, id DESC LIMIT \\?").
		WithArgs("deleted", "%5%", uint64(5), "%5%", hashBytes, 10).
		WillReturnRows(sqlmock.NewRows(keyColumns).AddRow(5, "prod key", "hash", "sk-gw-", 1, 1, "active", time.Now()))
	mock.ExpectQuery(`SELECT \? AS api_key_id, .* FROM \( *SELECT`).
		WithArgs(uint64(5), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(5), sqlmock.AnyArg(), uint64(5), sqlmock.AnyArg()).
		WillReturnRows(adminKeyUsageRows().AddRow(5, 100, 0.05, 500, 0.25, 2000, 1.0, 0.05, 3.0))
	keys, total, err := repository.ListAdminKeys(ctx, adminDomain.AdminKeyFilter{Search: "5"}, 1, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 || len(keys) != 1 || keys[0].ID != 5 {
		t.Fatalf("expected 1 key with id 5, got total=%d len=%d", total, len(keys))
	}
	if keys[0].DailyUsedTokens != 100 || keys[0].DailyUsedAmount != 0.05 {
		t.Fatalf("expected daily usage populated, got tokens=%d amount=%f", keys[0].DailyUsedTokens, keys[0].DailyUsedAmount)
	}
	if keys[0].UsageTodayAmount == nil || *keys[0].UsageTodayAmount != 0.05 || keys[0].Usage30dAmount == nil || *keys[0].Usage30dAmount != 3.0 {
		t.Fatal("admin listing must include the batched display usage")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListUserKeysDeterministicOrdering(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewKeyRepository(database)
	ctx := context.Background()

	keyColumns := []string{"id", "name", "key_hash", "key_prefix", "user_id", "organization_id", "status", "created_at"}
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `api_keys` WHERE organization_id = \\? AND organization_id > 0 AND status <> \\?").
		WithArgs(uint64(2), "deleted").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT \\* FROM `api_keys` WHERE organization_id = \\? AND organization_id > 0 AND status <> \\? ORDER BY created_at DESC, id DESC LIMIT \\?").
		WithArgs(uint64(2), "deleted", 10).
		WillReturnRows(sqlmock.NewRows(keyColumns).AddRow(1, "my key", "hash", "sk-gw-", 1, 2, "active", time.Now()))
	mock.ExpectQuery(`SELECT api_key_id, .*FROM \(SELECT .*usage_daily_rollups FORCE INDEX .*UNION ALL SELECT .*usage_logs_v2 FORCE INDEX .*UNION ALL SELECT .*usage_logs FORCE INDEX`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(1), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(1), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(1), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "daily_tokens", "daily_amount", "weekly_tokens", "weekly_amount", "monthly_tokens", "monthly_amount"}).AddRow(1, 0, 0.0, 0, 0.0, 0, 0.0))

	keys, total, err := repository.List(ctx, application.KeyFilter{UserID: 1, OrganizationID: 2}, 1, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 || len(keys) != 1 || keys[0].ID != 1 {
		t.Fatalf("expected 1 key with id 1, got total=%d len=%d", total, len(keys))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
