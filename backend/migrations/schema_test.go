package migrations

import (
	"regexp"
	"testing"
)

func TestInitialSchemaCreatesAllApplicationTables(t *testing.T) {
	data, err := Files.ReadFile("001_initial_schema.sql")
	if err != nil {
		t.Fatal(err)
	}

	created := make(map[string]bool)
	for _, match := range regexp.MustCompile("(?i)CREATE TABLE IF NOT EXISTS\\s+`?([a-z_]+)`?").FindAllSubmatch(data, -1) {
		created[string(match[1])] = true
	}
	want := []string{
		"access_revocations", "accounts", "admin_otp_challenges", "api_keys", "daily_usages", "groups", "key_ip_stats", "login_logs", "model_metadata",
		"organization_provider_bindings", "organizations", "provider_models", "refresh_records", "refresh_sessions",
		"request_logs", "security_events", "settings", "usage_logs", "users",
	}
	if len(created) != len(want) {
		t.Fatalf("initial schema creates %d tables, want %d: %v", len(created), len(want), created)
	}
	for _, table := range want {
		if !created[table] {
			t.Errorf("initial schema does not create %s", table)
		}
	}
}
