package migrations

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

const legacyBindingsMigration = "010_legacy_organization_bindings.sql"

type legacyBinding struct {
	AccountID uint64
	Priority  int
	Models    string
	Status    string
}

func TestLegacyOrganizationBindingsMySQL(t *testing.T) {
	conn, ctx := legacyBindingsTestDatabase(t)
	entries, err := Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	// Build the real pre-upgrade schema rather than a simplified SQL fixture.
	for _, entry := range entries {
		name := entry.Name()
		if name >= legacyBindingsMigration || !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".down.sql") {
			continue
		}
		script, err := Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, string(script)); err != nil {
			t.Fatalf("prepare %s: %v", name, err)
		}
	}
	for _, account := range []struct {
		id     int
		status string
	}{
		{1, "active"}, {2, "active"},
		{3, "active"}, {4, "active"},
		{5, "active"}, {6, "disabled"},
	} {
		if _, err := conn.ExecContext(ctx, `INSERT INTO accounts
			(id, name, platform, account_type, base_url, credentials_ciphertext, priority, status)
			VALUES (?, ?, 'openai', 'api_key', 'https://example.invalid', '', 9, ?)`,
			account.id, fmt.Sprintf("channel-%d", account.id), account.status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO provider_models (account_id, model_name) VALUES
		(1, 'shared'), (1, 'unlisted'), (2, 'other'), (3, 'unassigned-model')`); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		providers any
		models    any
		existing  []legacyBinding
		want      []legacyBinding
	}{
		{
			name:      "explicit channels preserve models without a current match",
			providers: `[1,2,3,4,5,6,999,1]`, models: `["shared","future"]`,
			want: []legacyBinding{
				{1, 0, `["shared","future"]`, "active"}, {2, 0, `["shared","future"]`, "active"},
				{3, 0, `["shared","future"]`, "active"}, {6, 0, `["shared","future"]`, "active"},
			},
		},
		{name: "SQL null models inherit", providers: `[1]`, want: []legacyBinding{{1, 0, `[]`, "active"}}},
		{name: "JSON null models inherit", providers: `[1]`, models: `null`, want: []legacyBinding{{1, 0, `[]`, "active"}}},
		{name: "empty models inherit", providers: `[1]`, models: `[]`, want: []legacyBinding{{1, 0, `[]`, "active"}}},
		{name: "wildcard models inherit", providers: `[1]`, models: `["*"]`, want: []legacyBinding{{1, 0, `[]`, "active"}}},
		{name: "mixed wildcard models inherit", providers: `[1]`, models: `["shared","*"]`, want: []legacyBinding{{1, 0, `[]`, "active"}}},
		{name: "SQL null channels grant nothing", models: `[]`},
		{name: "JSON null channels grant nothing", providers: `null`, models: `[]`},
		{name: "empty channels grant nothing", providers: `[]`, models: `[]`},
		{name: "model only policy grants no global channels", models: `["shared"]`},
		{name: "nonexistent channels grant nothing", providers: `[999]`, models: `[]`},
		{name: "invalid channel policy is skipped", providers: `{"id":1}`, models: `[]`},
		{name: "invalid channel elements are skipped", providers: `[1,"2"]`, models: `[]`},
		{name: "invalid model policy is skipped", providers: `[1]`, models: `"*"`},
		{name: "invalid model elements cannot enable a wildcard", providers: `[1]`, models: `[1,"*"]`},
		{
			name: "saved channel policy overrides stale summary", providers: `[1,2]`, models: `["*"]`,
			existing: []legacyBinding{{1, 8, `["shared"]`, "active"}},
			want:     []legacyBinding{{1, 8, `["shared"]`, "active"}},
		},
		{
			name: "paused binding blocks all stale grants", providers: `[1,2]`, models: `["*"]`,
			existing: []legacyBinding{{1, 7, `["saved"]`, "paused"}},
			want:     []legacyBinding{{1, 7, `["saved"]`, "paused"}},
		},
		{
			name: "disabled binding outside old summary remains authoritative", providers: `[1]`, models: `["*"]`,
			existing: []legacyBinding{{2, 4, `["saved"]`, "disabled"}},
			want:     []legacyBinding{{2, 4, `["saved"]`, "disabled"}},
		},
	}
	var wantInserted int64
	for index, test := range cases {
		wantInserted += int64(len(test.want) - len(test.existing))
		id := index + 1
		if _, err := conn.ExecContext(ctx, `INSERT INTO organizations (id, name, allowed_provider_ids, allowed_models)
			VALUES (?, ?, ?, ?)`, id, test.name, test.providers, test.models); err != nil {
			t.Fatal(err)
		}
		for _, binding := range test.existing {
			if _, err := conn.ExecContext(ctx, `INSERT INTO organization_provider_bindings
				(organization_id, account_id, priority, model_allowlist, status) VALUES (?, ?, ?, ?, ?)`,
				id, binding.AccountID, binding.Priority, binding.Models, binding.Status); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Include existing Key overrides and historical user attribution. Neither may
	// be rewritten while restoring inherited organization access.
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO users (id, email, password_hash, organization_id, role, status)
			VALUES (1, 'admin@example.invalid', 'fixture', NULL, 'super_admin', 'active');
		INSERT INTO api_keys (id, name, key_hash, key_prefix, organization_id, status, allowed_provider_ids, allowed_models, quota_used_amount)
			VALUES (1, 'inherited', 'hash1', 'prefix1', 1, 'active', NULL, NULL, 1.25),
			       (2, 'empty', 'hash2', 'prefix2', 2, 'active', JSON_ARRAY(), JSON_ARRAY(), 0),
			       (3, 'restricted', 'hash3', 'prefix3', 1, 'active', JSON_ARRAY(1), JSON_ARRAY('shared'), 2.5);
		INSERT INTO usage_logs (request_id, usage_id, user_id, organization_id, api_key_id, model, endpoint, status_code, input_tokens, output_tokens, total_tokens, cost_amount, created_at)
			VALUES ('request-1', 'usage-1', 1, 1, 1, 'shared', '/v1/chat/completions', 200, 2, 3, 5, 1.25, '2026-10-01');
		INSERT INTO daily_usages (usage_date, organization_id, api_key_id, model, request_count, total_tokens, cost_amount)
			VALUES ('2026-10-01', 1, 1, 'shared', 1, 5, 1.25);
		INSERT INTO request_logs (created_at, level, event, message, organization_id, request_id)
			VALUES ('2026-10-01', 'INFO', 'gateway.completed', 'fixture', 1, 'request-1');
		INSERT INTO security_events (organization_id, api_key_id, request_id, created_at)
			VALUES (1, 1, 'request-1', '2026-10-01');
	`); err != nil {
		t.Fatal(err)
	}
	unchangedTables := []string{"organizations", "accounts", "provider_models", "users", "api_keys", "usage_logs", "daily_usages", "request_logs", "security_events"}
	before := make(map[string]string, len(unchangedTables))
	for _, table := range unchangedTables {
		before[table] = legacyTableSnapshot(t, ctx, conn, table)
	}
	script, err := Files.ReadFile(legacyBindingsMigration)
	if err != nil {
		t.Fatal(err)
	}
	for pass := range 2 {
		result, err := conn.ExecContext(ctx, string(script))
		if err != nil {
			t.Fatal(err)
		}
		wantAffected := wantInserted
		if pass > 0 {
			wantAffected = 0
		}
		if affected, err := result.RowsAffected(); err != nil || affected != wantAffected {
			t.Fatalf("pass %d: affected=%d err=%v", pass, affected, err)
		}
		for index, test := range cases {
			t.Run(fmt.Sprintf("%s/pass%d", test.name, pass), func(t *testing.T) {
				rows, err := conn.QueryContext(ctx, `SELECT account_id, priority, model_allowlist, status
					FROM organization_provider_bindings WHERE organization_id = ? ORDER BY account_id`, index+1)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var got []legacyBinding
				for rows.Next() {
					var binding legacyBinding
					if err := rows.Scan(&binding.AccountID, &binding.Priority, &binding.Models, &binding.Status); err != nil {
						t.Fatal(err)
					}
					var models []string
					if err := json.Unmarshal([]byte(binding.Models), &models); err != nil {
						t.Fatal(err)
					}
					encoded, err := json.Marshal(models)
					if err != nil {
						t.Fatal(err)
					}
					binding.Models = string(encoded)
					got = append(got, binding)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, test.want) {
					t.Fatalf("bindings=%+v, want %+v", got, test.want)
				}
			})
		}
		for _, table := range unchangedTables {
			if after := legacyTableSnapshot(t, ctx, conn, table); after != before[table] {
				t.Errorf("pass %d changed %s", pass, table)
			}
		}
	}
}

func legacyBindingsTestDatabase(t *testing.T) (*sql.Conn, context.Context) {
	t.Helper()
	dsn := os.Getenv("BOS_MIGRATIONS_TEST_DSN")
	if dsn == "" {
		t.Skip("set BOS_MIGRATIONS_TEST_DSN to run against an isolated MySQL test server")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid BOS_MIGRATIONS_TEST_DSN")
	}
	// Ignore any configured business database. All writes are confined to the
	// schema created below, and cleanup can only drop that generated schema.
	config.DBName, config.MultiStatements, config.ParseTime = "", true, false
	connector, err := mysql.NewConnector(config)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	schema := "bos_bindings_test_" + strings.ToLower(rand.Text())
	if _, err := conn.ExecContext(ctx, "CREATE DATABASE `"+schema+"`"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := conn.ExecContext(cleanupCtx, "DROP DATABASE `"+schema+"`"); err != nil {
			t.Errorf("clean up disposable schema %s: %v", schema, err)
		}
	})
	if _, err := conn.ExecContext(ctx, "USE `"+schema+"`"); err != nil {
		t.Fatal(err)
	}
	return conn, ctx
}

func legacyTableSnapshot(t *testing.T, ctx context.Context, conn *sql.Conn, table string) string {
	t.Helper()
	rows, err := conn.QueryContext(ctx, "SELECT * FROM `"+table+"` ORDER BY 1, 2, 3")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var snapshot [][]sql.NullString
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		targets := make([]any, len(columns))
		for index := range values {
			targets[index] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		snapshot = append(snapshot, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
