package interfaces

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

type permissionProviderRepository struct {
	scopedProviderRepository
	provider domain.Provider
	deleted  bool
}

func (r *permissionProviderRepository) FindProvider(context.Context, uint64) (domain.Provider, error) {
	return r.provider, nil
}
func (r *permissionProviderRepository) DeleteProvider(context.Context, uint64) error {
	r.deleted = true
	return nil
}

func TestPublicChannelsAreReadOnlyForOrganizationAdministrators(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name, role, method string
		org                uint64
		status             int
	}{
		{"super reads public", "super_admin", "GET", 0, 200},
		{"super edits public", "super_admin", "DELETE", 0, 200},
		{"organization reads assigned public", "org_admin", "GET", 11, 200},
		{"organization cannot edit public", "org_admin", "DELETE", 11, 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &permissionProviderRepository{provider: domain.Provider{ID: 7}}
			handler := NewProviderHandler(application.NewProviderService(repository, nil, nil, nil))
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set("identity", httpapi.Claims{UserID: 3, Role: test.role, OrganizationID: test.org})
				c.Next()
			})
			handler.RegisterRoutes(router.Group("/admin"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(test.method, "/admin/providers/7", nil))
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if repository.deleted != (test.method == http.MethodDelete && test.status == 200) {
				t.Fatal("mutation authorization bypassed")
			}
		})
	}
}
