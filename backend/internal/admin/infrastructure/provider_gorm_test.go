package infrastructure

import (
	"strings"
	"testing"

	"bosapi/backend/internal/admin/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestApplyProviderVisibilityForSuperAdminIncludesAllChannels(t *testing.T) {
	database, closeDatabase := providerVisibilityTestDatabase(t)
	defer closeDatabase()

	query := applyProviderVisibility(database.Model(&ProviderRecord{}), domain.ProviderActor{Role: "super_admin"}).Find(&[]ProviderRecord{})
	sql := query.Statement.SQL.String()
	if strings.Contains(sql, "WHERE") || len(query.Statement.Vars) != 0 {
		t.Fatalf("super administrator must see every channel: %s %#v", sql, query.Statement.Vars)
	}
}

func TestApplyProviderVisibilityForOrgAdminUsesOrganizationAndPublicBindings(t *testing.T) {
	database, closeDatabase := providerVisibilityTestDatabase(t)
	defer closeDatabase()

	query := applyProviderVisibility(database.Model(&ProviderRecord{}), domain.ProviderActor{
		Role:           "org_admin",
		UserID:         11,
		OrganizationID: 7,
	}).Find(&[]ProviderRecord{})
	sql := query.Statement.SQL.String()
	for _, fragment := range []string{
		"organization_provider_bindings opb",
		"opb.account_id = accounts.id",
		"opb.organization_id = ?",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("visibility SQL = %q, want fragment %q", sql, fragment)
		}
	}
	if len(query.Statement.Vars) != 1 || query.Statement.Vars[0] != uint64(7) {
		t.Fatalf("visibility vars = %#v, want [7]", query.Statement.Vars)
	}
}

func providerVisibilityTestDatabase(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	sqlDatabase, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{
		DisableAutomaticPing: true,
		DryRun:               true,
	})
	if err != nil {
		sqlDatabase.Close()
		t.Fatal(err)
	}
	return database, func() { _ = sqlDatabase.Close() }
}
