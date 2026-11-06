package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	authapp "bosapi/backend/internal/auth/application"
	"bosapi/backend/migrations"
	"github.com/DATA-DOG/go-sqlmock"
)

type matchingPasswordHash string

func (password matchingPasswordHash) Match(value driver.Value) bool {
	hash, ok := value.(string)
	return ok && hash != string(password) && authapp.VerifyPassword(hash, string(password))
}

func initialSchemaMigration(t *testing.T) sqlMigration {
	t.Helper()
	files, err := readMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range files {
		if migration.filename == initialSchemaFilename {
			return migration
		}
	}
	t.Fatal("initial schema missing")
	return sqlMigration{}
}

func expectInitialAdminBinding(mock sqlmock.Sqlmock) {
	mock.ExpectExec("SET @bos_initial_admin_email = ?, @bos_initial_admin_password_hash = ?").
		WithArgs("admin@example.com", matchingPasswordHash("initial-password-123")).
		WillReturnResult(sqlmock.NewResult(0, 0))
}

func TestInitialAdminIsHashedAndNeverResetOnRestart(t *testing.T) {
	conn, mock := migrationConnection(t)
	migration := initialSchemaMigration(t)
	expectMigrationHistory(mock, migrationHistoryRows())
	expectInitialAdminBinding(mock)
	expectMigrationApplied(mock, migration)
	expectMigrationUnlock(mock)
	if err := applyMigrations(context.Background(), conn, "bosapi", []sqlMigration{migration}, InitialAdmin{
		Email: " Admin@Example.COM ", Password: "initial-password-123",
	}); err != nil {
		t.Fatal(err)
	}

	// No credentials are needed after completion; no password is sent or changed.
	expectMigrationHistory(mock, migrationHistoryRows().AddRow(migration.filename, migration.checksum, "2026-10-10 04:00:00"))
	expectMigrationUnlock(mock)
	if err := applyMigrations(context.Background(), conn, "bosapi", []sqlMigration{migration}); err != nil {
		t.Fatal(err)
	}

	// Changing stack.env after installation must not rotate the stored password
	// or replace the original administrator with a different account.
	expectMigrationHistory(mock, migrationHistoryRows().AddRow(migration.filename, migration.checksum, "2026-10-10 04:00:00"))
	expectMigrationUnlock(mock)
	if err := applyMigrations(context.Background(), conn, "bosapi", []sqlMigration{migration}, InitialAdmin{
		Email: "replacement@example.com", Password: "replacement-password-123",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInitialAdminRejectsInvalidCredentialsBeforeMarkingMigrationStarted(t *testing.T) {
	for _, admin := range []InitialAdmin{
		{}, {Email: "not-an-email", Password: "initial-password-123"},
		{Email: "Admin <admin@example.com>", Password: "initial-password-123"},
		{Email: "admin@example.com", Password: "short"},
	} {
		conn, mock := migrationConnection(t)
		expectMigrationHistory(mock, migrationHistoryRows())
		expectMigrationUnlock(mock)
		err := applyMigrations(context.Background(), conn, "bosapi", []sqlMigration{initialSchemaMigration(t)}, admin)
		if err == nil || !strings.Contains(err.Error(), "BOS_ADMIN_") {
			t.Fatalf("expected actionable credential error, got %v", err)
		}
	}
}

func TestCommunitySchemaRejectsCommercialMigrationHistory(t *testing.T) {
	migration := initialSchemaMigration(t)
	if _, ok := initialAdminUpgrade(migration, legacyInitialSchemaChecksum); ok {
		t.Fatal("community schema must not be applied to a commercial database")
	}
	conn, mock := migrationConnection(t)
	expectMigrationHistory(mock, migrationHistoryRows().AddRow(migration.filename, legacyInitialSchemaChecksum, "2026-10-08 04:00:00"))
	expectMigrationUnlock(mock)
	if err := applyMigrations(context.Background(), conn, "bosapi", []sqlMigration{migration}); err == nil || !strings.Contains(err.Error(), "has changed") {
		t.Fatalf("expected a checksum boundary, got %v", err)
	}
}

func TestInitialAdminUpgradeRejectsChangesToOriginalSchema(t *testing.T) {
	migration := initialSchemaMigration(t)
	migration.script = strings.Replace(migration.script, "CREATE TABLE", "DROP TABLE", 1)
	if _, ok := initialAdminUpgrade(migration, legacyInitialSchemaChecksum); ok {
		t.Fatal("modified baseline must not bypass checksum validation")
	}
	conn, mock := migrationConnection(t)
	expectMigrationHistory(mock, migrationHistoryRows().AddRow(migration.filename, legacyInitialSchemaChecksum, "2026-10-08 04:00:00"))
	expectMigrationUnlock(mock)
	if err := applyMigrations(context.Background(), conn, "bosapi", []sqlMigration{migration}); err == nil || !strings.Contains(err.Error(), "has changed") {
		t.Fatalf("expected checksum error, got %v", err)
	}
}

func TestReadMigrationsSelectsForwardSQLInOrder(t *testing.T) {
	first := "-- comment with a semicolon;\nCREATE TABLE sample (value TEXT); INSERT INTO sample VALUES ('a;b');"
	files := fstest.MapFS{
		"002_\u6570\u636e.sql":  {Data: []byte("INSERT INTO sample VALUES ('c');")},
		"001_create.up.sql":     {Data: []byte(first)},
		"001_create.down.sql":   {Data: []byte("DROP TABLE sample;")},
		"README.md":             {Data: []byte("instructions")},
		"files.go":              {Data: []byte("package migrations")},
		"nested/003_hidden.sql": {Data: []byte("SELECT 1;")},
	}
	migrations, err := readMigrations(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 2 || migrations[0].filename != "001_create.up.sql" || migrations[1].filename != "002_\u6570\u636e.sql" {
		t.Fatalf("unexpected migrations: %+v", migrations)
	}
	sum := sha256.Sum256([]byte(first))
	if migrations[0].script != first || migrations[0].checksum != hex.EncodeToString(sum[:]) {
		t.Fatalf("script was altered or checksum was incorrect: %+v", migrations[0])
	}
}

func TestReadMigrationsRejectsInvalidFiles(t *testing.T) {
	for _, test := range []struct {
		name   string
		script string
		want   string
	}{
		{"001_empty.sql", " \n\t", "is empty"},
		{strings.Repeat("a", 252) + ".sql", "SELECT 1;", "exceeds 255 bytes"},
	} {
		t.Run(test.want, func(t *testing.T) {
			_, err := readMigrations(fstest.MapFS{test.name: {Data: []byte(test.script)}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRetiredMigrationsAreValidatedButNeverExecuted(t *testing.T) {
	files := fstest.MapFS{
		"retired/019_old.sql": {Data: []byte("ALTER TABLE huge_table REBUILD PARTITION ALL;")},
		"021_new.sql":         {Data: []byte("CREATE TABLE empty_table (id INT);")},
	}
	scripts, err := readMigrations(files)
	if err != nil || len(scripts) != 2 || !scripts[0].retired || scripts[1].retired {
		t.Fatalf("unexpected retired migration discovery: %+v, %v", scripts, err)
	}
	for _, applied := range []bool{false, true} {
		conn, mock := migrationConnection(t)
		history := migrationHistoryRows()
		if applied {
			history.AddRow(scripts[0].filename, scripts[0].checksum, "2026-10-30 04:00:00")
		}
		expectMigrationHistory(mock, history)
		expectMigrationApplied(mock, scripts[1])
		expectMigrationUnlock(mock)
		if err := applyMigrations(context.Background(), conn, "bosapi", scripts); err != nil {
			t.Fatal(err)
		}
	}
	conn, mock := migrationConnection(t)
	expectMigrationHistory(mock, migrationHistoryRows().AddRow(scripts[0].filename, "tampered", "2026-10-30 04:00:00"))
	expectMigrationUnlock(mock)
	if err := applyMigrations(context.Background(), conn, "bosapi", scripts); err == nil || !strings.Contains(err.Error(), "has changed") {
		t.Fatalf("retired migration checksum must remain enforced: %v", err)
	}
}

func TestInPlaceLogPartitionMigrationIsRetired(t *testing.T) {
	scripts, err := readMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, script := range scripts {
		if script.filename == "019_request_log_partitions.sql" {
			found = true
			if !script.retired {
				t.Fatal("startup must never rebuild the existing request_logs table")
			}
		}
	}
	if !found {
		t.Fatal("keep the original script for existing database checksum validation")
	}
}

func TestApplyMigrationsWithNoSQLDoesNotConnect(t *testing.T) {
	if err := ApplyMigrations(context.Background(), "invalid DSN", fstest.MapFS{
		"README.md": {Data: []byte("instructions")},
	}); err != nil {
		t.Fatal(err)
	}
}

type unreadableMigrationFS struct{}

func (unreadableMigrationFS) Open(string) (fs.File, error) {
	return nil, errors.New("unreadable")
}

func TestApplyMigrationsReportsReadAndDSNErrors(t *testing.T) {
	if err := ApplyMigrations(context.Background(), "invalid DSN", fstest.MapFS{
		"001.sql": {Data: []byte("SELECT 1;")},
	}); err == nil || !strings.Contains(err.Error(), "parse migration database DSN") {
		t.Fatalf("invalid DSN error = %v", err)
	}
	if err := ApplyMigrations(context.Background(), "", unreadableMigrationFS{}); err == nil || !strings.Contains(err.Error(), "read SQL migrations") {
		t.Fatalf("unreadable directory error = %v", err)
	}
}

func migrationConnection(t *testing.T) (*sql.Conn, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		conn.Close()
		db.Close()
	})
	return conn, mock
}

func expectMigrationHistory(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	mock.ExpectQuery("SELECT GET_LOCK(?, 60)").WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(1))
	mock.ExpectExec(createMigrationTable).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT filename, checksum, applied_at FROM schema_migrations ORDER BY filename").
		WillReturnRows(rows).RowsWillBeClosed()
}

func migrationHistoryRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"filename", "checksum", "applied_at"})
}

func expectMigrationUnlock(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT RELEASE_LOCK(?)").WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"released"}).AddRow(1))
}

func expectMigrationApplied(mock sqlmock.Sqlmock, migration sqlMigration) {
	mock.ExpectExec("INSERT INTO schema_migrations (filename, checksum) VALUES (?, ?)").
		WithArgs(migration.filename, migration.checksum).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(migration.script).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE schema_migrations SET applied_at = UTC_TIMESTAMP(6) WHERE filename = ?").
		WithArgs(migration.filename).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestApplyMigrationsRunsNewFilesThenSkipsThemOnRestart(t *testing.T) {
	conn, mock := migrationConnection(t)
	migrations, err := readMigrations(fstest.MapFS{
		"002_seed.sql":      {Data: []byte("INSERT INTO sample VALUES ('a;b'); INSERT INTO sample VALUES ('c');")},
		"001_create.up.sql": {Data: []byte("CREATE TABLE sample (value TEXT);")},
	})
	if err != nil {
		t.Fatal(err)
	}
	expectMigrationHistory(mock, migrationHistoryRows())
	for _, migration := range migrations {
		expectMigrationApplied(mock, migration)
	}
	expectMigrationUnlock(mock)
	if err := applyMigrations(context.Background(), conn, "bosapi", migrations); err != nil {
		t.Fatal(err)
	}

	// A second startup must read history without running the SQL again.
	rows := migrationHistoryRows()
	for _, migration := range migrations {
		rows.AddRow(migration.filename, migration.checksum, "2026-10-08 04:00:00")
	}
	expectMigrationHistory(mock, rows)
	expectMigrationUnlock(mock)
	if err := applyMigrations(context.Background(), conn, "bosapi", migrations); err != nil {
		t.Fatal(err)
	}
}

func TestApplyMigrationsSkipsExistingFilesAndAppliesNewFile(t *testing.T) {
	conn, mock := migrationConnection(t)
	migrations := []sqlMigration{
		{filename: "001_existing.sql", checksum: "old", script: "SELECT 1;"},
		{filename: "002_new.sql", checksum: "new", script: "ALTER TABLE sample ADD COLUMN label TEXT;"},
	}
	expectMigrationHistory(mock, migrationHistoryRows().AddRow("001_existing.sql", "old", "2026-10-08 04:00:00"))
	expectMigrationApplied(mock, migrations[1])
	expectMigrationUnlock(mock)
	if err := applyMigrations(context.Background(), conn, "bosapi", migrations); err != nil {
		t.Fatal(err)
	}
}

func TestApplyMigrationsChecksHistoryBeforeChangingDatabase(t *testing.T) {
	for _, test := range []struct {
		name      string
		filename  string
		checksum  string
		appliedAt any
		want      string
	}{
		{"changed file", "002_existing.sql", "original", "2026-10-08 04:00:00", "has changed"},
		{"failed file", "002_existing.sql", "checksum", nil, "is unfinished"},
		{"removed failed file", "003_removed.sql", "checksum", nil, "is unfinished"},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn, mock := migrationConnection(t)
			migrations := []sqlMigration{
				{filename: "001_new.sql", checksum: "new", script: "SELECT 1;"},
				{filename: "002_existing.sql", checksum: "checksum", script: "SELECT 2;"},
			}
			expectMigrationHistory(mock, migrationHistoryRows().AddRow(test.filename, test.checksum, test.appliedAt))
			expectMigrationUnlock(mock)
			err := applyMigrations(context.Background(), conn, "bosapi", migrations)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), test.filename) {
				t.Fatalf("error = %v, want %s for %s", err, test.want, test.filename)
			}
		})
	}
}

func TestApplyMigrationsFailureLeavesFileUnfinishedAndStops(t *testing.T) {
	for _, failAt := range []string{"execute", "record completion"} {
		t.Run(failAt, func(t *testing.T) {
			conn, mock := migrationConnection(t)
			migrations := []sqlMigration{
				{filename: "001_bad.sql", checksum: "checksum", script: "ALTER TABLE sample ADD COLUMN label TEXT;"},
				{filename: "002_later.sql", checksum: "later", script: "SELECT 2;"},
			}
			expectMigrationHistory(mock, migrationHistoryRows())
			mock.ExpectExec("INSERT INTO schema_migrations (filename, checksum) VALUES (?, ?)").
				WithArgs("001_bad.sql", "checksum").WillReturnResult(sqlmock.NewResult(0, 1))
			failure := errors.New("database failure")
			if failAt == "execute" {
				mock.ExpectExec(migrations[0].script).WillReturnError(failure)
			} else {
				mock.ExpectExec(migrations[0].script).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("UPDATE schema_migrations SET applied_at = UTC_TIMESTAMP(6) WHERE filename = ?").
					WithArgs("001_bad.sql").WillReturnError(failure)
			}
			expectMigrationUnlock(mock)
			err := applyMigrations(context.Background(), conn, "bosapi", migrations)
			if !errors.Is(err, failure) || !strings.Contains(err.Error(), "001_bad.sql") {
				t.Fatalf("error = %v, want migration filename and original failure", err)
			}
		})
	}
}

func TestApplyMigrationsDoesNotRunWithoutLock(t *testing.T) {
	for _, result := range []any{0, nil} {
		conn, mock := migrationConnection(t)
		mock.ExpectQuery("SELECT GET_LOCK(?, 60)").WithArgs(sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(result))
		err := applyMigrations(context.Background(), conn, "bosapi", []sqlMigration{{filename: "001.sql", script: "SELECT 1;"}})
		if err == nil || !strings.Contains(err.Error(), "could not acquire SQL migration lock") {
			t.Fatalf("error = %v, want lock acquisition error", err)
		}
	}
}
