package migrations

import (
	"strings"
	"testing"
)

func TestSecurityEventReadsMigrationHasScopedReceiptsAndRollback(t *testing.T) {
	forward, err := Files.ReadFile("014_security_event_reads.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS security_event_reads",
		"PRIMARY KEY (user_id, organization_id, event_id)",
		"event_id bigint unsigned NOT NULL",
		"REFERENCES users (id) ON DELETE CASCADE",
		"REFERENCES security_events (id) ON DELETE CASCADE",
	} {
		if !strings.Contains(string(forward), required) {
			t.Errorf("read receipt migration is missing %q", required)
		}
	}
	rollback, err := Files.ReadFile("014_security_event_reads.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rollback), "DROP TABLE IF EXISTS security_event_reads;") {
		t.Fatal("read receipt migration is not reversible")
	}
}
