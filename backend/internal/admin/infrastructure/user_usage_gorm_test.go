package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"bosapi/backend/internal/admin/domain"
)

func TestListAdminOrganizationUsagePageOmitsRankings(t *testing.T) {
	connection, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		for _, forbidden := range []string{"key_ranks", "model_ranks", "organization_ranks", "LATERAL"} {
			if strings.Contains(actual, forbidden) {
				return fmt.Errorf("page-only query includes %s", forbidden)
			}
		}
		return sqlmock.QueryMatcherRegexp.Match(expected, actual)
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: connection, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	filter := domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{OrganizationID: 9, APIKeyID: 8, Model: "example", From: &from, To: &to}, Search: "Team"}
	mock.ExpectQuery(`WITH usage_buckets AS .*SELECT \* FROM user_days ORDER BY period DESC, total_tokens DESC, organization_id ASC LIMIT \? OFFSET \?`).
		WithArgs(uint64(9), uint64(8), "example", from, to, uint64(9), uint64(8), "example", from, to, "Team", "Team", 2, 2).
		WillReturnRows(sqlmock.NewRows([]string{"period", "organization_id", "organization_name", "key_count", "requests", "input_tokens", "output_tokens", "total_tokens", "cost_amount"}).
			AddRow("2026-10-01", 9, "Team", 2, 3, 11, 7, 18, 0.00000003))
	items, err := NewAdminUsageRepository(database).ListAdminOrganizationUsagePage(context.Background(), filter, 2, 2)
	if err != nil || len(items) != 1 || items[0].OrganizationID != 9 || items[0].KeyCount != 2 || items[0].TotalTokens != 18 || items[0].CostAmount != 0.00000003 {
		t.Fatalf("items=%+v error=%v", items, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDailyAdminOrganizationUsageQueries(t *testing.T) {
	for _, test := range []struct {
		name                           string
		empty, pastEnd, fail, unscoped bool
	}{
		{name: "multiple keys and full summary"}, {name: "empty", empty: true},
		{name: "global rankings retain deleted keys", unscoped: true},
		{name: "page past end retains summary and rankings", pastEnd: true},
		{name: "dashboard error", fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Error(err)
				}
				mock.ExpectClose()
				if err := connection.Close(); err != nil {
					t.Error(err)
				}
			})
			database, err := gorm.Open(mysql.New(mysql.Config{Conn: connection, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			from := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
			to := from.Add(24 * time.Hour)
			search := "'; DROP TABLE users; --"
			filter := domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{OrganizationID: 9, APIKeyID: 8, Model: "example", From: &from, To: &to}, Search: search}
			if test.unscoped {
				filter.OrganizationID = 0
			}
			querySQL := dailyAdminOrganizationDashboardQuery(database, filter, 2, 1).Statement.SQL.String()
			for _, part := range []string{
				"DATE_FORMAT(CONVERT_TZ(usage_logs.created_at, '+00:00', '+08:00'), '%Y-%m-%d') AS period",
				"FROM usage_logs FORCE INDEX (idx_usage_key_time)",
				"LEFT JOIN organizations AS org ON org.id = buckets.organization_id",
				"COUNT(DISTINCT api_key_id) AS key_count",
				"usage_logs.created_at >= ? AND usage_logs.created_at < ?",
				"usage_logs.organization_id IN (SELECT org.id FROM organizations AS org WHERE LOCATE(?, org.name) > 0) OR CAST(usage_logs.organization_id AS CHAR) = ?",
				"GROUP BY period, usage_logs.organization_id, usage_logs.api_key_id, usage_logs.model",
				"FROM usage_buckets GROUP BY api_key_id HAVING SUM(total_tokens) > 0",
				"FROM usage_buckets GROUP BY model HAVING SUM(total_tokens) > 0",
				"ORDER BY period DESC, total_tokens DESC, organization_id ASC LIMIT ? OFFSET ?",
				"GROUP BY organization_id, organization_name HAVING SUM(total_tokens) > 0",
				"ORDER BY row_type ASC, period DESC, total_tokens DESC, organization_id ASC, api_key_id ASC, model ASC",
			} {
				if !strings.Contains(querySQL, part) {
					t.Fatalf("missing %q in %s", part, querySQL)
				}
			}
			if strings.Contains(querySQL, search) || strings.Count(querySQL, "FROM usage_logs FORCE INDEX") != 1 {
				t.Fatalf("unsafe or repeated usage scan: %s", querySQL)
			}
			failure := errors.New("database unavailable")
			dashboardQuery := mock.ExpectQuery(regexp.QuoteMeta(querySQL))
			if test.unscoped {
				dashboardQuery.WithArgs(uint64(8), "example", from, to, uint64(8), "example", from, to, search, search, 1, 1)
				if strings.Contains(querySQL, "LATERAL") || strings.Contains(querySQL, "latest_usage") || !strings.Contains(querySQL, "org.id = ranked.organization_id") {
					t.Fatalf("unexpected history lookup or lost historical scope: %s", querySQL)
				}
			} else {
				dashboardQuery.WithArgs(uint64(9), uint64(8), "example", from, to, uint64(9), uint64(8), "example", from, to, search, search, 1, 1)
			}
			if test.fail {
				dashboardQuery.WillReturnError(failure)
			} else {
				rows := sqlmock.NewRows([]string{"row_type", "period", "organization_id", "organization_name", "key_count", "total", "requests", "errors", "input_tokens", "cache_read_tokens", "cache_write_tokens", "cache_read_cost_amount", "cache_write_cost_amount", "output_tokens", "total_tokens", "cost_amount", "api_key_id", "api_key_name", "organization_count", "model"})
				if test.empty {
					rows.AddRow("summary", "", 0, "", 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, "", 0, "")
				} else {
					if !test.pastEnd {
						rows.AddRow("item", "2026-10-01", 7, "Team", 2, 0, 2, 0, 11, 3, 2, 0.003, 0.002, 7, 18, 0.1, 0, "", 0, "")
					}
					rows.AddRow("organization", "", 7, "Team", 0, 0, 0, 0, 110, 0, 0, 0, 0, 70, 180, 0, 0, "", 0, "")
					rows.AddRow("key", "", 0, "Team", 0, 0, 0, 0, 110, 0, 0, 0, 0, 70, 180, 0, 8, "Development", 1, "")
					rows.AddRow("model", "", 0, "", 0, 0, 0, 0, 110, 0, 0, 0, 0, 70, 180, 0, 0, "", 0, "example")
					rows.AddRow("summary", "", 0, "", 0, 12, 12, 1, 110, 30, 20, 0.03, 0.02, 70, 180, 0.5, 0, "", 0, "")
				}
				dashboardQuery.WillReturnRows(rows)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := NewAdminUsageRepository(database).DailyAdminOrganizationUsage(ctx, filter, 2, 1)
			if test.fail {
				if !errors.Is(err, failure) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil || result.Items == nil || result.TopOrganizations == nil || result.TopKeys == nil || result.TopModels == nil {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if test.empty {
				if result.Total != 0 || len(result.Items) != 0 || len(result.TopOrganizations) != 0 || len(result.TopKeys) != 0 || len(result.TopModels) != 0 || result.Summary != (domain.AdminUsagePoint{}) {
					t.Fatalf("result=%+v", result)
				}
				return
			}
			if result.Total != 12 || result.Summary.Requests != 12 || result.Summary.Errors != 1 || result.Summary.TotalTokens != 180 || result.Summary.CostAmount != 0.5 || len(result.TopOrganizations) != 1 || result.TopOrganizations[0].OrganizationID != 7 || result.TopOrganizations[0].TotalTokens != 180 || result.TopOrganizations[0].InputTokens != 110 || result.TopOrganizations[0].OutputTokens != 70 || len(result.TopKeys) != 1 || result.TopKeys[0].TotalTokens != 180 || result.TopKeys[0].APIKeyID != 8 {
				t.Fatalf("result=%+v", result)
			}
			if result.Summary.CacheReadTokens != 30 || result.Summary.CacheWriteTokens != 20 || result.Summary.CacheReadCostAmount != 0.03 || result.Summary.CacheWriteCostAmount != 0.02 {
				t.Fatalf("summary cache metrics=%+v", result.Summary)
			}
			if test.pastEnd {
				if len(result.Items) != 0 {
					t.Fatalf("page beyond total returned items: %+v", result.Items)
				}
			} else if len(result.Items) != 1 || result.Items[0].Period != "2026-10-01" || result.Items[0].TotalTokens != 18 || result.Items[0].KeyCount != 2 || result.Items[0].OrganizationID != 7 {
				t.Fatalf("items=%+v", result.Items)
			}
			if !test.pastEnd && (result.Items[0].CacheReadTokens != 3 || result.Items[0].CacheWriteTokens != 2 || result.Items[0].CacheReadCostAmount != 0.003 || result.Items[0].CacheWriteCostAmount != 0.002) {
				t.Fatalf("item cache metrics=%+v", result.Items[0])
			}
			if len(result.TopModels) != 1 || result.TopModels[0].Model != "example" || result.TopModels[0].InputTokens != 110 || result.TopModels[0].OutputTokens != 70 || result.TopModels[0].TotalTokens != 180 {
				t.Fatalf("model ranking=%+v", result.TopModels)
			}
			if result.TopKeys[0].OrganizationName != "Team" || result.TopKeys[0].OrganizationCount != 1 {
				t.Fatalf("key organization=%+v", result.TopKeys[0])
			}
		})
	}
}

func TestAdminDashboardIndexSelection(t *testing.T) {
	connection, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: connection, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 25, 16, 0, 0, 0, time.UTC)
	to, wide := from.Add(24*time.Hour), from.Add(7*24*time.Hour)
	for _, test := range []struct {
		name   string
		filter domain.AdminUsageFilter
		index  string
	}{
		{"day", domain.AdminUsageFilter{From: &from, To: &to}, "idx_usage_logs_created_at"},
		{"key overrides org and model", domain.AdminUsageFilter{From: &from, To: &to, APIKeyID: 8, OrganizationID: 9, Model: "model"}, "idx_usage_key_time"},
		{"org overrides model", domain.AdminUsageFilter{From: &from, To: &to, OrganizationID: 9, Model: "model"}, "idx_usage_org_time"},
		{"model", domain.AdminUsageFilter{From: &from, To: &to, Model: "model"}, "idx_usage_model_time"},
		{"wide", domain.AdminUsageFilter{From: &from, To: &wide}, ""},
		{"wide scoped", domain.AdminUsageFilter{From: &from, To: &wide, APIKeyID: 8}, ""},
		{"open start", domain.AdminUsageFilter{To: &to}, ""},
		{"open end", domain.AdminUsageFilter{From: &from}, ""},
		{"empty", domain.AdminUsageFilter{From: &from, To: &from}, ""},
		{"reversed", domain.AdminUsageFilter{From: &to, To: &from}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := adminDashboardUsageQuery(database, test.filter).Find(&[]adminUsageRecord{}).Statement.SQL.String()
			if test.index == "" {
				if strings.Contains(query, "FORCE INDEX") {
					t.Fatalf("unexpected hint: %s", query)
				}
			} else if !strings.Contains(query, "FORCE INDEX ("+test.index+")") {
				t.Fatalf("missing %s: %s", test.index, query)
			}
			// This dashboard optimization must not change other admin usage reads.
			if query := adminUsageQuery(database, test.filter).Find(&[]adminUsageRecord{}).Statement.SQL.String(); strings.Contains(query, "FORCE INDEX") {
				t.Fatalf("hint escaped dashboard: %s", query)
			}
		})
	}
}

func TestDailyAdminOrganizationUsageCanceledContext(t *testing.T) {
	for _, test := range []struct {
		name     string
		deadline bool
	}{
		{name: "canceled"}, {name: "deadline exceeded", deadline: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Error(err)
				}
				mock.ExpectClose()
				if err := connection.Close(); err != nil {
					t.Error(err)
				}
			})
			database, err := gorm.Open(mysql.New(mysql.Config{Conn: connection, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			want := context.Canceled
			if test.deadline {
				ctx, cancel = context.WithDeadline(context.Background(), time.Unix(0, 0))
				defer cancel()
				want = context.DeadlineExceeded
			}
			from := time.Date(2026, 10, 26, 16, 0, 0, 0, time.UTC)
			to := from.Add(24 * time.Hour)
			filter := domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &to}}
			_, err = NewAdminUsageRepository(database).DailyAdminOrganizationUsage(ctx, filter, 1, 20)
			if !errors.Is(err, want) {
				t.Fatalf("error=%v want=%v", err, want)
			}
		})
	}
}
