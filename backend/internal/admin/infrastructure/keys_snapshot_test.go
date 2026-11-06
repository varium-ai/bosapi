package infrastructure

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestAdminKeyDetailSharesDisplayCacheAndReadsMetadataLive(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewCachedAdminKeyRepository(db, nil)
	defer repo.Close()
	ctx := context.Background()
	const lookup = "SELECT \\* FROM `api_keys` WHERE \\(id = \\? AND status <> \\?\\) AND organization_id = \\? ORDER BY `api_keys`.`id` LIMIT \\?"
	columns := []string{"id", "organization_id", "name", "status", "quota_used_amount"}
	mock.ExpectQuery(lookup).WithArgs(uint64(149), "deleted", uint64(3), 1).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(149, 3, "Original", "active", 1.25))
	mock.ExpectBegin()
	mock.ExpectQuery("WITH source AS").WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "daily_tokens", "weekly_tokens", "monthly_tokens", "daily_amount", "weekly_amount", "monthly_amount", "today_amount", "recent_amount"}).AddRow(149, 11, 22, 33, 0.1, 0.2, 0.3, 0.1, 0.4))
	mock.ExpectCommit()
	first, err := repo.FindAdminKey(ctx, 149, 3)
	if err != nil || first.Name != "Original" || first.UsageStatsStatus != "ready" || first.UsageStatsAsOf == nil || first.DailyUsedTokens != 11 {
		t.Fatalf("first metadata/snapshot: %+v err=%v", first, err)
	}
	// A warm read fetches changed policy/accounting fields without recomputing usage.
	mock.ExpectQuery(lookup).WithArgs(uint64(149), "deleted", uint64(3), 1).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(149, 3, "Renamed", "disabled", 2.5))
	second, err := repo.FindAdminKey(ctx, 149, 3)
	if err != nil || second.Name != "Renamed" || second.Status != "disabled" || second.QuotaUsedAmount != 2.5 || second.DailyUsedTokens != 11 || second.UsageStatsAsOf == nil || !second.UsageStatsAsOf.Equal(*first.UsageStatsAsOf) {
		t.Fatalf("metadata was cached or statistics recomputed: %+v err=%v", second, err)
	}
	// Authorization scope is checked on every read, before looking at the snapshot.
	mock.ExpectQuery(lookup).WithArgs(uint64(149), "deleted", uint64(4), 1).
		WillReturnRows(sqlmock.NewRows(columns))
	denied, err := repo.FindAdminKey(ctx, 149, 4)
	if !errors.Is(err, gorm.ErrRecordNotFound) || denied.ID != 0 {
		t.Fatalf("scope bypass: %+v %v", denied, err)
	}
	// A failed display refresh must not fail or hide the Key's live metadata.
	mock.ExpectQuery(lookup).WithArgs(uint64(150), "deleted", uint64(3), 1).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(150, 3, "Available metadata", "active", 4.5))
	mock.ExpectBegin()
	mock.ExpectQuery("WITH source AS").WillReturnError(errors.New("statistics unavailable"))
	mock.ExpectRollback()
	unavailable, err := repo.FindAdminKey(ctx, 150, 3)
	if err != nil || unavailable.Name != "Available metadata" || unavailable.QuotaUsedAmount != 4.5 || unavailable.UsageStatsStatus != "unavailable" {
		t.Fatalf("stats failure blocked metadata or masqueraded as zero: %+v %v", unavailable, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
