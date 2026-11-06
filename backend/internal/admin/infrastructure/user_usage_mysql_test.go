package infrastructure

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/testusage"
	"bosapi/backend/internal/platform/usagerollup"
)

// Set BOS_USAGE_DASHBOARD_TEST_DSN to a MySQL 8 test server with permission to
// create databases. All fixtures live in a randomly named, disposable database.
func newAdminDashboardMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("BOS_USAGE_DASHBOARD_TEST_DSN")
	if dsn == "" {
		t.Skip("set BOS_USAGE_DASHBOARD_TEST_DSN to verify dashboard SQL against MySQL")
	}
	config, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.DBName = ""
	config.ParseTime = true
	config.MultiStatements = true
	root, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema := "usage_dashboard_test_" + rand.Text()
	if _, err := root.ExecContext(ctx, "CREATE DATABASE `"+schema+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := root.ExecContext(cleanup, "DROP DATABASE `"+schema+"`"); err != nil {
			t.Error(err)
		}
	})
	config.DBName = schema
	database, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	for _, statement := range []string{
		`CREATE TABLE organizations (id BIGINT UNSIGNED PRIMARY KEY, name VARCHAR(191) NOT NULL)`,
		`CREATE TABLE api_keys (id BIGINT UNSIGNED PRIMARY KEY, name VARCHAR(191) NOT NULL, organization_id BIGINT UNSIGNED NOT NULL)`,
	} {
		if err := database.WithContext(ctx).Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	testusage.Install(t, database)
	testusage.LegacyDefaults(t, database)
	return database
}

func TestAdminOrganizationDashboardMySQL(t *testing.T) {
	database := newAdminDashboardMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, statement := range []string{
		`INSERT INTO organizations VALUES (1, 'Alpha'), (2, 'Beta')`,
		`INSERT INTO api_keys VALUES (10, 'Transferred', 2), (12, 'Active', 1), (13, 'Other', 2), (15, 'Missing current organization', 9)`,
		`INSERT INTO usage_logs (organization_id, api_key_id, model, status_code, input_tokens, output_tokens, total_tokens, cost_amount, created_at, client_ip) VALUES
			(1, 10, 'model-a', 200, 900, 100, 1000, 9.99, '2026-09-30 15:59:59.999', INET6_ATON('192.0.2.1')),
			(1, 10, 'model-a', 200, 60, 40, 100, 0.00000001, '2026-09-30 16:00:00', INET6_ATON('192.0.2.1')),
			(1, 11, 'model-b', 500, 70, 30, 100, 0.00000002, '2026-10-01 15:59:59.999', INET6_ATON('192.0.2.2')),
			(2, 10, 'model-a', 200, 120, 80, 200, 0.12345678, '2026-10-01 15:59:59', INET6_ATON('192.0.2.1')),
			(1, 12, 'Model-A', 200, 110, 90, 200, 0.00000003, '2026-10-01 16:00:00', INET6_ATON('192.0.2.1')),
			(1, 12, '', 429, 0, 0, 0, 0, '2026-10-01 17:00:00', INET6_ATON('192.0.2.2')),
			(2, 13, 'model-b', 200, 150, 50, 200, 0.10000001, '2026-10-02 15:59:59.999', NULL),
			(9, 14, '', 200, 5000000000, 50, 5000000050, 100000000.00000001, '2026-10-02 15:00:00', NULL),
			(1, 12, 'model-a', 200, 900, 100, 1000, 9.99, '2026-10-02 16:00:00', NULL),
            (1, 12, 'model-b', 200, 10, 10, 20, 0.00000001, '2026-10-01 18:00:00', NULL),
            (1, 15, 'model-c', 200, 10, 10, 20, 0.00000001, '2026-10-01 18:00:00', NULL),
            (9, 16, '', 500, 0, 0, 0, 0, '2026-09-30 18:00:00', NULL),
            (1, 11, 'model-c', 200, 10, 10, 20, 0.00000001, '2026-10-02 16:00:01', NULL),
            (2, 11, 'model-c', 200, 10, 10, 20, 0.00000001, '2026-10-02 16:00:01', NULL)`,
	} {
		if err := database.WithContext(ctx).Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	from := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	day, to := from.Add(24*time.Hour), from.Add(48*time.Hour)
	partial := from.Add(time.Millisecond)
	tests := []struct {
		name   string
		filter domain.AdminOrganizationUsageFilter
		page   int
	}{
		{"two days and decimal costs", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &to}}, 1},
		{"Shanghai midnight and time index", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &day}}, 1},
		{"second page", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &to}}, 2},
		{"past last page", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &to}}, 100},
		{"org index", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &day, OrganizationID: 1}}, 1},
		{"transferred key", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &day, APIKeyID: 10}}, 1},
		{"model index and collation", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &day, To: &to, Model: "model-a"}}, 1},
		{"combined filters", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &day, OrganizationID: 1, APIKeyID: 10, Model: "model-a", ClientIP: "192.0.2.1"}}, 1},
		{"partial day uses exact timestamps", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &partial, To: &to}}, 1},
		{"deleted key", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &day, APIKeyID: 11}}, 1},
		{"deleted organization", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &day, To: &to, OrganizationID: 9}}, 1},
		{"search by name", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &to}, Search: "Alpha"}, 1},
		{"search deleted organization by ID", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &to}, Search: "9"}, 1},
		{"zero-token-only organization day", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &day, OrganizationID: 9}}, 1},
		{"deleted current organization fallback", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &day, To: &to, APIKeyID: 15}}, 1},
		{"search by ID", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &to}, Search: "2"}, 1},
		{"empty", domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &to}, Search: "' OR 1=1 --"}, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			want := legacyOrganizationDashboard(t, database.WithContext(ctx), test.filter, test.page, 2)
			got, err := NewAdminUsageRepository(database).DailyAdminOrganizationUsage(ctx, test.filter, test.page, 2)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("dashboard changed:\n got: %+v\nwant: %+v", got, want)
			}
			items, err := NewAdminUsageRepository(database).ListAdminOrganizationUsagePage(ctx, test.filter, test.page, 2)
			if err != nil || !reflect.DeepEqual(items, want.Items) {
				t.Fatalf("page-only query changed: got=%+v want=%+v error=%v", items, want.Items, err)
			}
		})
	}
	// Verify that the shared CTE does not duplicate the source-table access.
	query := dailyAdminOrganizationDashboardQuery(database, domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &day}}, 1, 2).Statement
	var plan string
	if err := database.WithContext(ctx).Raw("EXPLAIN FORMAT=JSON "+query.SQL.String(), query.Vars...).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Count(plan, `"key": "idx_usage_logs_created_at"`) != 2 {
		t.Fatalf("expected two bounded physical usage scans: %s", plan)
	}
	installUsageRollups(t, database)
	for _, stage := range []struct {
		name  string
		batch int
	}{{"pending history", 0}, {"partly materialized", 3}, {"fully materialized", 5000}} {
		if stage.batch > 0 {
			if _, err := usagerollup.New(database).Batch(ctx, stage.batch); err != nil {
				t.Fatal(err)
			}
		}
		t.Run(stage.name, func(t *testing.T) {
			preaggregated := NewPreaggregatedAdminUsageRepository(database)
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					want := legacyOrganizationDashboard(t, database.WithContext(ctx), test.filter, test.page, 2)
					got, err := preaggregated.DailyAdminOrganizationUsage(ctx, test.filter, test.page, 2)
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("rollup changed dashboard: got=%+v want=%+v error=%v", got, want, err)
					}
					items, err := preaggregated.ListAdminOrganizationUsagePage(ctx, test.filter, test.page, 2)
					if err != nil || !reflect.DeepEqual(items, want.Items) {
						t.Fatalf("rollup changed page: got=%+v want=%+v error=%v", items, want.Items, err)
					}
					for _, period := range []string{"total", "daily", "weekly", "monthly"} {
						want, err := NewAdminUsageRepository(database).AggregateAdminUsage(ctx, test.filter.AdminUsageFilter, period)
						if err != nil {
							t.Fatal(err)
						}
						got, err := preaggregated.AggregateAdminUsage(ctx, test.filter.AdminUsageFilter, period)
						if err != nil || !reflect.DeepEqual(got, want) {
							t.Fatalf("%s rollup changed aggregate: got=%+v want=%+v error=%v", period, got, want, err)
						}
					}
				})
			}
		})
	}
}

func TestAdminOrganizationDashboardCacheMetricsMySQL(t *testing.T) {
	database := newAdminDashboardMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, statement := range []string{
		`INSERT INTO organizations VALUES (1, 'Alpha'), (2, 'Beta')`,
		`INSERT INTO api_keys VALUES (10, 'Alpha key', 1), (20, 'Beta key', 2)`,
		`INSERT INTO usage_logs (organization_id, api_key_id, model, input_tokens, output_tokens, total_tokens,
			cache_read_tokens, cache_write_tokens, cache_read_cost_amount, cache_write_cost_amount, cost_amount, created_at) VALUES
			(1, 10, 'model-a', 60, 40, 100, 11, 13, 0.01, 0.02, 0.1, '2026-11-05 00:00:00'),
			(2, 20, 'model-b', 120, 80, 200, 17, 19, 0.03, 0.04, 0.2, '2026-11-05 01:00:00')`,
		`INSERT INTO usage_logs_v2 SELECT * FROM usage_logs WHERE organization_id = 2`,
		`DELETE FROM usage_logs WHERE organization_id = 2`,
	} {
		if err := database.WithContext(ctx).Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	from := time.Date(2026, 11, 4, 16, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	for _, stage := range []string{"raw", "pending rollups", "materialized rollups"} {
		if stage == "materialized rollups" {
			if _, err := usagerollup.New(database).Batch(ctx, 100); err != nil {
				t.Fatal(err)
			}
		}
		repository := NewAdminUsageRepository(database)
		if stage != "raw" {
			repository = NewPreaggregatedAdminUsageRepository(database)
		}
		for _, organizationID := range []uint64{0, 1, 2} {
			t.Run(stage+"/organization_"+fmt.Sprint(organizationID), func(t *testing.T) {
				filter := domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{OrganizationID: organizationID, From: &from, To: &to}}
				got, err := repository.DailyAdminOrganizationUsage(ctx, filter, 1, 20)
				if err != nil {
					t.Fatal(err)
				}
				alpha := domain.AdminUsagePoint{Requests: 1, InputTokens: 60, OutputTokens: 40, TotalTokens: 100, CacheReadTokens: 11, CacheWriteTokens: 13, CacheReadCostAmount: 0.01, CacheWriteCostAmount: 0.02, CostAmount: 0.1}
				beta := domain.AdminUsagePoint{Requests: 1, InputTokens: 120, OutputTokens: 80, TotalTokens: 200, CacheReadTokens: 17, CacheWriteTokens: 19, CacheReadCostAmount: 0.03, CacheWriteCostAmount: 0.04, CostAmount: 0.2}
				want := domain.AdminUsagePoint{Requests: 2, InputTokens: 180, OutputTokens: 120, TotalTokens: 300, CacheReadTokens: 28, CacheWriteTokens: 32, CacheReadCostAmount: 0.04, CacheWriteCostAmount: 0.06, CostAmount: 0.3}
				count := 2
				if organizationID > 0 {
					count = 1
					want = alpha
					if organizationID == 2 {
						want = beta
					}
				}
				if got.Summary != want || got.Total != int64(count) || len(got.Items) != count || len(got.TopOrganizations) != count || len(got.TopKeys) != count || len(got.TopModels) != count {
					t.Fatalf("dashboard=%+v; want summary=%+v and %d items/ranks", got, want, count)
				}
				for i, item := range got.Items {
					wantPoint, keyID, model := alpha, uint64(10), "model-a"
					if item.OrganizationID == 2 {
						wantPoint, keyID, model = beta, 20, "model-b"
					}
					wantPoint.Period = "2026-11-05"
					if item.AdminUsagePoint != wantPoint || item.KeyCount != 1 || got.TopOrganizations[i].OrganizationID != item.OrganizationID || got.TopOrganizations[i].TotalTokens != wantPoint.TotalTokens || got.TopKeys[i].APIKeyID != keyID || got.TopKeys[i].TotalTokens != wantPoint.TotalTokens || got.TopKeys[i].InputTokens != wantPoint.InputTokens || got.TopKeys[i].OutputTokens != wantPoint.OutputTokens || got.TopModels[i].Model != model || got.TopModels[i].TotalTokens != wantPoint.TotalTokens {
						t.Fatalf("item or ranking columns shifted: %+v", got)
					}
				}
			})
		}
	}
}

func installUsageRollups(t *testing.T, database *gorm.DB) {
	t.Helper()
	// Installed with the production transition schema by newAdminDashboardMySQL.
}

func TestAdminDashboardKeyHistoryLookupMySQL(t *testing.T) {
	database := newAdminDashboardMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, statement := range []string{
		`INSERT INTO organizations VALUES (1, 'Legacy'), (2, 'Current'), (3, 'Partitioned'), (4, 'Rollup')`,
		`INSERT INTO api_keys VALUES (1, 'Transferred', 2), (2, 'Missing organization', 99)`,
		// A current organization must not make the fallback scan the entire
		// history looking for a row that satisfies a false outer predicate.
		`INSERT INTO usage_logs (organization_id, api_key_id, total_tokens, cost_amount, created_at, rollup_applied)
		 WITH RECURSIVE sequence AS (
			SELECT 1 AS n UNION ALL SELECT n + 1 FROM sequence WHERE n < 128
		 ) SELECT 1, 1, 2, 0, TIMESTAMPADD(SECOND, n, '2026-10-01 00:00:00'), 1 FROM sequence`,
		`INSERT INTO usage_logs (organization_id, api_key_id, total_tokens, cost_amount, created_at, rollup_applied) VALUES
		 (1, 2, 2, 0, '2026-10-02 00:00:00', 1),
		 (1, 3, 2, 0, '2026-10-01 00:00:00', 1),
		 (1, 4, 2, 0, '2026-10-04 00:00:00', 1)`,
		`INSERT INTO usage_logs_v2 SELECT * FROM usage_logs`,
		`UPDATE usage_logs_v2 SET organization_id = 3, created_at = '2026-10-03 00:00:00' WHERE api_key_id = 2`,
		`UPDATE usage_logs_v2 SET organization_id = 3, created_at = '2026-10-02 00:00:00' WHERE api_key_id IN (3, 4)`,
		`INSERT INTO usage_daily_rollups
		 SELECT DATE_ADD('2026-01-01', INTERVAL id DAY), 1, 1, 'history', 1, 0, 1, 1, 2, 0
		 FROM usage_logs WHERE api_key_id = 1`,
		`INSERT INTO usage_daily_rollups VALUES
		 ('2026-10-01', 4, 2, 'history', 1, 0, 1, 1, 2, 0),
		 ('2026-10-05', 4, 3, 'history', 1, 0, 1, 1, 2, 0),
		 ('2026-10-01', 4, 4, 'history', 1, 0, 1, 1, 2, 0)`,
	} {
		if err := database.WithContext(ctx).Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	query := database.Raw(`WITH key_ranks AS (
		SELECT api_key_id, MIN(organization_id) AS organization_id, COUNT(DISTINCT organization_id) AS organization_count FROM usage_logs GROUP BY api_key_id
	) SELECT * FROM (?) AS key_items ORDER BY api_key_id`, adminDashboardKeyItems(database, 0)).Statement
	var keys []domain.AdminKeyTokenRank
	if err := database.WithContext(ctx).Raw(query.SQL.String(), query.Vars...).Scan(&keys).Error; err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"Legacy", "Legacy", "Legacy", "Legacy"} {
		if len(keys) != 4 || keys[i].APIKeyID != uint64(i+1) || keys[i].OrganizationName != name {
			t.Fatalf("historical organization attribution changed: %+v", keys)
		}
	}
	var plan string
	if err := database.WithContext(ctx).Raw("EXPLAIN ANALYZE "+query.SQL.String(), query.Vars...).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan, "latest_usage") || strings.Contains(query.SQL.String(), "LATERAL") {
		t.Fatalf("name enrichment still visits raw history: %s", plan)
	}

}

// The previous four-query implementation is a reference for pagination,
// decimal aggregation and ordering; it deliberately leaves index choice alone.
func legacyOrganizationDashboard(t *testing.T, database *gorm.DB, filter domain.AdminOrganizationUsageFilter, page, size int) domain.AdminOrganizationUsageDashboard {
	t.Helper()
	grouped := adminOrganizationUsageSearch(adminUsageQuery(database, filter.AdminUsageFilter).
		Select("DATE_FORMAT(CONVERT_TZ(usage_logs.created_at, '+00:00', '+08:00'), '%Y-%m-%d') AS period, usage_logs.organization_id, COALESCE(org.name, '') AS organization_name, COUNT(DISTINCT usage_logs.api_key_id) AS key_count, COUNT(*) AS requests, COALESCE(SUM(usage_logs.status_code >= 400), 0) AS errors, COALESCE(SUM(usage_logs.input_tokens), 0) AS input_tokens, COALESCE(SUM(usage_logs.output_tokens), 0) AS output_tokens, COALESCE(SUM(usage_logs.total_tokens), 0) AS total_tokens, COALESCE(SUM(usage_logs.cost_amount), 0) AS cost_amount").
		Joins("LEFT JOIN organizations AS org ON org.id = usage_logs.organization_id").
		Group("period, usage_logs.organization_id, org.name"), filter.Search)
	result := domain.AdminOrganizationUsageDashboard{Items: []domain.AdminOrganizationUsagePoint{}, TopOrganizations: []domain.AdminOrganizationTokenRank{}}
	if err := database.Table("(?) AS user_days", grouped).Count(&result.Total).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Table("(?) AS user_days", grouped).
		Select("COALESCE(SUM(requests), 0) AS requests, COALESCE(SUM(errors), 0) AS errors, COALESCE(SUM(input_tokens), 0) AS input_tokens, COALESCE(SUM(output_tokens), 0) AS output_tokens, COALESCE(SUM(total_tokens), 0) AS total_tokens, COALESCE(SUM(cost_amount), 0) AS cost_amount").Scan(&result.Summary).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Table("(?) AS user_days", grouped).Order("period DESC, total_tokens DESC, organization_id ASC").Offset((page - 1) * size).Limit(size).Scan(&result.Items).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Table("(?) AS user_days", grouped).
		Select("organization_id, organization_name, SUM(input_tokens) AS input_tokens, SUM(output_tokens) AS output_tokens, SUM(total_tokens) AS total_tokens").
		Group("organization_id, organization_name").Having("SUM(total_tokens) > 0").Order("total_tokens DESC, organization_id ASC").Limit(20).Scan(&result.TopOrganizations).Error; err != nil {
		t.Fatal(err)
	}
	var err error
	repository := NewAdminUsageRepository(database)
	if result.TopKeys, err = repository.legacyTopAdminKeyUsage(context.Background(), filter); err != nil {
		t.Fatal(err)
	}
	if result.TopModels, err = repository.legacyTopAdminModelUsage(context.Background(), filter); err != nil {
		t.Fatal(err)
	}
	return result
}

func (repository *AdminUsageRepository) legacyTopAdminModelUsage(ctx context.Context, filter domain.AdminOrganizationUsageFilter) ([]domain.AdminModelTokenRank, error) {
	items := make([]domain.AdminModelTokenRank, 0)
	query := adminDashboardUsageQuery(repository.database.WithContext(ctx), filter.AdminUsageFilter).
		Select("usage_logs.model, COALESCE(SUM(usage_logs.input_tokens), 0) AS input_tokens, COALESCE(SUM(usage_logs.output_tokens), 0) AS output_tokens, COALESCE(SUM(usage_logs.total_tokens), 0) AS total_tokens").
		Joins("LEFT JOIN organizations AS org ON org.id = usage_logs.organization_id").
		Group("usage_logs.model").Having("SUM(usage_logs.total_tokens) > 0").
		Order("total_tokens DESC, usage_logs.model ASC").Limit(20)
	err := adminOrganizationUsageSearch(query, filter.Search).Scan(&items).Error
	return items, err
}

func (repository *AdminUsageRepository) legacyTopAdminKeyUsage(ctx context.Context, filter domain.AdminOrganizationUsageFilter) ([]domain.AdminKeyTokenRank, error) {
	items := make([]domain.AdminKeyTokenRank, 0)
	organizationName := "CASE WHEN COUNT(DISTINCT usage_logs.organization_id) = 1 THEN COALESCE(MAX(org.name), CONCAT('Organization #', MIN(usage_logs.organization_id))) ELSE '' END"
	query := adminDashboardUsageQuery(repository.database.WithContext(ctx), filter.AdminUsageFilter).
		Select("usage_logs.api_key_id, COALESCE(api_key.name, '') AS api_key_name, " + organizationName + " AS organization_name, COUNT(DISTINCT usage_logs.organization_id) AS organization_count, COALESCE(SUM(usage_logs.input_tokens), 0) AS input_tokens, COALESCE(SUM(usage_logs.output_tokens), 0) AS output_tokens, COALESCE(SUM(usage_logs.total_tokens), 0) AS total_tokens").
		Joins("LEFT JOIN api_keys AS api_key ON api_key.id = usage_logs.api_key_id").
		Joins("LEFT JOIN organizations AS org ON org.id = usage_logs.organization_id")
	if filter.OrganizationID == 0 {
		query = query.Joins("LEFT JOIN organizations AS current_org ON current_org.id = api_key.organization_id")
	}
	query = query.
		Group("usage_logs.api_key_id, api_key.name").Having("SUM(usage_logs.total_tokens) > 0").
		Order("total_tokens DESC, usage_logs.api_key_id ASC").Limit(20)
	err := adminOrganizationUsageSearch(query, filter.Search).Scan(&items).Error
	return items, err
}

func adminOrganizationUsageSearch(query *gorm.DB, search string) *gorm.DB {
	if search != "" {
		return query.Where("LOCATE(?, org.name) > 0 OR CAST(usage_logs.organization_id AS CHAR) = ?", search, search)
	}
	return query
}
