package interfaces

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/platform/testusage"
	"bosapi/backend/internal/platform/usagerollup"
	"bosapi/backend/internal/platform/usagestore"
	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/migrations"
	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This DSN must permit disposable databases. No fixture touches its named DB.
func newSetupMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("BOS_SETUP_TEST_DSN")
	if dsn == "" {
		t.Skip("set BOS_SETUP_TEST_DSN to verify setup SQL against MySQL")
	}
	config, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.DBName, config.ParseTime, config.MultiStatements = "", true, true
	root, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	schema := "setup_test_" + rand.Text()
	if _, err := root.Exec("CREATE DATABASE `" + schema + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := root.Exec("DROP DATABASE `" + schema + "`"); err != nil {
			t.Error(err)
		}
	})
	config.DBName = schema
	db, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	initial, err := migrations.Files.ReadFile("001_initial_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"organizations", "api_keys", "accounts", "provider_models", "organization_provider_bindings", "users"} {
		start := strings.Index(string(initial), "CREATE TABLE IF NOT EXISTS "+table+" (")
		end := start + strings.Index(string(initial[start:]), ";") + 1
		if err := db.Exec(string(initial[start:end])).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"008_provider_visibility.sql", "009_organization_keys.sql", "016_provider_verification.sql"} {
		script, err := migrations.Files.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(string(script)).Error; err != nil {
			t.Fatal(err)
		}
	}
	testusage.Install(t, db)
	testusage.LegacyDefaults(t, db)
	return db
}

func setupFixture(t *testing.T, db *gorm.DB, now time.Time) {
	t.Helper()
	base := now.Add(-10 * time.Hour)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (id,name,status,updated_at) VALUES (7,'target','active',?),(8,'other','active',?)`, []any{base, base}},
		{`INSERT INTO api_keys (id,name,key_hash,key_prefix,user_id,organization_id,status,updated_at)
 VALUES (10,'a','a','a',1,7,'active',?),(11,'b','b','b',1,7,'active',?),
 (12,'other','c','c',1,8,'active',?),(13,'denied','d','d',1,7,'active',?)`, []any{base, base, base, base}},
		{`UPDATE api_keys SET allowed_models='["blocked"]' WHERE id=13`, nil},
		{`INSERT INTO accounts (id,name,platform,account_type,api_format,base_url,credentials_ciphertext,status,schedulable,updated_at)
 VALUES (1,'public','custom','api_key','openai_chat','',X'','active',1,?),
 (2,'unassigned','custom','api_key','openai_chat','',X'','active',1,?),
 (3,'other','custom','api_key','openai_chat','',X'','active',1,?),
 (4,'gemini','custom','api_key','gemini','',X'','active',1,?)`, []any{base, base, base, base}},
		{`INSERT INTO provider_models (account_id,model_name,status,updated_at) VALUES
 (1,'alpha','active',?),(2,'unassigned-alpha','active',?),(3,'other','active',?),(4,'models/gemini','active',?)`, []any{base, base, base, base}},
		{`INSERT INTO organization_provider_bindings (organization_id,account_id,status) VALUES (7,1,'active'),(7,4,'active'),(8,1,'active')`, nil},
	} {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, entry := range []struct {
		table, model string
		key, org     uint64
		ago          time.Duration
		status       int
		code         string
	}{
		{"usage_logs", "alpha", 10, 7, 5 * time.Hour, 200, ""},
		{"usage_logs_v2", "alpha", 10, 7, 4 * time.Hour, 200, ""},
		{"usage_logs", "alpha", 11, 7, 2 * time.Hour, 200, ""},
		{"usage_logs_v2", "denied", 11, 7, time.Hour, 200, ""},
		{"usage_logs_v2", "alpha", 10, 7, 30 * time.Minute, 500, ""},
		{"usage_logs_v2", "alpha", 10, 8, 10 * time.Minute, 200, ""},
		{"usage_logs", "alpha", 12, 8, 5 * time.Minute, 200, ""},
		{"usage_logs_v2", "blocked", 13, 7, 2 * time.Minute, 200, ""},
		{"usage_logs", "unassigned-alpha", 10, 7, 3 * time.Hour, 200, ""},
		{"usage_logs_v2", "gemini", 10, 7, 90 * time.Minute, 200, ""},
		{"usage_logs", "alpha", 10, 7, time.Minute, 200, "security_blocked"},
	} {
		if err := db.Exec("INSERT INTO "+entry.table+` (id,request_id,usage_id,organization_id,api_key_id,model,endpoint,status_code,error_code,
 input_tokens,output_tokens,total_tokens,cost_amount,created_at) VALUES (?,?,'test',?,?,?,'chat/completions',?,?,1,2,3,0.00000001,?)`,
			i+1, fmt.Sprint("request-", i), entry.org, entry.key, entry.model, entry.status, entry.code, now.Add(-entry.ago)).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestSetupStatusMySQLScopesAndEvidence(t *testing.T) {
	db := newSetupMySQL(t)
	now := time.Date(2026, 11, 2, 4, 0, 0, 0, time.UTC)
	setupFixture(t, db, now)
	for _, test := range []struct {
		name, change string
		usable       bool
		ago          time.Duration
	}{
		{name: "latest across both tables and all keys, excluding denied models and errors", usable: true, ago: 90 * time.Minute},
		{name: "disabled key", change: "UPDATE api_keys SET status='disabled' WHERE id=10", usable: true, ago: 2 * time.Hour},
		{name: "expired key", change: "UPDATE api_keys SET expires_at='2026-11-02 04:00:00' WHERE id=10", usable: true, ago: 2 * time.Hour},
		{name: "provider allowlist", change: "UPDATE api_keys SET allowed_provider_ids='[1]' WHERE id=10", usable: true, ago: 2 * time.Hour},
		{name: "model allowlist", change: "UPDATE api_keys SET allowed_models='[\"alpha\"]' WHERE id=10", usable: true, ago: 2 * time.Hour},
		{name: "updated channel invalidates old success", change: "UPDATE accounts SET updated_at='2026-11-02 04:00:00' WHERE id=4", usable: true, ago: 2 * time.Hour},
		{name: "updated model invalidates old success", change: "UPDATE provider_models SET updated_at='2026-11-02 04:00:00' WHERE account_id=4", usable: true, ago: 2 * time.Hour},
		{name: "binding model scope", change: "UPDATE organization_provider_bindings SET model_allowlist='[\"blocked\"]' WHERE organization_id=7"},
		{name: "paused bindings deny unassigned channels", change: "UPDATE organization_provider_bindings SET status='disabled' WHERE organization_id=7"},
		{name: "key configuration invalidates evidence", change: "UPDATE api_keys SET updated_at='2026-11-02 04:00:00' WHERE organization_id=7", usable: true},
		{name: "null key config admits no evidence", change: "UPDATE api_keys SET updated_at=NULL WHERE organization_id=7", usable: true},
		{name: "zero budget cannot admit a first request", change: "UPDATE api_keys SET daily_quota_tokens=0 WHERE organization_id=7"},
		{name: "exhausted key excluded from success", change: "UPDATE api_keys SET daily_quota_tokens=0 WHERE id=10", usable: true, ago: 2 * time.Hour},
		{name: "total budget exhausted", change: "UPDATE api_keys SET quota_tokens=0 WHERE organization_id=7"},
		{name: "last candidate page contains latest success", change: `INSERT INTO api_keys
 (id,name,key_hash,key_prefix,user_id,organization_id,status,updated_at)
 SELECT 1000+a.n+10*b.n,CONCAT('page-',a.n+10*b.n),CONCAT('page-',a.n+10*b.n),'page',1,7,'active',NULL FROM
 (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) a
 CROSS JOIN (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) b;
 UPDATE api_keys SET updated_at='2026-11-01 18:00:00' WHERE id=1099;
 INSERT INTO usage_logs (id,request_id,usage_id,organization_id,api_key_id,model,total_tokens,cost_amount,created_at)
 VALUES (50,'last-page','test',7,1099,'alpha',3,0.00000001,'2026-11-02 04:30:00')`, usable: true, ago: -30 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := db.Begin()
			t.Cleanup(func() { tx.Rollback() })
			if test.change != "" {
				if err := tx.Exec(test.change).Error; err != nil {
					t.Fatal(err)
				}
			}
			usable, latest, err := loadSetupKeyStatus(tx, 7, now.Add(-10*time.Hour), now, false)
			if err != nil || usable != test.usable {
				t.Fatalf("usable=%v latest=%v error=%v", usable, latest, err)
			}
			if test.ago == 0 {
				if latest != nil {
					t.Fatalf("unexpected success: %v", latest)
				}
			} else if latest == nil || !latest.Equal(now.Add(-test.ago)) {
				t.Fatalf("latest=%v want=%v", latest, now.Add(-test.ago))
			}
		})
	}
	usable, latest, err := loadSetupKeyStatus(db, 7, now, now, false)
	if err != nil || !usable || latest != nil {
		t.Fatalf("organization config must invalidate evidence: usable=%v latest=%v error=%v", usable, latest, err)
	}
}

func TestSetupQuotaMySQLParityAcrossRollupAndMove(t *testing.T) {
	db := newSetupMySQL(t)
	nows := []time.Time{
		time.Date(2026, 10, 11, 18, 0, 0, 0, time.UTC), // UTC Sunday, business Monday
		time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC), // month starts mid-week
		time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), // month starts before the week
	}
	if err := db.Exec(`INSERT INTO api_keys (id,name,key_hash,key_prefix,user_id,organization_id,status) VALUES (7,'key','hash','prefix',1,2,'active')`).Error; err != nil {
		t.Fatal(err)
	}
	id := 0
	for _, now := range nows {
		day, week, month := usagetime.PeriodStarts(now)
		for _, boundary := range []time.Time{day, week, month, now.Add(time.Hour)} {
			for _, delta := range []time.Duration{-time.Millisecond, 0, time.Millisecond} {
				id++
				table := "usage_logs"
				if id%2 == 0 {
					table = "usage_logs_v2"
				}
				// Historical organization attribution must not narrow a Key's quota.
				if err := db.Exec("INSERT INTO "+table+` (id,request_id,usage_id,organization_id,api_key_id,model,endpoint,status_code,
 input_tokens,output_tokens,total_tokens,cost_amount,created_at) VALUES (?,?,'test',?,7,'alpha','chat/completions',200,1,2,9007199254740993,0.00000001,?)`,
					id, fmt.Sprint("quota-", id), id%2+1, boundary.Add(delta)).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	columns := []string{"daily_quota_amount", "daily_quota_tokens", "weekly_quota_amount", "weekly_quota_tokens", "monthly_quota_amount", "monthly_quota_tokens"}
	reset := "UPDATE api_keys SET " + strings.Join(columns, "=NULL,") + "=NULL WHERE id=7"
	verify := func(t *testing.T) {
		for _, now := range nows {
			day, week, month := usagetime.PeriodStarts(now)
			for i, column := range columns {
				boundary := []time.Time{day, week, month}[i/2]
				metric, unit := "cost_amount", "0.00000001"
				if i%2 == 1 {
					metric, unit = "total_tokens", "1"
				}
				for _, delta := range []int{-1, 0, 1} {
					if err := db.Exec(reset).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.Exec("UPDATE api_keys SET "+column+"=(SELECT COALESCE(SUM("+metric+"),0) FROM usage_logs_read WHERE api_key_id=7 AND created_at >= ?)+?*"+unit+" WHERE id=7", boundary, delta).Error; err != nil {
						t.Fatal(err)
					}
					var got struct{ Usable bool }
					if err := setupPeriodicQuotaQuery(db, 7, now).Scan(&got).Error; err != nil {
						t.Fatal(err)
					}
					if got.Usable != (delta > 0) {
						t.Fatalf("%s now=%v budget delta=%d usable=%v", column, now, delta, got.Usable)
					}
				}
			}
		}
		if err := db.Exec(reset).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Run("pending", verify)
	processor := usagerollup.New(db)
	if _, err := processor.Batch(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	t.Run("mixed", verify)
	if _, err := processor.Batch(context.Background(), 5000); err != nil {
		t.Fatal(err)
	}
	t.Run("acknowledged", verify)
	// Retain every artificial calendar fixture while physically moving history.
	if err := db.Exec("UPDATE usage_log_retention SET retained_from='2026-08-01' WHERE id=1").Error; err != nil {
		t.Fatal(err)
	}
	testusage.Activate(t, db, true)
	if _, err := usagestore.MoveBatch(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Run("moved", verify)
}

func explainSetupSources(t *testing.T, db, query *gorm.DB) map[string][]string {
	t.Helper()
	statement := query.Statement
	var encoded string
	if err := db.Raw("EXPLAIN FORMAT=JSON "+statement.SQL.String(), statement.Vars...).Scan(&encoded).Error; err != nil {
		t.Fatal(err)
	}
	var plan any
	if err := json.Unmarshal([]byte(encoded), &plan); err != nil {
		t.Fatal(err)
	}
	sources := make(map[string][]string)
	var walk func(any)
	walk = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			name, _ := item["table_name"].(string)
			if name == "usage_logs" || name == "usage_logs_v2" || name == "usage_daily_rollups" || name == "u" {
				access, _ := item["access_type"].(string)
				key, _ := item["key"].(string)
				if access != "ref" && access != "range" && access != "const" {
					t.Fatalf("unbounded source %s: %s\n%s", name, access, encoded)
				}
				if name == "u" && item["backward_index_scan"] != true {
					t.Fatalf("evidence must stop in a reverse time-index scan, not sort all matching history:\n%s", encoded)
				}
				sources[name] = append(sources[name], key)
			}
			for _, child := range item {
				walk(child)
			}
		case []any:
			for _, child := range item {
				walk(child)
			}
		}
	}
	walk(plan)
	return sources
}

func TestSetupMySQLPlansAndVolume(t *testing.T) {
	db := newSetupMySQL(t)
	now := time.Date(2026, 11, 2, 4, 0, 0, 0, time.UTC)
	setupFixture(t, db, now)
	count := 10000
	if os.Getenv("BOS_SETUP_PERF") == "1" {
		count = 1000000
	}
	// A busy Key's already-acknowledged history must not be reread for budgets.
	// Bound fixture transactions too, so the optional volume test can run on a
	// small test server without one million-row undo/temporary allocations.
	digits := "(SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9)"
	insert := `INSERT INTO usage_logs
 (id,request_id,usage_id,organization_id,api_key_id,model,endpoint,status_code,input_tokens,output_tokens,total_tokens,cost_amount,created_at,rollup_applied)
 SELECT 100+?+n,CONCAT('volume-',?+n),'test',7,10,'alpha','chat/completions',200,1,2,3,0.00000001,?,1 FROM (
 SELECT a.n+10*b.n+100*c.n+1000*d.n AS n FROM ` + digits + " a CROSS JOIN " + digits +
		" b CROSS JOIN " + digits + " c CROSS JOIN " + digits + " d) numbers WHERE n < ?"
	for offset := 0; offset < count; offset += 10000 {
		if err := db.Exec(insert, offset, offset, now.Add(-4*time.Hour), min(10000, count-offset)).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`INSERT INTO usage_daily_rollups
 (usage_date,organization_id,api_key_id,model,requests,errors,input_tokens,output_tokens,total_tokens,cost_amount)
 VALUES (?,7,10,'alpha',?,0,?,?,?,?*0.00000001)`, now.Add(-4*time.Hour).In(usagetime.Location()).Format("2006-01-02"),
		count, count, 2*count, 3*count, count).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE api_keys SET daily_quota_tokens=100000000 WHERE id=10").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ANALYZE TABLE usage_logs, usage_logs_v2, usage_daily_rollups").Error; err != nil {
		t.Fatal(err)
	}
	quotaPlan := explainSetupSources(t, db, setupPeriodicQuotaQuery(db, 10, now))
	for _, table := range []string{"usage_logs", "usage_logs_v2"} {
		if len(quotaPlan[table]) != 1 || quotaPlan[table][0] != "idx_usage_key_pending_time" {
			t.Fatalf("wrong pending plan: %v", quotaPlan)
		}
	}
	evidencePlan := explainSetupSources(t, db, setupEvidenceQuery(db, 10, 7, now.Add(-10*time.Hour), nil, false))
	if len(evidencePlan["u"]) != 2 {
		t.Fatalf("expected two indexed evidence branches: %v", evidencePlan)
	}
	started := time.Now()
	usable, latest, err := loadSetupKeyStatus(db, 7, now.Add(-10*time.Hour), now, false)
	if err != nil || !usable || latest == nil || !latest.Equal(now.Add(-90*time.Minute)) {
		t.Fatalf("usable=%v latest=%v error=%v", usable, latest, err)
	}
	t.Logf("%d acknowledged busy-Key rows: setup-status %s; quota indexes=%v; evidence indexes=%v", count, time.Since(started), quotaPlan, evidencePlan)
}
