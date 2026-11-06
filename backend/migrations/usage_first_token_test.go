package migrations

import (
	"strings"
	"testing"
)

func TestUsageFirstTokenMigrationKeepsHistoricalTimingUnknownAndSupportsRollback(t *testing.T) {
	forward, err := Files.ReadFile("015_usage_first_token.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"ALTER TABLE usage_logs", "ADD COLUMN first_token_ms int unsigned NULL AFTER duration_ms"} {
		if !strings.Contains(string(forward), required) {
			t.Errorf("first token migration is missing %q", required)
		}
	}
	if strings.Contains(strings.ToUpper(string(forward)), "DEFAULT") {
		t.Fatal("historical first token timing must remain NULL")
	}
	rollback, err := Files.ReadFile("015_usage_first_token.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"ALTER TABLE usage_logs", "DROP COLUMN first_token_ms"} {
		if !strings.Contains(string(rollback), required) {
			t.Errorf("first token rollback is missing %q", required)
		}
	}
}
