package interfaces

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type scopedProviderRepository struct {
	actor   domain.ProviderActor
	created domain.Provider
}

func (repository *scopedProviderRepository) ListProviders(_ context.Context, filter domain.ProviderFilter, _, _ int) ([]domain.Provider, int64, error) {
	repository.actor = filter.Actor
	return []domain.Provider{}, 0, nil
}
func (*scopedProviderRepository) FindProvider(context.Context, uint64) (domain.Provider, error) {
	return domain.Provider{}, nil
}
func (repository *scopedProviderRepository) CreateProvider(_ context.Context, provider domain.Provider, _ string) (domain.Provider, error) {
	repository.created = provider
	return provider, nil
}
func (*scopedProviderRepository) UpdateProvider(context.Context, uint64, map[string]any, string) (domain.Provider, error) {
	return domain.Provider{}, nil
}
func (*scopedProviderRepository) DisableProvider(context.Context, uint64) error   { return nil }
func (*scopedProviderRepository) DeleteProvider(context.Context, uint64) error    { return nil }
func (*scopedProviderRepository) SetStatus(context.Context, uint64, string) error { return nil }
func (*scopedProviderRepository) ProviderCredential(context.Context, uint64) (domain.Provider, string, error) {
	return domain.Provider{}, "", nil
}
func (*scopedProviderRepository) AuthorizeProvider(_ context.Context, id uint64, _ domain.ProviderActor) error {
	if id == 99 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func TestProviderHandlerScopesListsAndRestrictsCreation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{"super_admin", "org_admin"} {
		t.Run(role, func(t *testing.T) {
			repository := &scopedProviderRepository{}
			handler := NewProviderHandler(application.NewProviderService(repository, func(value string) (string, error) { return value, nil }, nil, nil))
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				ctx.Set("identity", httpapi.Claims{UserID: 7, OrganizationID: 11, Role: role})
				ctx.Next()
			})
			handler.RegisterRoutes(router.Group("/admin"))

			listResponse := httptest.NewRecorder()
			router.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/admin/providers?page=1&page_size=20", nil))
			if listResponse.Code != http.StatusOK || repository.actor.Role != role || repository.actor.UserID != 7 || repository.actor.OrganizationID != 11 {
				t.Fatalf("list status=%d actor=%+v", listResponse.Code, repository.actor)
			}

			body := `{"name":"channel","platform":"openai","account_type":"apikey","api_format":"openai_chat","base_url":"https://api.openai.com/v1"}`
			createResponse := httptest.NewRecorder()
			router.ServeHTTP(createResponse, httptest.NewRequest(http.MethodPost, "/admin/providers", strings.NewReader(body)))
			if role == "org_admin" {
				if createResponse.Code != http.StatusForbidden || repository.created.ID != 0 {
					t.Fatalf("organization administrator created a provider: %d", createResponse.Code)
				}
			} else if createResponse.Code != http.StatusCreated || repository.created.CreatedByUserID == nil || *repository.created.CreatedByUserID != 7 {
				t.Fatalf("create status=%d provider=%+v", createResponse.Code, repository.created)
			}
		})
	}
}

func TestProviderHandlerHidesUnauthorizedProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := &scopedProviderRepository{}
	handler := NewProviderHandler(application.NewProviderService(repository, nil, nil, nil))
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set("identity", httpapi.Claims{UserID: 7, OrganizationID: 11, Role: "super_admin"})
		ctx.Next()
	})
	handler.RegisterRoutes(router.Group("/admin"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/providers/99", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
