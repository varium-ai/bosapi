package migrations

import "testing"

func TestRequestLogPartitionMigrationMySQL(t *testing.T) {
	conn, ctx := legacyBindingsTestDatabase(t)
	for _, filename := range []string{"001_initial_schema.sql", "retired/019_request_log_partitions.sql"} {
		script, err := Files.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, string(script)); err != nil {
			t.Fatalf("%s: %v", filename, err)
		}
		if filename == "001_initial_schema.sql" {
			if _, err := conn.ExecContext(ctx, `INSERT INTO request_logs (created_at, level, event, message) VALUES
				(UTC_TIMESTAMP(6) - INTERVAL 30 DAY, 'INFO', 'old', 'retain during migration'),
				(UTC_TIMESTAMP(6), 'INFO', 'new', 'retain during migration')`); err != nil {
				t.Fatal(err)
			}
		}
	}
	var partitions, rows int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.PARTITIONS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'request_logs' AND PARTITION_NAME IS NOT NULL`).Scan(&partitions); err != nil || partitions != 17 {
		t.Fatalf("partitions=%d want 17, err=%v", partitions, err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM request_logs").Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("migration changed existing rows=%d err=%v", rows, err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO request_logs (created_at, level, event, message)
		VALUES (UTC_TIMESTAMP(6) + INTERVAL 60 DAY, 'INFO', 'future', 'catch-all accepts future writes')`); err != nil {
		t.Fatal(err)
	}
	script, err := Files.ReadFile("retired/019_request_log_partitions.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, string(script)); err != nil {
		t.Fatal(err)
	}
	var primary string
	if err := conn.QueryRowContext(ctx, `SELECT GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX)
		FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE()
		AND TABLE_NAME = 'request_logs' AND INDEX_NAME = 'PRIMARY'`).Scan(&primary); err != nil || primary != "id" {
		t.Fatalf("rollback primary key=%q err=%v", primary, err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM request_logs").Scan(&rows); err != nil || rows != 3 {
		t.Fatalf("rollback changed surviving rows=%d err=%v", rows, err)
	}
}

func TestRequestLogTransitionMigrationMySQL(t *testing.T) {
	for _, previouslyPartitioned := range []bool{false, true} {
		name := "unpartitioned legacy table"
		if previouslyPartitioned {
			name = "previously applied 019"
		}
		t.Run(name, func(t *testing.T) {
			conn, ctx := legacyBindingsTestDatabase(t)
			apply := func(filename string) {
				t.Helper()
				script, err := Files.ReadFile(filename)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := conn.ExecContext(ctx, string(script)); err != nil {
					t.Fatalf("%s: %v", filename, err)
				}
			}
			apply("001_initial_schema.sql")
			if _, err := conn.ExecContext(ctx, `INSERT INTO request_logs (created_at, level, event, message)
				VALUES (UTC_TIMESTAMP(6) - INTERVAL 30 DAY, 'INFO', 'test', 'original history')`); err != nil {
				t.Fatal(err)
			}
			if previouslyPartitioned {
				apply("retired/019_request_log_partitions.sql")
			}
			var table, before, after string
			if err := conn.QueryRowContext(ctx, "SHOW CREATE TABLE request_logs").Scan(&table, &before); err != nil {
				t.Fatal(err)
			}
			apply("021_request_log_transition.sql")
			if err := conn.QueryRowContext(ctx, "SHOW CREATE TABLE request_logs").Scan(&table, &after); err != nil || before != after {
				t.Fatalf("startup changed the original table: %v", err)
			}
			var source, destination, partitions int
			if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM request_logs").Scan(&source); err != nil || source != 1 {
				t.Fatalf("source rows=%d err=%v", source, err)
			}
			if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM request_logs_v2").Scan(&destination); err != nil || destination != 0 {
				t.Fatalf("startup copied rows=%d err=%v", destination, err)
			}
			if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.PARTITIONS
				WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'request_logs_v2' AND PARTITION_NAME IS NOT NULL`).Scan(&partitions); err != nil || partitions != 17 {
				t.Fatalf("destination partitions=%d err=%v", partitions, err)
			}
			var window int64
			if err := conn.QueryRowContext(ctx, "SELECT TIMESTAMPDIFF(MICROSECOND, from_at, until_at) FROM request_log_backfill WHERE id = 1").Scan(&window); err != nil || window != 24*60*60*1000000 {
				t.Fatalf("backfill window=%d microseconds err=%v", window, err)
			}
		})
	}
}
