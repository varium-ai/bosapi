package infrastructure

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"bosapi/backend/internal/platform/geoip"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"bosapi/backend/internal/admin/domain"
)

func TestSecurityEventsCombinedFiltersPreserveMetadata(t *testing.T) {
	for _, toolName := range []string{"exec_command", "'; DROP TABLE security_events; --"} {
		t.Run(toolName, func(t *testing.T) {
			sqlDatabase, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDatabase.Close() })
			database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			filter := domain.SecurityEventFilter{OrganizationID: 9, APIKeyID: 7, RuleID: "baseline", Action: "block", ToolName: toolName, ClientIP: "192.0.2.42", EventType: "tool_call", Severity: "high"}
			where := " FROM `security_events` WHERE organization_id = ? AND api_key_id = ? AND rule_id = ? AND action = ? AND tool_name = ? AND client_ip = INET6_ATON(?) AND event_type = ? AND severity = ?"
			mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*)"+where)).
				WithArgs(9, 7, "baseline", "block", toolName, "192.0.2.42", "tool_call", "high").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			mock.ExpectQuery("SELECT security_events\\.\\*"+regexp.QuoteMeta(where+" ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?")).
				WithArgs(9, 7, "baseline", "block", toolName, "192.0.2.42", "tool_call", "high", 10, 10).
				WillReturnRows(sqlmock.NewRows([]string{"id", "client_ip", "tool_name", "tool_protocol", "tool_direction", "call_id_hash", "rule_version", "rule_scope", "rule_sensitivity", "event_type", "severity"}).
					AddRow(1, []byte(net.ParseIP("192.0.2.42").To4()), toolName, "responses", "response", "hash", 3, "global", "high", "tool_call", "high"))
			items, total, err := NewSecurityEventRepository(database).ListSecurityEvents(context.Background(), filter, 2, 10)
			if err != nil {
				t.Fatal(err)
			}
			if total != 1 || len(items) != 1 {
				t.Fatalf("items=%v total=%d", items, total)
			}
			event := items[0]
			if event.ClientIP != "192.0.2.0" || event.ToolName != toolName || event.ToolProtocol != "responses" || event.ToolDirection != "response" || event.CallIDHash != "hash" || event.RuleVersion != 3 || event.RuleScope != "global" || event.RuleSensitivity != "high" || event.EventType != "tool_call" || event.Severity != "high" {
				t.Fatalf("metadata=%+v", event)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSecurityEventsReadSnapshotNamesWithoutRuleHistory(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDatabase.Close() })
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	rows := sqlmock.NewRows([]string{"id", "organization_id", "rule_id", "rule_version", "rule_scope", "rule_name_snapshot"}).
		AddRow(1, 9, "prompt_secret", 2, "builtin", "Prompt credential check").
		AddRow(2, 9, "12", 7, "organization", "Historical snapshot").
		AddRow(3, 9, "12", 6, "organization", "")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `security_events` WHERE organization_id = ?")).WithArgs(9).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	// The community database has no rule history or webhook tables. Only this SELECT may run.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT security_events.* FROM `security_events` WHERE organization_id = ? ORDER BY created_at DESC, id DESC LIMIT ?")).
		WithArgs(9, 20).WillReturnRows(rows)
	items, total, err := NewSecurityEventRepository(database).ListSecurityEvents(context.Background(), domain.SecurityEventFilter{OrganizationID: 9}, 1, 20)
	if err != nil || total != 3 || len(items) != 3 {
		t.Fatalf("items=%v total=%d err=%v", items, total, err)
	}
	for index, name := range []string{"Prompt credential check", "Historical snapshot", ""} {
		if items[index].RuleName != name {
			t.Fatalf("event %d name=%q want=%q", index, items[index].RuleName, name)
		}
	}
	if items[1].RuleID != "12" || items[1].RuleVersion != 7 || items[1].RuleScope != "organization" {
		t.Fatalf("lost snapshot identity: %+v", items[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFormatClientIPIncludesCountryAndMasksIPv4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "geoip.json")
	if err := os.WriteFile(path, []byte(`[{"cidr":"125.85.178.0/24","city":"Unknown","country":"China"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	resolver, err := geoip.New(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := formatClientIP("125.85.178.42", resolver); got != "125.85.178.0(China)" {
		t.Fatalf("formatted IP = %q, want 125.85.178.0(China)", got)
	}
	if got := formatClientIP("125.85.178.42", nil); got != "125.85.178.0" {
		t.Fatalf("masked IP = %q, want 125.85.178.0", got)
	}
}
