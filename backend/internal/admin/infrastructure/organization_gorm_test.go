package infrastructure

import (
	"context"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestOrganizationRepositoryListNumericSearch(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewOrganizationRepository(database)

	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `organizations` WHERE name LIKE \\? OR id = \\?").
		WithArgs("%1%", uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	mock.ExpectQuery("SELECT \\* FROM `organizations` WHERE name LIKE \\? OR id = \\? ORDER BY id ASC LIMIT \\?").
		WithArgs("%1%", uint64(1), 20).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "description", "status", "created_at", "updated_at"}).
			AddRow(1, "default", "default org", "active", time.Now(), time.Now()))
	mock.ExpectQuery("SELECT \\* FROM `organization_provider_bindings`").WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "account_id", "status"}))
	mock.ExpectQuery("SELECT DISTINCT `organization_id` FROM `api_keys` WHERE organization_id IN \\(\\?\\)").WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}))

	orgs, total, err := repo.List(context.Background(), domain.OrganizationFilter{Search: "1"}, 1, 20)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if total != 1 || len(orgs) != 1 || orgs[0].ID != 1 {
		t.Fatalf("expected 1 organization with id 1, got total=%d len=%d", total, len(orgs))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOrganizationReadsProjectActiveBindingsOverStaleAllowlists(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewOrganizationRepository(database)
	mock.ExpectQuery("SELECT \\* FROM `organizations`").WithArgs(1, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status", "allowed_provider_ids", "allowed_models"}).
			AddRow(1, "Platform Administration", "active", `[2]`, `["deepseek"]`))
	mock.ExpectQuery("SELECT \\* FROM `organization_provider_bindings`").WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "account_id", "status", "model_allowlist"}).
			AddRow(1, 1, "active", `[]`).AddRow(1, 2, "active", `[]`))
	mock.ExpectQuery("SELECT DISTINCT b.organization_id, pm.model_name").WithArgs(1, "active", "active", "active", true).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "model_name"}).AddRow(1, "deepseek").AddRow(1, "glm"))
	mock.ExpectQuery("SELECT DISTINCT `organization_id` FROM `api_keys`").WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(1))
	organization, err := repo.Find(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !organization.HasActiveBindings || len(organization.AllowedProviderIDs) != 2 || len(organization.AllowedModels) != 2 || organization.AllowedModels[1] != "glm" {
		t.Fatalf("stale organization policy leaked into display: %+v", organization)
	}
	if !organization.HasAPIKeys {
		t.Fatal("existing organization key was not projected")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOrganizationKeyPresenceIsBatchedAndScoped(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewOrganizationRepository(database)
	items := []domain.Organization{{ID: 3}, {ID: 7}, {ID: 9}}
	mock.ExpectQuery("SELECT DISTINCT `organization_id` FROM `api_keys` WHERE organization_id IN \\(\\?,\\?,\\?\\)").
		WithArgs(3, 7, 9).WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(7))
	if err := repo.applyKeyPresence(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	if items[0].HasAPIKeys || !items[1].HasAPIKeys || items[2].HasAPIKeys {
		t.Fatalf("key presence leaked across organizations: %+v", items)
	}
	// An empty page must not issue a global key read.
	if err := repo.applyKeyPresence(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT DISTINCT `organization_id` FROM `api_keys`").WithArgs(3).
		WillReturnError(gorm.ErrInvalidDB)
	if err := repo.applyKeyPresence(context.Background(), items[:1]); err == nil {
		t.Fatal("a failed status read must not report an uninitialized organization")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
