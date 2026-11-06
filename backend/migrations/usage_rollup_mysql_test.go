package migrations

import "testing"

func TestUsageRollupMigrationMySQL(t *testing.T) {
	connection, ctx := legacyBindingsTestDatabase(t)
	initial, err := Files.ReadFile("001_initial_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, string(initial)); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO usage_logs
		(request_id, usage_id, organization_id, api_key_id, model, endpoint, status_code,
		 input_tokens, output_tokens, total_tokens, cost_amount, created_at)
		VALUES (?, 'usage', 9, 7, 'example', '/chat/completions', 200, 1, 2, 3, 0.00000001, '2026-10-01')`
	if _, err := connection.ExecContext(ctx, insert, "historical"); err != nil {
		t.Fatal(err)
	}
	forward, err := Files.ReadFile("017_usage_daily_rollups.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, string(forward)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, insert, "new"); err != nil {
		t.Fatal(err)
	}
	var logs, pending, tokens int
	if err := connection.QueryRowContext(ctx, "SELECT COUNT(*), SUM(rollup_applied = 0), SUM(total_tokens) FROM usage_logs").Scan(&logs, &pending, &tokens); err != nil || logs != 2 || pending != 2 || tokens != 6 {
		t.Fatalf("existing and future records must start pending: logs=%d pending=%d tokens=%d error=%v", logs, pending, tokens, err)
	}
	rollback, err := Files.ReadFile("017_usage_daily_rollups.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, string(rollback)); err != nil {
		t.Fatal(err)
	}
	if err := connection.QueryRowContext(ctx, "SELECT COUNT(*), SUM(total_tokens) FROM usage_logs").Scan(&logs, &tokens); err != nil || logs != 2 || tokens != 6 {
		t.Fatalf("rollback changed the immutable ledger: logs=%d tokens=%d error=%v", logs, tokens, err)
	}
}
