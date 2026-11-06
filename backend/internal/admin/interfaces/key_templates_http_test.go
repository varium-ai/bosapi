package interfaces

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
)

type privateTemplateRepository struct {
	records []domain.KeyTemplate
	failure error
}

func (repository *privateTemplateRepository) List(_ context.Context, creator uint64, _, _ int) ([]domain.KeyTemplate, int64, error) {
	if repository.failure != nil {
		return nil, 0, repository.failure
	}
	items := []domain.KeyTemplate{}
	for _, item := range repository.records {
		if item.CreatedBy == creator {
			items = append(items, item)
		}
	}
	return items, int64(len(items)), nil
}
func (repository *privateTemplateRepository) Save(_ context.Context, item *domain.KeyTemplate) error {
	if repository.failure != nil {
		return repository.failure
	}
	if item.ID != 0 {
		for index, existing := range repository.records {
			if existing.ID == item.ID && existing.CreatedBy == item.CreatedBy {
				repository.records[index] = *item
				return nil
			}
		}
		return gorm.ErrRecordNotFound
	}
	item.ID = uint64(len(repository.records) + 1)
	repository.records = append(repository.records, *item)
	return nil
}
func (repository *privateTemplateRepository) Delete(_ context.Context, creator, id uint64) error {
	if repository.failure != nil {
		return repository.failure
	}
	for index, item := range repository.records {
		if item.ID == id && item.CreatedBy == creator {
			repository.records = append(repository.records[:index], repository.records[index+1:]...)
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func TestPrivateTemplatesCannotBeReadOrModifiedByOtherAdministrators(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{"super_admin", "org_admin"} {
		t.Run(role, func(t *testing.T) {
			repository := &privateTemplateRepository{records: []domain.KeyTemplate{{ID: 1, CreatedBy: 20, Name: "other-private", Settings: domain.KeyTemplateSettings{OrganizationID: 3, UserID: 2}}}}
			router := gin.New()
			router.Use(func(ctx *gin.Context) { ctx.Set("identity", httpapi.Claims{UserID: 19, OrganizationID: 3, Role: role}) })
			NewKeyTemplateHandler(application.NewKeyTemplateService(repository)).RegisterRoutes(router.Group("/admin"))
			body := `{"created_by":20,"name":"mine","settings":{"organization_id":3,"user_id":2,"api_key":"must-not-be-stored"}}`
			for _, test := range []struct {
				method, path, body string
				status             int
			}{
				{"GET", "/admin/key-templates?created_by=20", "", 200},
				{"PUT", "/admin/key-templates/1", body, 404}, {"DELETE", "/admin/key-templates/1", "", 404},
				{"POST", "/admin/key-templates", body, 201}, {"GET", "/admin/key-templates", "", 200},
				{"PUT", "/admin/key-templates/2", body, 200}, {"DELETE", "/admin/key-templates/2", "", 200},
			} {
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
				if response.Code != test.status || strings.Contains(response.Body.String(), "other-private") || strings.Contains(response.Body.String(), "must-not-be-stored") {
					t.Fatalf("privacy failure: %s %s -> %d %s", test.method, test.path, response.Code, response.Body.String())
				}
				if test.method == "POST" && repository.records[1].CreatedBy != 19 {
					t.Fatal("untrusted creator accepted")
				}
			}
		})
	}
}

func TestPrivateTemplateIdentityAndMalformedRequestsAreRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		role, method, path, body string
		status                   int
	}{
		{"", "GET", "/admin/key-templates", "", 401}, {"user", "GET", "/admin/key-templates", "", 403},
		{"org_admin", "POST", "/admin/key-templates", `{"name":"cross","settings":{"organization_id":4,"user_id":2}}`, 400},
		{"super_admin", "PUT", "/admin/key-templates/invalid", `{}`, 400},
		{"super_admin", "POST", "/admin/key-templates", `{"name":"broken","settings":{"quota_tokens":-1}}`, 400},
		{"super_admin", "POST", "/admin/key-templates", `invalid`, 400},
		{"super_admin", "GET", "/admin/key-templates?page_size=1000", "", 400},
	} {
		t.Run(test.role+test.path+test.body, func(t *testing.T) {
			repository := &privateTemplateRepository{}
			router := gin.New()
			if test.role != "" {
				router.Use(func(ctx *gin.Context) {
					ctx.Set("identity", httpapi.Claims{UserID: 19, OrganizationID: 3, Role: test.role})
				})
			}
			NewKeyTemplateHandler(application.NewKeyTemplateService(repository)).RegisterRoutes(router.Group("/admin"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
			if response.Code != test.status || len(repository.records) != 0 {
				t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPrivateTemplateFailuresDoNotExposeStorageDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := &privateTemplateRepository{failure: errors.New("private-storage-details")}
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set("identity", httpapi.Claims{UserID: 19, OrganizationID: 3, Role: "super_admin"})
	})
	NewKeyTemplateHandler(application.NewKeyTemplateService(repository)).RegisterRoutes(router.Group("/admin"))
	for _, test := range []struct{ method, path, body string }{{"GET", "/admin/key-templates", ""}, {"DELETE", "/admin/key-templates/1", ""}, {"POST", "/admin/key-templates", `{"name":"valid","settings":{"organization_id":3,"user_id":2}}`}} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
		if response.Code != 500 || strings.Contains(response.Body.String(), "private-storage-details") {
			t.Fatalf("unexpected error response: %d %s", response.Code, response.Body.String())
		}
	}
}
