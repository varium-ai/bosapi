package infrastructure

import (
	"context"
	"strings"
	"testing"
	"time"

	"bosapi/backend/migrations"
	"gorm.io/gorm"
)

// Uses the disposable MySQL database configured by BOS_USAGE_DASHBOARD_TEST_DSN.
func TestRequestLogPartitionsMySQL(t *testing.T) {
	database := newAdminDashboardMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var now time.Time
	if err := database.WithContext(ctx).Raw("SELECT UTC_TIMESTAMP(6)").Scan(&now).Error; err != nil {
		t.Fatal(err)
	}
	cutoff := now.UTC().Truncate(24 * time.Hour).Add(-7 * 24 * time.Hour)
	script, err := migrations.Files.ReadFile("021_request_log_transition.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Run the prepared migration statements on one session, like the runner.
	if err := database.WithContext(ctx).Connection(func(conn *gorm.DB) error {
		for _, statement := range strings.Split(string(script), ";") {
			if strings.TrimSpace(statement) != "" {
				if err := conn.Exec(statement).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.WithContext(ctx).Exec("INSERT INTO request_logs_v2 (created_at,level,event,message) VALUES (?,'INFO','test','old'), (?,'INFO','test','cutoff'), (?,'INFO','test','future')",
		cutoff.Add(-time.Microsecond), cutoff, now.AddDate(0, 0, 30)).Error; err != nil {
		t.Fatal(err)
	}
	repository := &RequestLogRepository{database: database}
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	// Another replica holding the advisory lock must make this run a no-op.
	owner, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	var locked int
	if err := owner.QueryRowContext(ctx, "SELECT GET_LOCK("+requestLogPartitionLock+", 0)").Scan(&locked); err != nil || locked != 1 {
		t.Fatalf("acquire maintenance lock=%d err=%v", locked, err)
	}
	if partitioned, err := repository.maintainRequestLogPartitions(ctx, now); err != nil || !partitioned {
		t.Fatalf("locked maintenance: partitioned=%v err=%v", partitioned, err)
	}
	var before int64
	if err := database.Table("request_logs_v2").Count(&before).Error; err != nil || before != 3 {
		t.Fatalf("contending replica changed rows=%d err=%v", before, err)
	}
	if err := owner.QueryRowContext(ctx, "SELECT RELEASE_LOCK("+requestLogPartitionLock+")").Scan(&locked); err != nil || locked != 1 {
		t.Fatalf("release maintenance lock=%d err=%v", locked, err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		partitioned, err := repository.maintainRequestLogPartitions(ctx, now)
		if err != nil || !partitioned {
			t.Fatalf("partitioned=%v err=%v", partitioned, err)
		}
	}
	var count int64
	if err := database.WithContext(ctx).Table("request_logs_v2").Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("retained rows=%d, want cutoff and future rows: %v", count, err)
	}
	// Catch-all rows move into daily partitions when maintenance catches up.
	if _, err := repository.maintainRequestLogPartitions(ctx, now.AddDate(0, 0, 30)); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.maintainRequestLogPartitions(ctx, now.AddDate(0, 0, 30)); err != nil {
		t.Fatal(err)
	}
	if err := database.WithContext(ctx).Table("request_logs_v2").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("catch-up retained rows=%d, want future row: %v", count, err)
	}
}
