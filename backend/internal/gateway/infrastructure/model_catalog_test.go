package infrastructure

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestModelCatalogAppliesTenantAndAvailabilityPolicies(t *testing.T) {
	database, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(expected string, actual string) error {
		if expected == "key_catalog" && (!strings.Contains(actual, "a.id IN (?)") || !strings.Contains(actual, "pm.model_name IN (?)")) {
			return errors.New("missing key restrictions")
		}
		if expected == "gemini_catalog" && !strings.Contains(actual, "a.api_format IN") {
			return errors.New("missing Gemini protocol restriction")
		}
		for _, required := range []string{
			"a.id = pm.account_id", "b.organization_id = o.id", "JOIN organizations AS o ON o.id = ?",
			"LEFT JOIN organization_provider_bindings AS b", "b.status = ?",
			"b.account_id IS NOT NULL",
			"pm.status = ? AND a.status = ? AND o.status = ?", "a.schedulable = ?",
			"JSON_CONTAINS(b.model_allowlist, JSON_QUOTE(pm.model_name))",
			"a.api_format = 'gemini'", "SUBSTRING(pm.model_name, 8)",
			"JSON_CONTAINS(b.model_allowlist, JSON_QUOTE(CONCAT('models/'",
		} {
			if !strings.Contains(actual, required) {
				return errors.New("missing availability predicate: " + required)
			}
		}
		if strings.Contains(actual, "configured_binding.status") {
			return errors.New("paused bindings must not restore unrestricted discovery")
		}
		return nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: database, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewModelCatalogRepository(db)
	mock.ExpectQuery("catalog").WithArgs(2, "active", 2, "active", "active", "active", true).
		WillReturnRows(sqlmock.NewRows([]string{"model_name", "platform"}).AddRow("zai-org/GLM-5.3-Flash", "openai"))
	models, err := repository.ListAvailableModels(context.Background(), 2)
	if err != nil || len(models) != 1 || models[0].ID != "zai-org/GLM-5.3-Flash" {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	mock.ExpectQuery("gemini_catalog").WithArgs(2, "active", 2, "active", "active", "active", true, "gemini").
		WillReturnRows(sqlmock.NewRows([]string{"model_name", "platform"}).AddRow("gemini-test", "gemini"))
	models, err = repository.ListAvailableModels(context.Background(), 2, "gemini")
	if err != nil || len(models) != 1 || models[0].ID != "gemini-test" {
		t.Fatalf("Gemini catalog=%+v err=%v", models, err)
	}
	mock.ExpectQuery("key_catalog").WithArgs(2, "active", 2, "active", "active", "active", true, uint64(7), "models/one", "one").WillReturnRows(sqlmock.NewRows([]string{"model_name", "platform"}).AddRow("models/one", "gemini"))
	models, err = repository.ListAvailableModelsForKey(context.Background(), 2, []uint64{7}, []string{"models/one"})
	if err != nil || len(models) != 1 {
		t.Fatalf("restricted key catalog=%+v err=%v", models, err)
	}
	// An organization without assigned bindings has no models.
	mock.ExpectQuery("catalog").WithArgs(1, "active", 1, "active", "active", "active", true).
		WillReturnRows(sqlmock.NewRows([]string{"model_name", "platform"}))
	models, err = repository.ListAvailableModels(context.Background(), 1)
	if err != nil || len(models) != 0 {
		t.Fatalf("unassigned organization must not receive global models: models=%+v err=%v", models, err)
	}
	mock.ExpectQuery("catalog").WithArgs(3, "active", 3, "active", "active", "active", true).
		WillReturnError(errors.New("catalog unavailable"))
	if _, err := repository.ListAvailableModels(context.Background(), 3); err == nil {
		t.Fatal("catalog errors must not fall back to global models")
	}
	models, err = repository.ListAvailableModels(context.Background(), 0)
	if err != nil || models == nil || len(models) != 0 {
		t.Fatalf("missing tenant must return an empty list: %+v %v", models, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
