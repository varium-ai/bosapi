package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"bosapi/backend/internal/admin/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func newOrganizationSyncDatabase(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	return db, mock
}

func TestOrganizationPolicyEditsSynchronizeBindingsAtomically(t *testing.T) {
	active := domain.ProviderBinding{AccountID: 1, Priority: 7, Status: "active", ModelAllowlist: []string{"old"}}
	paused := domain.ProviderBinding{AccountID: 2, Priority: 9, Status: "disabled", ModelAllowlist: []string{"paused"}}
	for _, test := range []struct {
		name                                 string
		existing, saved                      []domain.ProviderBinding
		ids                                  []uint64
		models, effective                    []string
		wantIDs, wantModels                  string
		modelRead, writeBindings, failUpdate bool
	}{
		{name: "initialize_missing_bindings_from_organization", ids: []uint64{1}, models: []string{"codex-auto-review"}, saved: []domain.ProviderBinding{{AccountID: 1, Status: "active", ModelAllowlist: []string{"codex-auto-review"}}}, wantIDs: `[1]`, wantModels: `["codex-auto-review"]`, modelRead: true, writeBindings: true},
		{name: "model_edit_keeps_priorities_and_paused_channels", existing: []domain.ProviderBinding{active, paused}, ids: []uint64{1}, models: []string{"new"}, effective: []string{"old"}, saved: []domain.ProviderBinding{{AccountID: 1, Priority: 7, Status: "active", ModelAllowlist: []string{"new"}}, paused}, wantIDs: `[1]`, wantModels: `["new"]`, modelRead: true, writeBindings: true},
		{name: "channel_edit_replaces_active_membership", existing: []domain.ProviderBinding{active}, ids: []uint64{2}, models: []string{"old"}, effective: []string{"old"}, saved: []domain.ProviderBinding{{AccountID: 2, Status: "active", ModelAllowlist: []string{"old"}}}, wantIDs: `[2]`, wantModels: `["old"]`, modelRead: true, writeBindings: true},
		{name: "unchanged_policy_does_not_rewrite_binding_details", existing: []domain.ProviderBinding{active, paused}, ids: []uint64{1}, models: []string{"old"}, effective: []string{"old"}, saved: []domain.ProviderBinding{active, paused}, wantIDs: `[1]`, wantModels: `["old"]`},
		{name: "unchanged_effective_models_keep_unrestricted_binding", existing: []domain.ProviderBinding{{AccountID: 1, Priority: 7, Status: "active", ModelAllowlist: []string{}}}, ids: []uint64{1}, models: []string{"old"}, effective: []string{"old"}, wantIDs: `[1]`, wantModels: `[]`},
		{name: "unchanged_union_keeps_channel_specific_model_filters", existing: []domain.ProviderBinding{active, {AccountID: 2, Priority: 9, Status: "active", ModelAllowlist: []string{"b"}}}, ids: []uint64{2, 1}, models: []string{"old", "b"}, effective: []string{"b", "old"}, wantIDs: `[1,2]`, wantModels: `["b","old"]`},
		{name: "clear_models_means_all_models_on_retained_channel", existing: []domain.ProviderBinding{active}, ids: []uint64{1}, models: []string{}, effective: []string{"old"}, saved: []domain.ProviderBinding{{AccountID: 1, Priority: 7, Status: "active", ModelAllowlist: []string{}}}, wantIDs: `[1]`, wantModels: `[]`, writeBindings: true},
		{name: "clear_channels_removes_bindings", existing: []domain.ProviderBinding{active}, ids: []uint64{}, models: []string{}, effective: []string{"old"}, wantIDs: `[]`, wantModels: `[]`, writeBindings: true},
		{name: "paused_policy_stays_paused_on_unchanged_edit", existing: []domain.ProviderBinding{paused}, ids: []uint64{}, models: []string{}, saved: []domain.ProviderBinding{paused}, wantIDs: `[]`, wantModels: `[]`},
		{name: "organization_write_failure_rolls_back_bindings", existing: []domain.ProviderBinding{active}, ids: []uint64{1}, models: []string{"new"}, effective: []string{"old"}, saved: []domain.ProviderBinding{{AccountID: 1, Priority: 7, Status: "active", ModelAllowlist: []string{"new"}}}, wantIDs: `[1]`, wantModels: `["new"]`, modelRead: true, writeBindings: true, failUpdate: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock := newOrganizationSyncDatabase(t)
			mock.ExpectBegin()
			// Matches the historical screenshot: the organization has explicit
			// policy even if no binding rows have ever been saved.
			mock.ExpectQuery("SELECT \\* FROM `organizations` .* FOR UPDATE").WithArgs(3, 1).
				WillReturnRows(sqlmock.NewRows([]string{"id", "name", "allowed_provider_ids", "allowed_models"}).AddRow(3, "rolle", `[1]`, `["codex-auto-review"]`))
			records := sqlmock.NewRows([]string{"organization_id", "account_id", "priority", "status", "model_allowlist"})
			hasActive := false
			for _, binding := range test.existing {
				models, _ := json.Marshal(binding.ModelAllowlist)
				records.AddRow(3, binding.AccountID, binding.Priority, binding.Status, string(models))
				hasActive = hasActive || binding.Status == "active"
			}
			mock.ExpectQuery("SELECT \\* FROM `organization_provider_bindings` WHERE organization_id = \\? ORDER BY account_id ASC").WithArgs(3).WillReturnRows(records)
			if hasActive {
				models := sqlmock.NewRows([]string{"organization_id", "model_name"})
				for _, model := range test.effective {
					models.AddRow(3, model)
				}
				mock.ExpectQuery("SELECT DISTINCT b.organization_id, pm.model_name").WithArgs(3, "active", "active", "active", true).WillReturnRows(models)
			}
			if test.modelRead {
				models := sqlmock.NewRows([]string{"account_id", "model_name"})
				for _, id := range test.ids {
					for _, model := range test.models {
						models.AddRow(id, model)
					}
				}
				mock.ExpectQuery("SELECT account_id, model_name FROM `provider_models`").WillReturnRows(models)
			}
			if test.writeBindings {
				if len(test.saved) > 0 {
					mock.ExpectQuery("SELECT count\\(\\*\\) FROM `accounts`").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(len(test.saved)))
				}
				mock.ExpectExec("DELETE FROM `organization_provider_bindings`").WithArgs(3).WillReturnResult(sqlmock.NewResult(0, int64(len(test.existing))))
				for _, binding := range test.saved {
					models, _ := json.Marshal(binding.ModelAllowlist)
					mock.ExpectExec("INSERT INTO `organization_provider_bindings`").WithArgs(3, binding.AccountID, binding.Priority, string(models), binding.Status).WillReturnResult(sqlmock.NewResult(0, 1))
				}
			}
			failure := errors.New("organization write failed")
			update := mock.ExpectExec("UPDATE `organizations` SET `allowed_models`=\\?,`allowed_provider_ids`=\\?,`updated_at`=\\? WHERE id = \\?").WithArgs(test.wantModels, test.wantIDs, sqlmock.AnyArg(), 3)
			if test.failUpdate {
				update.WillReturnError(failure)
				mock.ExpectRollback()
			} else {
				update.WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			updates := map[string]any{"allowed_provider_ids": test.ids, "allowed_models": test.models}
			err := NewOrganizationRepository(db).Update(context.Background(), 3, updates)
			if test.failUpdate && !errors.Is(err, failure) || !test.failUpdate && err != nil {
				t.Fatalf("err=%v", err)
			}
			if _, ok := updates["allowed_provider_ids"].([]uint64); !ok {
				t.Fatal("caller policy was mutated")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLegacyOrganizationPolicyAppearsInBindingsWithoutWriting(t *testing.T) {
	db, mock := newOrganizationSyncDatabase(t)
	mock.ExpectQuery("SELECT \\* FROM `organizations`").WithArgs(3, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "allowed_provider_ids", "allowed_models"}).AddRow(3, "rolle", `[1]`, `["codex-auto-review"]`))
	mock.ExpectQuery("SELECT \\* FROM `organization_provider_bindings`").WithArgs(3).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "account_id"}))
	mock.ExpectQuery("SELECT account_id, model_name FROM `provider_models`").WithArgs(1, "active").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "model_name"}).AddRow(1, "codex-auto-review"))
	bindings, err := NewProviderBindingRepository(db).ListProviderBindings(context.Background(), 3)
	if err != nil || len(bindings) != 1 || bindings[0].AccountID != 1 || bindings[0].Status != "active" || bindings[0].ModelAllowlist[0] != "codex-auto-review" {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOrganizationModelDistributionNeverTreatsNoMatchAsUnrestricted(t *testing.T) {
	db, mock := newOrganizationSyncDatabase(t)
	mock.ExpectQuery("SELECT account_id, model_name FROM `provider_models`").WithArgs(1, 2, "active").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "model_name"}).AddRow(1, "glm").AddRow(2, "deepseek"))
	bindings, err := organizationPolicyBindings(db, []uint64{1, 2}, []string{"glm"}, nil, true)
	if err != nil || len(bindings) != 2 || len(bindings[1].ModelAllowlist) != 1 || bindings[1].ModelAllowlist[0] != "glm" {
		t.Fatalf("no-match channel gained unrestricted access: %+v err=%v", bindings, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOrganizationCreationStoresInitialBindingsAtomically(t *testing.T) {
	for _, invalidProvider := range []bool{false, true} {
		t.Run(map[bool]string{false: "creates_initial_bindings", true: "invalid_provider_rolls_back_organization"}[invalidProvider], func(t *testing.T) {
			db, mock := newOrganizationSyncDatabase(t)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT setting_key FROM settings").WithArgs("community.capacity_lock").WillReturnRows(sqlmock.NewRows([]string{"setting_key"}).AddRow("community.capacity_lock"))
			mock.ExpectQuery("SELECT count.*FROM `organizations`").WithArgs("deleted").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			mock.ExpectExec("INSERT INTO `organizations`").WillReturnResult(sqlmock.NewResult(3, 1))
			mock.ExpectQuery("SELECT account_id, model_name FROM `provider_models`").WithArgs(1, "active").
				WillReturnRows(sqlmock.NewRows([]string{"account_id", "model_name"}).AddRow(1, "codex-auto-review"))
			count := 1
			if invalidProvider {
				count = 0
			}
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `accounts`").WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
			if invalidProvider {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec("DELETE FROM `organization_provider_bindings`").WithArgs(3).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("INSERT INTO `organization_provider_bindings`").WithArgs(3, 1, 0, `["codex-auto-review"]`, "active").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("UPDATE `organizations`").WithArgs(`["codex-auto-review"]`, `[1]`, sqlmock.AnyArg(), 3).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			organization := domain.Organization{Name: "rolle", Status: "active", AllowedProviderIDs: []uint64{1}, AllowedModels: []string{"codex-auto-review"}}
			err := NewOrganizationRepository(db).Create(context.Background(), &organization)
			if invalidProvider {
				if !errors.Is(err, gorm.ErrRecordNotFound) || organization.ID != 0 {
					t.Fatalf("failed creation leaked a tenant: %+v err=%v", organization, err)
				}
			} else if err != nil || organization.ID != 3 || !organization.HasActiveBindings {
				t.Fatalf("organization=%+v err=%v", organization, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOrganizationMetadataOnlyUpdateLeavesPolicyUntouched(t *testing.T) {
	db, mock := newOrganizationSyncDatabase(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `organizations` SET `description`=\\?,`updated_at`=\\? WHERE id = \\?").WithArgs("new description", sqlmock.AnyArg(), 3).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := NewOrganizationRepository(db).Update(context.Background(), 3, map[string]any{"description": "new description"}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateOrganizationPersistsReviewedBindingsAtomically(t *testing.T) {
	for _, failBinding := range []bool{false, true} {
		t.Run(fmt.Sprint(failBinding), func(t *testing.T) {
			db, mock := newOrganizationSyncDatabase(t)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT setting_key FROM settings").WithArgs("community.capacity_lock").WillReturnRows(sqlmock.NewRows([]string{"setting_key"}).AddRow("community.capacity_lock"))
			mock.ExpectQuery("SELECT count.*FROM `organizations`").WithArgs("deleted").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			mock.ExpectExec("INSERT INTO `organizations`").WillReturnResult(sqlmock.NewResult(17, 1))
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `accounts`").WithArgs(uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			mock.ExpectExec("DELETE FROM `organization_provider_bindings`").WithArgs(17).WillReturnResult(sqlmock.NewResult(0, 0))
			insert := mock.ExpectExec("INSERT INTO `organization_provider_bindings`").WithArgs(17, uint64(2), 19, `["private-model"]`, "disabled")
			if failBinding {
				insert.WillReturnError(errors.New("binding write failed"))
				mock.ExpectRollback()
			} else {
				insert.WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("UPDATE `organizations`").WithArgs(`[]`, `[]`, sqlmock.AnyArg(), 17).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			organization := domain.Organization{Name: "reviewed copy", Status: "active", InitialBindings: []domain.ProviderBinding{{AccountID: 2, Priority: 19, Status: "disabled", ModelAllowlist: []string{"private-model"}}}}
			err := NewOrganizationRepository(db).Create(context.Background(), &organization)
			if (err != nil) != failBinding {
				t.Fatalf("unexpected creation error: %v", err)
			}
			if !failBinding && (!organization.HasProviderBindings || organization.HasActiveBindings) {
				t.Fatal("paused copied bindings must not be reported as active")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
