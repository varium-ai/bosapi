package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/mail"
	"sort"
	"strings"
	"time"

	authapp "bosapi/backend/internal/auth/application"
	"bosapi/backend/internal/platform/logging"
	"github.com/go-sql-driver/mysql"
)

const createMigrationTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
    filename VARCHAR(255) COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    checksum CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    applied_at DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`

type sqlMigration struct {
	filename string
	checksum string
	script   string
	retired  bool
}

// InitialAdmin supplies the initial login credentials; only the password hash
// is sent to MySQL. Credentials are ignored once the initial migration is done.
type InitialAdmin struct {
	Email    string
	Password string
}

const initialSchemaFilename = "001_initial_schema.sql"
const initialAdminMarker = "\n-- BEGIN INITIAL SUPER ADMIN BOOTSTRAP\n"
const legacyInitialSchemaChecksum = "dc40b66606e81206178199382a54b22aee97d938a68070a2d77d91297842948c"

// ApplyMigrations runs embedded forward SQL files before the HTTP server starts.
// MySQL DDL implicitly commits, so an unfinished migration blocks subsequent
// startups until its partial changes have been repaired explicitly.
func ApplyMigrations(ctx context.Context, dsn string, files fs.FS, initialAdmin ...InitialAdmin) error {
	migrations, err := readMigrations(files)
	if err != nil || len(migrations) == 0 {
		return err
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("parse migration database DSN: %w", err)
	}
	// Enable multiple statements only on this dedicated migration connection.
	config.MultiStatements = true
	connector, err := mysql.NewConnector(config)
	if err != nil {
		return fmt.Errorf("configure migration database: %w", err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("connect migration database: %w", err)
	}
	defer conn.Close()
	return applyMigrations(ctx, conn, config.DBName, migrations, initialAdmin...)
}

// initialAdminUpgrade accepts only the exact pre-bootstrap baseline, with its
// original bytes intact. Other edits still fail migration checksum validation.
func initialAdminUpgrade(migration sqlMigration, appliedChecksum string) (string, bool) {
	if migration.filename != initialSchemaFilename || appliedChecksum != legacyInitialSchemaChecksum {
		return "", false
	}
	index := strings.Index(migration.script, initialAdminMarker)
	if index < 0 {
		return "", false
	}
	sum := sha256.Sum256([]byte(migration.script[:index]))
	if hex.EncodeToString(sum[:]) != legacyInitialSchemaChecksum {
		return "", false
	}
	return migration.script[index:], true
}

func bindInitialAdmin(ctx context.Context, conn *sql.Conn, credentials []InitialAdmin) error {
	var admin InitialAdmin
	if len(credentials) > 0 {
		admin = credentials[0]
	}
	email := strings.ToLower(strings.TrimSpace(admin.Email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(email) > 320 || !strings.Contains(email, "@") {
		return errors.New("set a valid BOS_ADMIN_EMAIL for initial administrator creation")
	}
	hash, err := authapp.HashPassword(admin.Password)
	if err != nil {
		return fmt.Errorf("invalid BOS_ADMIN_PASSWORD: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "SET @bos_initial_admin_email = ?, @bos_initial_admin_password_hash = ?", email, hash); err != nil {
		return fmt.Errorf("bind initial administrator credentials: %w", err)
	}
	return nil
}

func readMigrations(files fs.FS) ([]sqlMigration, error) {
	entries, err := fs.ReadDir(files, ".") // ReadDir sorts by filename.
	if err != nil {
		return nil, fmt.Errorf("read SQL migrations: %w", err)
	}
	var migrations []sqlMigration
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".down.sql") {
			continue
		}
		if len(name) > 255 {
			return nil, fmt.Errorf("migration filename exceeds 255 bytes: %s", name)
		}
		script, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		if strings.TrimSpace(string(script)) == "" {
			return nil, fmt.Errorf("migration %s is empty", name)
		}
		sum := sha256.Sum256(script)
		migrations = append(migrations, sqlMigration{filename: name, checksum: hex.EncodeToString(sum[:]), script: string(script)})
	}
	// Retired scripts remain immutable and checksum-validated for databases that
	// already ran them, but are never executed against a new or older database.
	retired, err := fs.ReadDir(files, "retired")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read retired SQL migrations: %w", err)
	}
	for _, entry := range retired {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".down.sql") {
			continue
		}
		if len(name) > 255 {
			return nil, fmt.Errorf("migration filename exceeds 255 bytes: %s", name)
		}
		for _, migration := range migrations {
			if migration.filename == name {
				return nil, fmt.Errorf("SQL migration %s is both active and retired", name)
			}
		}
		script, err := fs.ReadFile(files, "retired/"+name)
		if err != nil {
			return nil, fmt.Errorf("read retired migration %s: %w", name, err)
		}
		if strings.TrimSpace(string(script)) == "" {
			return nil, fmt.Errorf("migration %s is empty", name)
		}
		sum := sha256.Sum256(script)
		migrations = append(migrations, sqlMigration{filename: name, checksum: hex.EncodeToString(sum[:]), script: string(script), retired: true})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].filename < migrations[j].filename })
	return migrations, nil
}

func applyMigrations(ctx context.Context, conn *sql.Conn, databaseName string, migrations []sqlMigration, initialAdmin ...InitialAdmin) (resultErr error) {
	// Named locks belong to a MySQL session; keep all operations on this connection.
	lockHash := sha256.Sum256([]byte("bosapi:migrations:" + databaseName))
	lockName := hex.EncodeToString(lockHash[:])
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", lockName).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire SQL migration lock: %w", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return errors.New("could not acquire SQL migration lock within 60 seconds")
	}
	defer func() {
		// Release the lock even if the migration context was cancelled.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released sql.NullInt64
		if err := conn.QueryRowContext(cleanupCtx, "SELECT RELEASE_LOCK(?)", lockName).Scan(&released); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("release SQL migration lock: %w", err))
		} else if !released.Valid || released.Int64 != 1 {
			resultErr = errors.Join(resultErr, errors.New("SQL migration lock was lost"))
		}
	}()
	if _, err := conn.ExecContext(ctx, createMigrationTable); err != nil {
		return fmt.Errorf("create SQL migration history: %w", err)
	}
	rows, err := conn.QueryContext(ctx, "SELECT filename, checksum, applied_at FROM schema_migrations ORDER BY filename")
	if err != nil {
		return fmt.Errorf("read SQL migration history: %w", err)
	}
	defer rows.Close()
	applied := make(map[string]string)
	for rows.Next() {
		var name, checksum string
		var appliedAt sql.NullString
		if err := rows.Scan(&name, &checksum, &appliedAt); err != nil {
			return fmt.Errorf("read SQL migration history row: %w", err)
		}
		if !appliedAt.Valid {
			return fmt.Errorf("migration %s is unfinished; repair its partial changes and schema_migrations entry before redeploying", name)
		}
		applied[name] = checksum
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read SQL migration history: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close SQL migration history: %w", err)
	}
	// Validate all known scripts before executing any new changes.
	upgrades := make(map[string]string)
	for _, migration := range migrations {
		if checksum, exists := applied[migration.filename]; exists && checksum != migration.checksum {
			if script, ok := initialAdminUpgrade(migration, checksum); ok {
				upgrades[migration.filename] = script
			} else {
				return fmt.Errorf("applied migration %s has changed; restore it and add a new SQL file instead", migration.filename)
			}
		}
	}
	for _, migration := range migrations {
		if migration.retired {
			continue
		}
		upgradeScript, upgrading := upgrades[migration.filename]
		if _, exists := applied[migration.filename]; exists && !upgrading {
			continue
		}
		if migration.filename == initialSchemaFilename {
			if err := bindInitialAdmin(ctx, conn, initialAdmin); err != nil {
				return err
			}
		}
		script := migration.script
		if upgrading {
			// Run only the additive bootstrap, without replaying the baseline.
			script = upgradeScript
			if _, err := conn.ExecContext(ctx, "UPDATE schema_migrations SET applied_at = NULL WHERE filename = ?", migration.filename); err != nil {
				return fmt.Errorf("record migration %s upgrade as started: %w", migration.filename, err)
			}
		} else {
			if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migrations (filename, checksum) VALUES (?, ?)", migration.filename, migration.checksum); err != nil {
				return fmt.Errorf("record migration %s as started: %w", migration.filename, err)
			}
		}
		logging.Printf(ctx, "INFO", "database.migration.started", "filename=%s", migration.filename)
		if _, err := conn.ExecContext(ctx, script); err != nil {
			return fmt.Errorf("execute migration %s (marked unfinished): %w", migration.filename, err)
		}
		if upgrading {
			if _, err := conn.ExecContext(ctx, "UPDATE schema_migrations SET checksum = ?, applied_at = UTC_TIMESTAMP(6) WHERE filename = ?", migration.checksum, migration.filename); err != nil {
				return fmt.Errorf("record migration %s upgrade as applied: %w", migration.filename, err)
			}
		} else {
			if _, err := conn.ExecContext(ctx, "UPDATE schema_migrations SET applied_at = UTC_TIMESTAMP(6) WHERE filename = ?", migration.filename); err != nil {
				return fmt.Errorf("record migration %s as applied: %w", migration.filename, err)
			}
		}
		logging.Printf(ctx, "INFO", "database.migration.applied", "filename=%s", migration.filename)
	}
	return nil
}
