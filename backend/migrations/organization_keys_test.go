package migrations

import (
	"strings"
	"testing"
)

func TestOrganizationKeyMigrationPreservesHistoricalLedgers(t *testing.T) {
	data, err := Files.ReadFile("009_organization_keys.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(data))
	for _, required := range []string{"users modify organization_id", "organization_id = null where role = 'super_admin'", "api_keys modify user_id", "api_keys set user_id = null", "allowed_provider_ids json", "allowed_models json"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("missing migration: %s", required)
		}
	}
	for _, forbidden := range []string{"update usage_logs", "update request_logs", "update security_events", "delete from", "drop table", "set organization_id = 1"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration rewrites history or removes data: %s", forbidden)
		}
	}
}
