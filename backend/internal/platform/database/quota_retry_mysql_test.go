package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"io/fs"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"bosapi/backend/migrations"
	"github.com/go-sql-driver/mysql"
)

func TestGatewayQuotaMigrationUpgradeMySQL(t *testing.T) {
	dsn := os.Getenv("BOS_MIGRATIONS_TEST_DSN")
	if dsn == "" {
		t.Skip("set BOS_MIGRATIONS_TEST_DSN for the isolated migration upgrade test")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.DBName = ""
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := "quota_upgrade_" + rand.Text()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if _, err := db.ExecContext(ctx, "CREATE DATABASE `"+schema+"`"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := db.ExecContext(cleanup, "DROP DATABASE `"+schema+"`"); err != nil {
			t.Error(err)
		}
	}()
	config.DBName = schema
	baseline := fstest.MapFS{}
	err = fs.WalkDir(migrations.Files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || path == "029_gateway_quota_retries.sql" {
			return err
		}
		body, err := migrations.Files.ReadFile(path)
		baseline[path] = &fstest.MapFile{Data: body}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(ctx, config.FormatDSN(), baseline, InitialAdmin{Email: "admin@example.com", Password: "initial-test-password-123"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ApplyMigrations(ctx, config.FormatDSN(), migrations.Files); err != nil {
			t.Fatalf("upgrade or restart failed: %v", err)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+schema+"`.schema_migrations WHERE filename = '029_gateway_quota_retries.sql' AND applied_at IS NOT NULL").Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration applied count=%d err=%v", count, err)
	}
}
