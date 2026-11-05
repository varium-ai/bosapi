package migrations

import (
	"strings"
	"testing"
)

func TestOrganizationQuotaIndexesMigration(t *testing.T) {
	forward, err := Files.ReadFile("026_organization_quota_indexes.sql")
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := Files.ReadFile("026_organization_quota_indexes.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Normalize whitespace while retaining column order: the pending flag must
	// precede the time range, with both aggregate values covered by the index.
	up, down := strings.Join(strings.Fields(string(forward)), " "), strings.Join(strings.Fields(string(rollback)), " ")
	for _, table := range []string{"usage_logs", "usage_logs_v2"} {
		want := "ALTER TABLE " + table + " ADD INDEX idx_usage_org_pending_time (organization_id, rollup_applied, created_at, total_tokens, cost_amount), ALGORITHM=INPLACE, LOCK=NONE;"
		if !strings.Contains(up, want) {
			t.Errorf("missing forward index: %s", want)
		}
		want = "ALTER TABLE " + table + " DROP INDEX idx_usage_org_pending_time, ALGORITHM=INPLACE, LOCK=NONE;"
		if !strings.Contains(down, want) {
			t.Errorf("missing rollback index: %s", want)
		}
	}
	if strings.Count(up, "ALTER TABLE") != 2 || strings.Count(down, "ALTER TABLE") != 2 {
		t.Fatal("migration must change only the two ledger indexes")
	}
	for _, fragment := range []string{"DELETE FROM", "DROP TABLE", "UPDATE ", "INSERT INTO"} {
		if strings.Contains(strings.ToUpper(up+down), fragment) {
			t.Fatalf("migration changes ledger data: %s", fragment)
		}
	}
	rollup, err := Files.ReadFile("017_usage_daily_rollups.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rollup), "KEY idx_rollup_org_date (organization_id, usage_date)") {
		t.Fatal("missing existing organization rollup index")
	}
}
