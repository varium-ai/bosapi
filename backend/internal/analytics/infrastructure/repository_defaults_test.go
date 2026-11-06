package infrastructure

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"bosapi/backend/internal/analytics/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func defaultRepository(t *testing.T) (*Repository, sqlmock.Sqlmock) {
	t.Helper()
	connection, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: connection, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	return NewRepository(db), mock
}

func defaultSnapshot(initial domain.Report, configured bool, setting string) *sqlmock.Rows {
	var since, cursor any
	installation, epoch := "", ""
	revision := 0
	if configured {
		revision = 1
	}
	if configured && setting == `{"enabled":true}` {
		installation, epoch = initial.InstallationID, initial.CollectionEpoch
		since, cursor = initial.GeneratedAt, domain.DayStart(initial.GeneratedAt)
	}
	return sqlmock.NewRows([]string{"id", "installation_id", "collection_epoch", "enabled_since", "scan_cursor", "config_revision", "setting_json", "configured"}).
		AddRow(1, installation, epoch, since, cursor, revision, setting, configured)
}

type enabledSetting bool

func (expected enabledSetting) Match(value driver.Value) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	var setting struct{ Enabled bool }
	return json.Unmarshal([]byte(text), &setting) == nil && setting.Enabled == bool(expected)
}

func TestDefaultInitializationKeepsEveryPersistedChoice(t *testing.T) {
	for _, setting := range []string{`{"enabled":false}`, `{"enabled":true}`, ""} {
		t.Run(setting, func(t *testing.T) {
			repo, mock := defaultRepository(t)
			initial := domain.Installation(domain.Metadata{Version: "v1.0.0", OS: "linux", Arch: "amd64"}, time.Now())
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT s.*FOR UPDATE").WithArgs(domain.SettingKey).WillReturnRows(defaultSnapshot(initial, true, setting))
			mock.ExpectCommit()
			if err := repo.InitializeDefault(context.Background(), initial); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFirstDefaultEnableAndExplicitDisableArePersistedAtomically(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name := "explicit_opt_out"
		if enabled {
			name = "initial_default"
		}
		t.Run(name, func(t *testing.T) {
			repo, mock := defaultRepository(t)
			now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
			initial := domain.Installation(domain.Metadata{Version: "v1.0.0", OS: "linux", Arch: "amd64"}, now)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT s.*FOR UPDATE").WithArgs(domain.SettingKey).WillReturnRows(defaultSnapshot(initial, false, ""))
			mock.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(now))
			mock.ExpectExec("UPDATE `usage_reporting_outbox`").WillReturnResult(sqlmock.NewResult(0, 0))
			if enabled {
				mock.ExpectExec("INSERT INTO `usage_reporting_outbox`").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery("SELECT `report_id` FROM `usage_reporting_outbox`").WillReturnRows(sqlmock.NewRows([]string{"report_id"}))
			}
			mock.ExpectExec("UPDATE `usage_reporting_state`").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO settings").WithArgs(domain.SettingKey, enabledSetting(enabled), nil, now).WillReturnResult(sqlmock.NewResult(0, 1))
			action, setting := "disabled", `{"enabled":false}`
			if enabled {
				action, setting = "enabled", `{"enabled":true}`
			}
			mock.ExpectExec("INSERT INTO usage_reporting_events").WithArgs(action, nil, now).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT s").WithArgs(domain.SettingKey).WillReturnRows(defaultSnapshot(initial, true, setting))
			mock.ExpectCommit()
			var err error
			if enabled {
				err = repo.InitializeDefault(context.Background(), initial)
			} else {
				_, err = repo.Toggle(context.Background(), false, nil, initial)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDefaultInitializationDoesNotRepairCorruptSavedConfiguration(t *testing.T) {
	repo, mock := defaultRepository(t)
	initial := domain.Installation(domain.Metadata{}, time.Now())
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT s.*FOR UPDATE").WillReturnRows(defaultSnapshot(initial, true, `{"enabled":"invalid"}`))
	mock.ExpectRollback()
	if err := repo.InitializeDefault(context.Background(), initial); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
