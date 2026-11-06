package infrastructure

import (
	"context"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/usagerollup"
)

// This opt-in test uses only a disposable schema and logs relative timings;
// it deliberately avoids unstable wall-clock pass/fail thresholds.
func TestAdminOrganizationDashboardMySQLVolume(t *testing.T) {
	if os.Getenv("BOS_USAGE_DASHBOARD_PERF") != "1" {
		t.Skip("set BOS_USAGE_DASHBOARD_PERF=1 for the 200,000-request comparison")
	}
	database := newAdminDashboardMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	numbers := `SELECT a.n + 10*b.n + 100*c.n + 1000*d.n + 10000*e.n + 100000*f.n AS n
        FROM digits a CROSS JOIN digits b CROSS JOIN digits c CROSS JOIN digits d CROSS JOIN digits e CROSS JOIN digits f`
	for _, statement := range []string{
		"CREATE TABLE digits (n INT PRIMARY KEY)",
		"INSERT INTO digits VALUES (0),(1),(2),(3),(4),(5),(6),(7),(8),(9)",
		`INSERT INTO organizations SELECT n+1, CONCAT('Organization ', n+1) FROM (` + numbers + `) seq WHERE n < 100`,
		// Half of the 5,000 keys are deleted and need historical attribution.
		`INSERT INTO api_keys SELECT n+1, CONCAT('Key ', n+1), MOD(n,100)+1 FROM (` + numbers + `) seq WHERE n < 5000 AND MOD(n,2)=0`,
		`INSERT INTO usage_logs (organization_id, api_key_id, model, status_code, input_tokens, output_tokens, total_tokens, cost_amount, created_at)
         SELECT MOD(n,100)+1, MOD(n,5000)+1, CONCAT('model-', MOD(FLOOR(n/5000),4)),
            IF(MOD(n,17)=0,500,200), 1000+MOD(n,100), 100, 1100+MOD(n,100), 0.00000001,
            TIMESTAMPADD(SECOND, MOD(n,86400), '2026-10-26 16:00:00') FROM (` + numbers + `) seq WHERE n < 200000`,
		"ANALYZE TABLE usage_logs",
	} {
		if err := database.WithContext(ctx).Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	from := time.Date(2026, 10, 26, 16, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	filter := domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &to}}
	repository := NewAdminUsageRepository(database)
	// Warm both paths before comparing the same snapshot and connection pool.
	if _, err := repository.legacyConsolidatedOrganizationUsage(ctx, filter, 1, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DailyAdminOrganizationUsage(ctx, filter, 1, 20); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	want, err := repository.legacyConsolidatedOrganizationUsage(ctx, filter, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Since(start)
	start = time.Now()
	got, err := repository.DailyAdminOrganizationUsage(ctx, filter, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Since(start)
	if !reflect.DeepEqual(got, want) || got.Summary.Requests != 200000 || len(got.TopKeys) != 20 {
		t.Fatalf("dashboard mismatch: got=%+v want=%+v", got, want)
	}
	t.Logf("200000 requests, 5000 keys (2500 deleted): previous=%s optimized=%s speedup=%.2fx", before, after, float64(before)/float64(after))
	query := dailyAdminOrganizationDashboardQuery(database, filter, 1, 20).Statement
	var plan string
	if err := database.WithContext(ctx).Raw("EXPLAIN ANALYZE "+query.SQL.String(), query.Vars...).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	latestLookup := regexp.MustCompile(`Index lookup on latest_usage using idx_usage_key_time[^\n]*\(reverse\)[^\n]*rows=1 loops=20\)`)
	if strings.Contains(plan, "Sort: latest_usage") || !latestLookup.MatchString(plan) {
		t.Fatalf("expected twenty single-row backward history lookups without sorting: %s", plan)
	}
	for _, line := range strings.Split(plan, "\n") {
		if strings.Contains(line, "usage_logs") || strings.Contains(line, "latest_usage") || strings.Contains(line, "Materialize CTE usage_buckets") {
			t.Log(strings.TrimSpace(line))
		}
	}
	installUsageRollups(t, database)
	processor := usagerollup.New(database)
	for {
		count, err := processor.Batch(ctx, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			break
		}
	}
	preaggregated := NewPreaggregatedAdminUsageRepository(database)
	if _, err := preaggregated.DailyAdminOrganizationUsage(ctx, filter, 1, 20); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	rolledUp, err := preaggregated.DailyAdminOrganizationUsage(ctx, filter, 1, 20)
	rollupTime := time.Since(start)
	if err != nil || !reflect.DeepEqual(rolledUp, got) {
		t.Fatalf("preaggregation changed counters: result=%+v error=%v", rolledUp, err)
	}
	var buckets int64
	if err := database.WithContext(ctx).Table("usage_daily_rollups").Count(&buckets).Error; err != nil || buckets != 20000 {
		t.Fatalf("expected 20000 daily buckets: count=%d error=%v", buckets, err)
	}
	t.Logf("preaggregation: 200000 logs -> %d buckets, raw=%s rollup=%s speedup=%.2fx", buckets, after, rollupTime, float64(after)/float64(rollupTime))
}

// Preserve the previous three-statement implementation only as a performance
// reference, so improvements are compared against the code being replaced.
// legacyConsolidatedOrganizationUsage preserves attribution from the usage log even after key deletion.
func (repository *AdminUsageRepository) legacyConsolidatedOrganizationUsage(ctx context.Context, filter domain.AdminOrganizationUsageFilter, page, size int) (domain.AdminOrganizationUsageDashboard, error) {
	result := domain.AdminOrganizationUsageDashboard{
		Items: make([]domain.AdminOrganizationUsagePoint, 0), TopOrganizations: make([]domain.AdminOrganizationTokenRank, 0),
		TopKeys: make([]domain.AdminKeyTokenRank, 0), TopModels: make([]domain.AdminModelTokenRank, 0),
	}
	var rows []adminOrganizationDashboardRow
	if err := legacyConsolidatedDashboardQuery(repository.database.WithContext(ctx), filter, page, size).Scan(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		switch row.RowType {
		case "summary":
			result.Total = row.Total
			result.Summary = row.AdminUsagePoint
		case "item":
			result.Items = append(result.Items, row.AdminOrganizationUsagePoint)
		case "organization":
			result.TopOrganizations = append(result.TopOrganizations, domain.AdminOrganizationTokenRank{
				OrganizationID: row.OrganizationID, OrganizationName: row.OrganizationName,
				InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, TotalTokens: row.TotalTokens,
			})
		}
	}
	if result.Total == 0 {
		return result, nil
	}
	keys, err := repository.legacyTopAdminKeyUsage(ctx, filter)
	if err != nil {
		return result, err
	}
	result.TopKeys = keys
	models, err := repository.legacyTopAdminModelUsage(ctx, filter)
	if err != nil {
		return result, err
	}
	result.TopModels = models
	return result, nil
}
func legacyConsolidatedDashboardQuery(database *gorm.DB, filter domain.AdminOrganizationUsageFilter, page, size int) *gorm.DB {
	// GROUP BY makes this CTE materialize once in MySQL. All four dashboard
	// sections reuse it, while only the requested page and top 20 leave MySQL.
	// Keep cost arithmetic in SQL so DECIMAL totals retain their precision.
	return database.Raw(`WITH user_days AS (?),
	page_items AS (
		SELECT * FROM user_days
		ORDER BY period DESC, total_tokens DESC, organization_id ASC LIMIT ? OFFSET ?
	), organization_ranks AS (
		SELECT organization_id, organization_name, SUM(input_tokens) AS input_tokens,
			SUM(output_tokens) AS output_tokens, SUM(total_tokens) AS total_tokens
		FROM user_days GROUP BY organization_id, organization_name HAVING SUM(total_tokens) > 0
		ORDER BY total_tokens DESC, organization_id ASC LIMIT 20
	)
	SELECT 'summary' AS row_type, '' AS period, 0 AS organization_id, '' AS organization_name,
		0 AS key_count, COUNT(*) AS total, COALESCE(SUM(requests), 0) AS requests,
		COALESCE(SUM(errors), 0) AS errors, COALESCE(SUM(input_tokens), 0) AS input_tokens,
		COALESCE(SUM(output_tokens), 0) AS output_tokens, COALESCE(SUM(total_tokens), 0) AS total_tokens,
		COALESCE(SUM(cost_amount), 0) AS cost_amount FROM user_days
	UNION ALL
	SELECT 'item', period, organization_id, organization_name, key_count, 0,
		requests, errors, input_tokens, output_tokens, total_tokens, cost_amount FROM page_items
	UNION ALL
	SELECT 'organization', '', organization_id, organization_name, 0, 0, 0, 0,
		input_tokens, output_tokens, total_tokens, 0 FROM organization_ranks
	ORDER BY row_type ASC, period DESC, total_tokens DESC, organization_id ASC`,
		legacyDailyOrganizationQuery(database, filter), size, (page-1)*size)
}

func legacyDailyOrganizationQuery(database *gorm.DB, filter domain.AdminOrganizationUsageFilter) *gorm.DB {
	query := adminDashboardUsageQuery(database, filter.AdminUsageFilter).
		Select("DATE_FORMAT(CONVERT_TZ(usage_logs.created_at, '+00:00', '+08:00'), '%Y-%m-%d') AS period, usage_logs.organization_id, COALESCE(org.name, '') AS organization_name, COUNT(DISTINCT usage_logs.api_key_id) AS key_count, COUNT(*) AS requests, COALESCE(SUM(usage_logs.status_code >= 400), 0) AS errors, COALESCE(SUM(usage_logs.input_tokens), 0) AS input_tokens, COALESCE(SUM(usage_logs.output_tokens), 0) AS output_tokens, COALESCE(SUM(usage_logs.total_tokens), 0) AS total_tokens, COALESCE(SUM(usage_logs.cost_amount), 0) AS cost_amount").
		Joins("LEFT JOIN organizations AS org ON org.id = usage_logs.organization_id").
		Group("period, usage_logs.organization_id, org.name")
	return adminOrganizationUsageSearch(query, filter.Search)
}
