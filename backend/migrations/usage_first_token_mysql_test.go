package migrations

import (
	"database/sql"
	"testing"
)

func TestUsageFirstTokenMigrationMySQL(t *testing.T) {
	conn, ctx := legacyBindingsTestDatabase(t)
	for _, filename := range []string{"001_initial_schema.sql", "015_usage_first_token.sql"} {
		script, err := Files.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, string(script)); err != nil {
			t.Fatalf("execute %s: %v", filename, err)
		}
		if filename == "001_initial_schema.sql" {
			if _, err := conn.ExecContext(ctx, `INSERT INTO usage_logs
				(request_id, usage_id, organization_id, api_key_id, model, endpoint, status_code,
				 input_tokens, output_tokens, total_tokens, cost_amount, duration_ms, created_at)
				VALUES ('historical', 'usage', 2, 7, 'example', 'chat/completions', 200,
				 1, 2, 3, 0.01, 1000, '2026-10-22')`); err != nil {
				t.Fatal(err)
			}
		}
	}
	var historical sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT first_token_ms FROM usage_logs WHERE request_id = ?", "historical").Scan(&historical); err != nil || historical.Valid {
		t.Fatalf("historical first token timing = %+v, err=%v", historical, err)
	}
	for _, test := range []struct {
		name  string
		value uint32
	}{
		{name: "zero", value: 0},
		{name: "measured", value: 123},
		{name: "maximum", value: ^uint32(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := conn.ExecContext(ctx, "UPDATE usage_logs SET first_token_ms = ? WHERE request_id = ?", test.value, "historical"); err != nil {
				t.Fatal(err)
			}
			var got uint32
			if err := conn.QueryRowContext(ctx, "SELECT first_token_ms FROM usage_logs WHERE request_id = ?", "historical").Scan(&got); err != nil || got != test.value {
				t.Fatalf("first token timing = %d, want %d: %v", got, test.value, err)
			}
		})
	}
	rollback, err := Files.ReadFile("015_usage_first_token.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, string(rollback)); err != nil {
		t.Fatalf("execute first token rollback: %v", err)
	}
	var columns, requests, tokens, duration int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'usage_logs' AND column_name = 'first_token_ms'").Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("first token column count after rollback = %d: %v", columns, err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*), SUM(total_tokens), SUM(duration_ms) FROM usage_logs").Scan(&requests, &tokens, &duration); err != nil || requests != 1 || tokens != 3 || duration != 1000 {
		t.Fatalf("rollback changed historical usage: requests=%d tokens=%d duration=%d err=%v", requests, tokens, duration, err)
	}
}
