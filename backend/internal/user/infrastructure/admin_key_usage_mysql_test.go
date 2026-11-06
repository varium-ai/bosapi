package infrastructure

import (
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/platform/testusage"
	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/internal/user/domain"
	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newKeyUsageMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("BOS_USAGE_DASHBOARD_TEST_DSN")
	if dsn == "" {
		t.Skip("set BOS_USAGE_DASHBOARD_TEST_DSN to verify Key usage against MySQL")
	}
	config, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.DBName, config.ParseTime, config.MultiStatements = "", true, true
	root, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema := "key_usage_test_" + rand.Text()
	if _, err := root.ExecContext(ctx, "CREATE DATABASE `"+schema+"`"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := root.ExecContext(ctx, "DROP DATABASE `"+schema+"`"); err != nil {
			t.Error(err)
		}
	})
	config.DBName = schema
	database, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	testusage.Install(t, database)
	testusage.LegacyDefaults(t, database)
	return database
}

func TestAdminKeyUsageMySQLParityAndRangePlan(t *testing.T) {
	database := newKeyUsageMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database = database.WithContext(ctx)
	type entry struct {
		APIKeyID, OrganizationID, TotalTokens uint64
		CostAmount                            string
		CreatedAt                             time.Time
	}
	var entries []entry
	nows := []time.Time{
		time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC),  // month starts inside a week
		time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), // month starts before rolling 30d
	}
	for _, now := range nows {
		day, week, month := usagetime.PeriodStarts(now)
		for _, boundary := range []time.Time{day, week, month, now.Add(-720 * time.Hour), now} {
			for _, delta := range []time.Duration{-time.Microsecond, 0, time.Microsecond} {
				// A moved Key includes history from both recorded organizations.
				entries = append(entries, entry{7, 1, 3, "0.00000001", boundary.Add(delta)}, entry{7, 2, 5, "0.12345678", boundary.Add(delta)})
			}
		}
	}
	for range 10000 {
		entries = append(entries, entry{99, 1, 100, "10", nows[0].AddDate(-1, 0, 0)})
	}
	if err := database.Table("usage_logs").CreateInBatches(entries, 500).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("ANALYZE TABLE usage_logs").Error; err != nil {
		t.Fatal(err)
	}
	repository := NewKeyRepository(database)
	for _, now := range nows {
		want := []domain.APIKey{{ID: 7, OrganizationID: 2}, {ID: 8}}
		got := append([]domain.APIKey(nil), want...)
		if err := repository.populatePeriodicUsageAt(ctx, want, now); err != nil {
			t.Fatal(err)
		}
		if err := repository.populateDisplayUsageAt(ctx, want, now); err != nil {
			t.Fatal(err)
		}
		if err := repository.populateAdminKeyUsageAt(ctx, got, now); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("usage changed at %s:\n got: %+v\nwant: %+v", now, got, want)
		}
		query, args := adminKeyUsageQuery(got, now)
		var plan []struct {
			Table string
			Type  string
			Key   string
			Rows  int64
			Extra string
		}
		if err := database.Raw("EXPLAIN "+query, args...).Scan(&plan).Error; err != nil {
			t.Fatal(err)
		}
		ranges := 0
		for _, step := range plan {
			if step.Table != "usage_logs" && step.Table != "usage_logs_v2" {
				continue
			}
			ranges++
			if (step.Type != "range" && !(step.Table == "usage_logs_v2" && step.Rows <= 1)) || step.Key != "idx_usage_key_time_totals" || !strings.Contains(step.Extra, "Using index") || step.Rows >= 10000 {
				t.Fatalf("expected bounded covering-index range: %+v", step)
			}
		}
		if ranges != 2*len(got) {
			t.Fatalf("expected two physical ranges per Key: %+v", plan)
		}
		t.Logf("%s: matched legacy totals, EXPLAIN: %+v", now, plan)
	}
	var count int64
	if err := database.Table("usage_logs").Count(&count).Error; err != nil || count != int64(len(entries)) {
		t.Fatalf("index upgrade/rollback changed ledger rows=%d want %d: %v", count, len(entries), err)
	}
}
