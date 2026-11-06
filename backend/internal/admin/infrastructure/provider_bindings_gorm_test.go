package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"bosapi/backend/internal/admin/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestListProviderBindingsPreservesSavedConfiguration(t *testing.T) {
	for _, scenario := range []string{"saved", "empty", "missing", "invalid_json"} {
		t.Run(scenario, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			orgs := sqlmock.NewRows([]string{"id"})
			if scenario != "missing" {
				orgs.AddRow(1)
			}
			mock.ExpectQuery("SELECT \\* FROM `organizations`").WithArgs(1, 1).WillReturnRows(orgs)
			if scenario != "missing" {
				bindings := sqlmock.NewRows([]string{"organization_id", "account_id", "priority", "status", "model_allowlist"})
				if scenario == "saved" {
					bindings.AddRow(1, 1, 2, "active", `["glm"]`).AddRow(1, 2, 1, "disabled", `null`)
				} else if scenario == "invalid_json" {
					bindings.AddRow(1, 1, 2, "active", `broken`)
				}
				mock.ExpectQuery("SELECT \\* FROM `organization_provider_bindings` WHERE organization_id = \\? ORDER BY priority DESC, account_id ASC").
					WithArgs(1).WillReturnRows(bindings)
			}
			items, err := NewProviderBindingRepository(db).ListProviderBindings(context.Background(), 1)
			switch scenario {
			case "missing":
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("err=%v", err)
				}
			case "invalid_json":
				if err == nil {
					t.Fatal("invalid saved policy was silently replaced")
				}
			case "empty":
				if err != nil || items == nil || len(items) != 0 {
					t.Fatalf("items=%+v err=%v", items, err)
				}
			case "saved":
				if err != nil || len(items) != 2 || items[0].AccountID != 1 || items[0].Priority != 2 || items[0].ModelAllowlist[0] != "glm" || items[1].Status != "disabled" || items[1].ModelAllowlist == nil {
					t.Fatalf("saved bindings changed: %+v err=%v", items, err)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBindingSaveSynchronizesOrganizationInTheSameTransaction(t *testing.T) {
	for _, test := range []struct {
		name              string
		bindings          []domain.ProviderBinding
		providers, models string
		failAt            string
	}{
		{name: "active_union_excludes_paused_and_deduplicates_models", bindings: []domain.ProviderBinding{
			{AccountID: 2, Priority: 1, Status: "active", ModelAllowlist: []string{"glm", "deepseek"}},
			{AccountID: 1, Priority: 2, Status: "active", ModelAllowlist: []string{"glm"}},
			{AccountID: 3, Priority: 7, Status: "disabled", ModelAllowlist: []string{"paused-model"}},
		}, providers: `[1,2]`, models: `["deepseek","glm"]`},
		{name: "unrestricted_binding_clears_global_model_filter", bindings: []domain.ProviderBinding{
			{AccountID: 1, Status: "active", ModelAllowlist: []string{"glm"}},
			{AccountID: 2, Status: "active", ModelAllowlist: []string{}},
		}, providers: `[1,2]`, models: `[]`},
		{name: "removed_bindings_clear_stale_organization_policy", providers: `[]`, models: `[]`},
		{name: "paused_bindings_are_not_in_organization_allowlists", bindings: []domain.ProviderBinding{{AccountID: 1, Status: "disabled", ModelAllowlist: []string{"glm"}}}, providers: `[]`, models: `[]`},
		{name: "organization_update_failure_rolls_back_binding_write", bindings: []domain.ProviderBinding{{AccountID: 1, Status: "active", ModelAllowlist: []string{"glm"}}}, providers: `[1]`, models: `["glm"]`, failAt: "organization"},
		{name: "binding_insert_failure_does_not_update_organization", bindings: []domain.ProviderBinding{{AccountID: 1, Status: "active", ModelAllowlist: []string{"glm"}}}, failAt: "binding"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT \\* FROM `organizations` .* FOR UPDATE").WithArgs(3, 1).
				WillReturnRows(sqlmock.NewRows([]string{"id", "allowed_provider_ids", "allowed_models"}).AddRow(3, `[99]`, `["old-model"]`))
			if len(test.bindings) > 0 {
				mock.ExpectQuery("SELECT count\\(\\*\\) FROM `accounts` WHERE id IN").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(len(test.bindings)))
			}
			mock.ExpectExec("DELETE FROM `organization_provider_bindings` WHERE organization_id = \\?").WithArgs(3).
				WillReturnResult(sqlmock.NewResult(0, 2))
			failure := errors.New("write failed")
			for _, binding := range test.bindings {
				models, _ := json.Marshal(binding.ModelAllowlist)
				insert := mock.ExpectExec("INSERT INTO `organization_provider_bindings`").WithArgs(3, binding.AccountID, binding.Priority, string(models), binding.Status)
				if test.failAt == "binding" {
					insert.WillReturnError(failure)
					break
				}
				insert.WillReturnResult(sqlmock.NewResult(0, 1))
			}
			if test.failAt != "binding" {
				update := mock.ExpectExec("UPDATE `organizations` SET `allowed_models`=\\?,`allowed_provider_ids`=\\?,`updated_at`=\\? WHERE id = \\?").
					WithArgs(test.models, test.providers, sqlmock.AnyArg(), 3)
				if test.failAt == "organization" {
					update.WillReturnError(failure)
				} else {
					update.WillReturnResult(sqlmock.NewResult(0, 1))
				}
			}
			if test.failAt != "" {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			err = NewProviderBindingRepository(db).ReplaceProviderBindings(context.Background(), 3, test.bindings)
			if test.failAt != "" && !errors.Is(err, failure) || test.failAt == "" && err != nil {
				t.Fatalf("err=%v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
