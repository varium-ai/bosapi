package interfaces

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bosapi/backend/internal/platform/edition"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/user/application"
	"bosapi/backend/internal/user/domain"
	"github.com/gin-gonic/gin"
)

type capacityKeyRepository struct {
	application.KeyRepository
	attempted bool
}

func (repository *capacityKeyRepository) Create(context.Context, *domain.APIKey) error {
	repository.attempted = true
	return &edition.LimitError{Resource: "api_keys", Limit: edition.MaxAPIKeys}
}

func TestSelfServiceKeyCreationReturnsCapacityConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := &capacityKeyRepository{}
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set("identity", httpapi.Claims{UserID: 19, OrganizationID: 3})
	})
	NewKeyHandler(application.NewKeyService(repository), nil).RegisterRoutes(router.Group("/api/v1"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/keys", strings.NewReader(`{"name":"eleventh"}`)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"conflict"`) || !strings.Contains(response.Body.String(), "at most 10") || !repository.attempted {
		t.Fatalf("attempted=%v status=%d body=%s", repository.attempted, response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"key":`) {
		t.Fatal("a failed creation must not return a credential")
	}
}
