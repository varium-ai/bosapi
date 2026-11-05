// Package testusage installs production usage schemas in disposable MySQL tests.
package testusage

import (
	"strings"
	"testing"

	"bosapi/backend/migrations"
	"gorm.io/gorm"
)

func Install(t *testing.T, db *gorm.DB) {
	t.Helper()
	initial, err := migrations.Files.ReadFile("001_initial_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := string(initial)
	start := strings.Index(schema, "CREATE TABLE IF NOT EXISTS usage_logs (")
	end := start + strings.Index(schema[start:], ";") + 1
	if err := db.Exec(schema[start:end]).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"004_usage_error_code.sql", "015_usage_first_token.sql", "017_usage_daily_rollups.sql", "020_key_usage_covering_index.sql", "023_usage_log_transition.sql", "024_cache_usage.sql", "025_setup_usage_indexes.sql", "026_organization_quota_indexes.sql"} {
		script, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(string(script)).Error; err != nil {
			t.Fatalf("install %s: %v", name, err)
		}
	}
}

// Allow compact legacy query fixtures while retaining the production types,
// indexes, constraints and destination schema installed by Install.
func LegacyDefaults(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`ALTER TABLE usage_logs
		ALTER request_id SET DEFAULT (UUID()), ALTER usage_id SET DEFAULT '',
		ALTER model SET DEFAULT '', ALTER endpoint SET DEFAULT '', ALTER status_code SET DEFAULT 200,
		ALTER input_tokens SET DEFAULT 0, ALTER output_tokens SET DEFAULT 0`).Error; err != nil {
		t.Fatal(err)
	}
}

func Activate(t *testing.T, db *gorm.DB, drain bool) {
	t.Helper()
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE usage_log_backfill SET enabled = ? WHERE id = 1", drain).Error; err != nil {
			return err
		}
		var id int
		if err := tx.Raw("SELECT id FROM usage_log_retention WHERE id = 1 FOR UPDATE").Scan(&id).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE usage_log_retention SET legacy_high_watermark = (SELECT COALESCE(MAX(id),0) FROM usage_logs),
			partition_writes = 1 WHERE id = 1 AND partition_writes = 0`).Error
	}); err != nil {
		t.Fatal(err)
	}
}
