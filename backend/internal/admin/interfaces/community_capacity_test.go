package interfaces

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/edition"
	"bosapi/backend/internal/platform/httpapi"
	userdomain "bosapi/backend/internal/user/domain"
	"github.com/gin-gonic/gin"
)

type capacityKeyRepository struct {
	domain.AdminKeyRepository
	attempted int
}

func (repository *capacityKeyRepository) FindAdminKey(context.Context, uint64, uint64) (userdomain.APIKey, error) {
	return userdomain.APIKey{Name: "source", OrganizationID: 3, Status: "active"}, nil
}

func (repository *capacityKeyRepository) CreateAdminKey(context.Context, *userdomain.APIKey) error {
	repository.attempted++
	return &edition.LimitError{Resource: "api_keys", Limit: edition.MaxAPIKeys}
}

func (repository *capacityKeyRepository) CreateAdminKeys(_ context.Context, keys []*userdomain.APIKey) error {
	repository.attempted += len(keys)
	return &edition.LimitError{Resource: "api_keys", Limit: edition.MaxAPIKeys}
}

func TestAdminKeyCreationPathsReturnCapacityConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		path, body string
		incoming   int
	}{
		{"/admin/keys", `{"name":"new key","organization_id":3}`, 1},
		{"/admin/keys/batch-create", `{"names":["first","second"],"organization_id":3}`, 2},
		{"/admin/keys/import", `{"keys":[{"name":"first","api_key":"first-capacity-fixture"},{"name":"second","api_key":"second-capacity-fixture"}],"organization_id":3}`, 2},
		{"/admin/keys/7/duplicate", `{}`, 1},
	} {
		t.Run(test.path, func(t *testing.T) {
			repository := &capacityKeyRepository{}
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				ctx.Set("identity", httpapi.Claims{UserID: 19, Role: "super_admin", OrganizationID: 3})
			})
			NewAdminKeyHandler(application.NewAdminKeyService(repository)).RegisterRoutes(router.Group("/admin"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body)))
			assertCapacityConflict(t, response, "10")
			if repository.attempted != test.incoming {
				t.Fatalf("attempted=%d, want %d", repository.attempted, test.incoming)
			}
			if strings.Contains(response.Body.String(), "capacity-fixture") || strings.Contains(response.Body.String(), `"key":`) {
				t.Fatal("a failed creation must not return a credential")
			}
		})
	}
}

type capacityOrganizationRepository struct {
	domain.OrganizationRepository
	attempted bool
}

func (repository *capacityOrganizationRepository) Create(context.Context, *domain.Organization) error {
	repository.attempted = true
	return &edition.LimitError{Resource: "organizations", Limit: edition.MaxOrganizations}
}

func TestOrganizationCreationReturnsCapacityConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := &capacityOrganizationRepository{}
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set("identity", httpapi.Claims{UserID: 19, Role: "super_admin"})
	})
	NewOrganizationHandler(application.NewOrganizationService(repository)).RegisterRoutes(router.Group("/admin"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/organizations", strings.NewReader(`{"name":"third"}`)))
	assertCapacityConflict(t, response, "2")
	if !repository.attempted {
		t.Fatal("creation did not reach the repository")
	}
}

func assertCapacityConflict(t *testing.T, response *httptest.ResponseRecorder, limit string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusConflict || body.Error.Code != "conflict" || !strings.Contains(body.Error.Message, "at most "+limit) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
