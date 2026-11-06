package migrations

import "testing"

func TestUsageLogTransitionLeavesLiveSourceUntouchedMySQL(t *testing.T) {
	conn, ctx := legacyBindingsTestDatabase(t)
	apply := func(name string) {
		t.Helper()
		script, err := Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, string(script)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"001_initial_schema.sql", "004_usage_error_code.sql", "015_usage_first_token.sql", "017_usage_daily_rollups.sql", "020_key_usage_covering_index.sql"} {
		apply(name)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO usage_logs
		(request_id, usage_id, organization_id, api_key_id, model, endpoint, status_code, input_tokens, output_tokens, total_tokens, cost_amount, created_at)
		VALUES ('old', 'usage', 2, 7, 'model', 'chat/completions', 200, 1, 2, 3, 0.12345678, UTC_TIMESTAMP() - INTERVAL 90 DAY),
		('new', 'usage', 2, 7, 'model', 'chat/completions', 200, 1, 2, 3, 0.00000001, UTC_TIMESTAMP())`); err != nil {
		t.Fatal(err)
	}
	var table, before, after string
	if err := conn.QueryRowContext(ctx, "SHOW CREATE TABLE usage_logs").Scan(&table, &before); err != nil {
		t.Fatal(err)
	}
	apply("023_usage_log_transition.sql")
	if err := conn.QueryRowContext(ctx, "SHOW CREATE TABLE usage_logs").Scan(&table, &after); err != nil || before != after {
		t.Fatalf("live source schema changed: %v", err)
	}
	var rows, partitions, copied, enabled int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_logs").Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("source changed: %d %v", rows, err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_logs_v2").Scan(&copied); err != nil || copied != 0 {
		t.Fatalf("startup copied rows: %d %v", copied, err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT enabled FROM usage_log_backfill WHERE id = 1").Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("legacy drain enabled before rollout: %d %v", enabled, err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.PARTITIONS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'usage_logs_v2' AND PARTITION_NAME IS NOT NULL`).Scan(&partitions); err != nil || partitions != 40 {
		t.Fatalf("partitions=%d err=%v", partitions, err)
	}
	var primary string
	if err := conn.QueryRowContext(ctx, `SELECT GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'usage_logs_v2' AND INDEX_NAME = 'PRIMARY'`).Scan(&primary); err != nil || primary != "id,created_at" {
		t.Fatalf("partition primary=%s err=%v", primary, err)
	}
}
