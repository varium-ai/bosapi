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

func TestPublicModelRepository_ListPublicModels(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_ string, actual string) error {
		for _, required := range []string{
			"a.id = pm.account_id",
			"b.account_id = a.id",
			"o.id = b.organization_id",
			"pm.status = ?",
			"a.status = ?",
			"b.status = ?",
			"o.status = ?",
			"a.schedulable = ?",
			"JSON_CONTAINS(b.model_allowlist, JSON_QUOTE(pm.model_name))",
			"JSON_LENGTH(b.model_allowlist) = 0",
		} {
			if !strings.Contains(actual, required) {
				return errors.New("missing predicate: " + required)
			}
		}
		if strings.Contains(actual, "o.allowed_provider_ids") || strings.Contains(actual, "o.allowed_models") {
			return errors.New("legacy organization policy must not override active bindings")
		}
		return nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()

	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewPublicModelRepository(database)

	mock.ExpectQuery("SELECT").
		WithArgs("active", "active", "active", "active", true, 1000).
		WillReturnRows(sqlmock.NewRows([]string{"model_name", "platform", "capabilities_json"}).
			AddRow("deepseek-ai/DeepSeek-V4.1-Flash", "openai", `{"stream":true,"tools":true}`).
			AddRow("Qwen3.8-27B", "openai", `{"stream":true}`))

	items, err := repo.ListPublicModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ID != "Qwen3.8-27B" || items[1].ID != "deepseek-ai/DeepSeek-V4.1-Flash" {
		t.Fatalf("expected sorted items, got: %+v", items)
	}
	if items[1].Capabilities["tools"] != true {
		t.Fatalf("expected capabilities parsed, got: %+v", items[1].Capabilities)
	}
}
