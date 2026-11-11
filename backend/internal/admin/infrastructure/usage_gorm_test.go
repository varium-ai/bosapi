package infrastructure

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestAdminUsageIncludesSplitTokens(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewAdminUsageRepository(database)
	mock.ExpectQuery("SELECT count.*FROM usage_logs_read AS usage_logs").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT .*organization_name.*api_key_name.*FROM usage_logs_read AS usage_logs.*LEFT JOIN organizations AS org.*LEFT JOIN api_keys AS api_key").WillReturnRows(sqlmock.NewRows([]string{"id", "organization_name", "api_key_name", "input_tokens", "output_tokens", "total_tokens", "created_at"}).AddRow(1, "Platform", "example-key", 11, 7, 18, time.Now()))
	items, _, err := repository.ListAdminUsage(context.Background(), domain.AdminUsageFilter{}, 1, 20)
	if err != nil || len(items) != 1 || items[0].OrganizationName != "Platform" || items[0].APIKeyName != "example-key" || items[0].InputTokens != 11 || items[0].OutputTokens != 7 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	for _, period := range []string{"total", "daily", "weekly", "monthly"} {
		mock.ExpectQuery("SELECT .*COALESCE\\(SUM\\(input_tokens\\), 0\\) AS input_tokens, COALESCE\\(SUM\\(cache_read_tokens\\), 0\\) AS cache_read_tokens, COALESCE\\(SUM\\(cache_write_tokens\\), 0\\) AS cache_write_tokens, COALESCE\\(SUM\\(cache_read_cost_amount\\), 0\\) AS cache_read_cost_amount, COALESCE\\(SUM\\(cache_write_cost_amount\\), 0\\) AS cache_write_cost_amount, COALESCE\\(SUM\\(output_tokens\\), 0\\) AS output_tokens.*FROM usage_logs_read AS usage_logs").WillReturnRows(sqlmock.NewRows([]string{"period", "requests", "errors", "input_tokens", "cache_read_tokens", "cache_write_tokens", "cache_read_cost_amount", "cache_write_cost_amount", "output_tokens", "total_tokens", "cost_amount"}).AddRow("2026-10-08", 1, 0, 11, 4, 2, 0.0001, 0.0002, 7, 18, 0.01))
		points, err := repository.AggregateAdminUsage(context.Background(), domain.AdminUsageFilter{}, period)
		if err != nil || len(points) != 1 || points[0].InputTokens != 11 || points[0].CacheReadTokens != 4 || points[0].CacheWriteTokens != 2 || points[0].OutputTokens != 7 {
			t.Fatalf("period=%s points=%+v err=%v", period, points, err)
		}
		encoded, _ := json.Marshal(points[0])
		if !strings.Contains(string(encoded), `"input_tokens":11`) || !strings.Contains(string(encoded), `"cache_read_tokens":4`) || !strings.Contains(string(encoded), `"cache_write_tokens":2`) || !strings.Contains(string(encoded), `"output_tokens":7`) {
			t.Fatalf("JSON lacks cache split tokens: %s", encoded)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAdminErrorsIncludeModelAndEndpointWithinTenantScope(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC()
	filter := domain.AdminErrorFilter{OrganizationID: 11, APIKeyID: 22, Model: "gpt-4o", From: end.Add(-5 * time.Minute), To: end}
	statement := adminErrorPageQuery(database.Session(&gorm.Session{DryRun: true}), filter, 1).Find(&[]adminUsageRecord{}).Statement
	query := statement.SQL.String()
	if strings.Contains(query, "usage_logs_read") || strings.Count(query, "organization_id = ?") != 2 || strings.Count(query, "api_key_id = ?") != 2 || strings.Count(query, "model = ?") != 2 || strings.Count(query, "LIMIT ?") != 3 {
		t.Fatalf("error paging must bound and scope both physical branches: %s", query)
	}
	if strings.Count(query, "created_at >= ? AND created_at < ?") != 2 {
		t.Fatalf("time window must be inside both physical branches: %s", query)
	}
	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs(400, filter.From, filter.To, 11, 22, "gpt-4o", "security_blocked", "security_inspection_unavailable", "tool_call_inspection_unavailable", "tool_call_invalid", "unauthorized_tool", 2,
			400, filter.From, filter.To, 11, 22, "gpt-4o", "security_blocked", "security_inspection_unavailable", "tool_call_inspection_unavailable", "tool_call_invalid", "unauthorized_tool", 2, 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "request_id", "organization_id", "api_key_id", "model", "endpoint", "status_code", "error_code", "duration_ms", "created_at"}).
			AddRow(9, "req-failed", 11, 22, "deepseek-v4-pro", "chat/completions", 502, "provider_unavailable", 20289, time.Now()))
	items, more, err := NewAdminErrorRepository(database).ListAdminErrors(context.Background(), filter, 1)
	if err != nil || more || len(items) != 1 {
		t.Fatalf("items=%+v more=%v err=%v", items, more, err)
	}
	if items[0].Model != "deepseek-v4-pro" || items[0].Endpoint != "chat/completions" || items[0].ErrorCode != "provider_unavailable" {
		t.Fatalf("missing failure context: %+v", items[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
