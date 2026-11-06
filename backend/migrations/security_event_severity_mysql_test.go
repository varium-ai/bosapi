package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"bosapi/backend/internal/admin/domain"
)

type securityEventSeverityFixture struct {
	rule, action, tool, kind, before, after string
}

func TestSecurityEventSeverityMigrationMySQL(t *testing.T) {
	conn, ctx := legacyBindingsTestDatabase(t)
	applySecurityEventSeverityScript(t, ctx, conn, "001_initial_schema.sql")
	fixtures := securityEventSeverityFixtures()
	for index, fixture := range fixtures {
		_, err := conn.ExecContext(ctx, `INSERT INTO security_events
			(id, request_id, organization_id, api_key_id, rule_id, action, tool_name, event_type, severity, created_at)
			VALUES (?, ?, 9, 7, ?, ?, ?, ?, ?, '2026-10-01')`,
			index+1, fmt.Sprintf("request-%d", index+1), fixture.rule, fixture.action, fixture.tool, fixture.kind, fixture.before)
		if err != nil {
			t.Fatal(err)
		}
	}
	before := legacyTableSnapshot(t, ctx, conn, "security_events")
	for pass := range 2 {
		t.Run(fmt.Sprintf("forward pass %d", pass), func(t *testing.T) {
			applySecurityEventSeverityScript(t, ctx, conn, "013_security_event_severity.sql")
			assertSecurityEventSeverityFixtures(t, ctx, conn, fixtures)
		})
	}
	applySecurityEventSeverityScript(t, ctx, conn, "013_security_event_severity.down.sql")
	if after := legacyTableSnapshot(t, ctx, conn, "security_events"); after != before {
		t.Fatal("rollback did not restore the original event records")
	}
}

func applySecurityEventSeverityScript(t *testing.T, ctx context.Context, conn *sql.Conn, name string) {
	t.Helper()
	script, err := Files.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, string(script)); err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
}

func securityEventSeverityFixtures() []securityEventSeverityFixture {
	var fixtures []securityEventSeverityFixture
	for _, rule := range []struct{ id, tool string }{
		{"model_not_allowed", ""}, {"authentication_key_not_found_or_unusable", ""},
		{"invalid_provider", ""}, {"authentication_missing_credential", ""},
		{"prompt_secret", ""}, {"model_output_code", ""}, {"model_output_code", "shell"},
		{"tool_call_invalid", ""}, {"tool_call_invalid", "shell"},
		{"unauthorized_tool", ""}, {"tool_call_inspection_unavailable", ""},
		{"1", "shell"}, {"2", "shell"}, {"tool_future_rule", ""}, {"custom", "shell"},
		{"authentication_expired", ""}, {"rate_limit", ""}, {"quota_exceeded", ""},
		{"policy_not_allowed", ""}, {"12", ""}, {"unknown", ""}, {"", ""},
	} {
		for _, action := range []struct{ value, before string }{
			{"block", "high"}, {"observe", "warning"}, {"allow", "info"}, {"", "info"},
		} {
			kind, after := domain.ClassifySecurityEvent(rule.id, action.value, rule.tool)
			before := action.before
			if rule.id == "tool_call_inspection_unavailable" {
				before = "critical"
			}
			fixtures = append(fixtures, securityEventSeverityFixture{rule.id, action.value, rule.tool, kind, before, after})
		}
	}
	return fixtures
}

func assertSecurityEventSeverityFixtures(t *testing.T, ctx context.Context, conn *sql.Conn, fixtures []securityEventSeverityFixture) {
	t.Helper()
	var count int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM security_events").Scan(&count); err != nil || count != len(fixtures) {
		t.Fatalf("event count = %d, want %d: %v", count, len(fixtures), err)
	}
	for index, want := range fixtures {
		t.Run(fmt.Sprintf("event %d %s %s", index+1, want.rule, want.action), func(t *testing.T) {
			var got securityEventSeverityFixture
			var request string
			var organizationID, keyID uint64
			err := conn.QueryRowContext(ctx, `SELECT rule_id, action, tool_name, event_type, severity, request_id, organization_id, api_key_id
				FROM security_events WHERE id = ?`, index+1).
				Scan(&got.rule, &got.action, &got.tool, &got.kind, &got.after, &request, &organizationID, &keyID)
			if err != nil {
				t.Fatal(err)
			}
			got.before = want.before
			if got != want || request != fmt.Sprintf("request-%d", index+1) || organizationID != 9 || keyID != 7 {
				t.Fatalf("event = %+v, want %+v; request=%s organization=%d key=%d", got, want, request, organizationID, keyID)
			}
		})
	}
}
