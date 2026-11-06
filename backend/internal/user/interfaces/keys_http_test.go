package interfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/user/application"
	"bosapi/backend/internal/user/domain"
)

type rateLimitKeyRepositoryStub struct {
	key     domain.APIKey
	updates map[string]any
	deleted struct{ id, userID, organizationID uint64 }
}

func (repository *rateLimitKeyRepositoryStub) FindOwned(context.Context, uint64, uint64, uint64) (domain.APIKey, error) {
	return repository.key, nil
}

func (repository *rateLimitKeyRepositoryStub) UpdateOwned(_ context.Context, _, _, _ uint64, updates map[string]any) error {
	repository.updates = updates
	if limit, ok := updates["rate_limit_5h"].(*int64); ok {
		repository.key.RateLimit5H = limit
	}
	if limit, ok := updates["rate_limit_1d"].(*int64); ok {
		repository.key.RateLimit1D = limit
	}
	if limit, ok := updates["rate_limit_7d"].(*int64); ok {
		repository.key.RateLimit7D = limit
	}
	return nil
}

func (repository *rateLimitKeyRepositoryStub) DeleteOwned(_ context.Context, id, userID, organizationID uint64) error {
	repository.deleted.id = id
	repository.deleted.userID = userID
	repository.deleted.organizationID = organizationID
	return nil
}

func TestUpdateKeyIPListsReachPersistenceAsLists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name string
		body string
		want []string
	}{
		{"single", `{"ip_allowlist":["192.0.2.0/24"],"ip_blocklist":["192.0.2.0/24"]}`, []string{"192.0.2.0/24"}},
		{"multiple with models", `{"allowed_models":["model-a"],"ip_allowlist":["192.0.2.1","2001:db8::/32"],"ip_blocklist":["192.0.2.1","2001:db8::/32"]}`, []string{"192.0.2.1", "2001:db8::/32"}},
		{"clear", `{"ip_allowlist":[],"ip_blocklist":[]}`, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &rateLimitKeyRepositoryStub{key: domain.APIKey{ID: 7, OrganizationID: 2}}
			handler := NewKeyHandler(nil, repository)
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				ctx.Set("identity", httpapi.Claims{UserID: 3, OrganizationID: 2})
			})
			handler.RegisterRoutes(router.Group("/api/v1"))
			request := httptest.NewRequest(http.MethodPut, "/api/v1/keys/7", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			for _, field := range []string{"ip_allowlist", "ip_blocklist"} {
				if !reflect.DeepEqual(repository.updates[field], test.want) {
					t.Fatalf("%s=%#v, want %#v", field, repository.updates[field], test.want)
				}
			}
		})
	}
}

func TestUpdateKeyRateLimitsValidateBeforePersistence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, field := range []string{"rate_limit_5h", "rate_limit_1d", "rate_limit_7d"} {
		t.Run(field, func(t *testing.T) {
			for _, test := range []struct {
				name   string
				limit  int64
				status int
			}{
				{"minimum", 1, http.StatusOK},
				{"maximum", math.MaxUint32, http.StatusOK},
				{"zero", 0, http.StatusBadRequest},
				{"negative", -1, http.StatusBadRequest},
				{"overflow", math.MaxUint32 + 1, http.StatusBadRequest},
			} {
				t.Run(test.name, func(t *testing.T) {
					testUpdateKeyRateLimit(t, field, test.limit, test.status)
				})
			}
		})
	}
}

func TestDeleteKeyUsesAuthenticatedOwnerScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := &rateLimitKeyRepositoryStub{}
	handler := NewKeyHandler(nil, repository)
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set("identity", httpapi.Claims{UserID: 3, OrganizationID: 2})
	})
	handler.RegisterRoutes(router.Group("/api/v1"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/keys/7", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if repository.deleted.id != 7 || repository.deleted.userID != 3 || repository.deleted.organizationID != 2 {
		t.Fatalf("delete scope = %+v, want key 7 owned by user 3 in organization 2", repository.deleted)
	}
}

func TestUpdateKeyNameUsesUnicodeCharacters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name   string
		status int
	}{
		{name: strings.Repeat("密", 100), status: http.StatusOK},
		{name: "  " + strings.Repeat("密", 100) + "  ", status: http.StatusOK},
		{name: strings.Repeat("密", 101), status: http.StatusBadRequest},
		{name: "   ", status: http.StatusBadRequest},
	} {
		repository := &rateLimitKeyRepositoryStub{}
		router := gin.New()
		router.Use(func(ctx *gin.Context) {
			ctx.Set("identity", httpapi.Claims{UserID: 3, OrganizationID: 2})
		})
		NewKeyHandler(nil, repository).RegisterRoutes(router.Group("/api/v1"))
		payload, err := json.Marshal(map[string]string{"name": test.name})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPut, "/api/v1/keys/7", strings.NewReader(string(payload)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("name length %d: status=%d body=%s", len(test.name), response.Code, response.Body.String())
		}
		if test.status == http.StatusBadRequest {
			if repository.updates != nil {
				t.Fatal("invalid name reached persistence")
			}
		} else if repository.updates["name"] != strings.TrimSpace(test.name) {
			t.Fatalf("stored name=%q", repository.updates["name"])
		}
	}
}

func TestUpdateKeyValidatesEveryQuota(t *testing.T) {
	for _, field := range []string{
		"quota_amount", "daily_quota_amount", "weekly_quota_amount", "monthly_quota_amount",
		"quota_tokens", "daily_quota_tokens", "weekly_quota_tokens", "monthly_quota_tokens",
	} {
		for _, value := range []int{-1, 0, 10} {
			var request keyUpdate
			if err := json.Unmarshal([]byte(fmt.Sprintf(`{"%s":%d}`, field, value)), &request); err != nil {
				t.Fatal(err)
			}
			updates, err := request.values()
			if value < 0 {
				if err == nil {
					t.Fatalf("negative %s accepted", field)
				}
				continue
			}
			if _, present := updates[field]; err != nil || !present {
				t.Fatalf("%s=%d: updates=%v err=%v", field, value, updates, err)
			}
		}
	}
}

func testUpdateKeyRateLimit(t *testing.T, field string, limit int64, status int) {
	t.Helper()
	repository := &rateLimitKeyRepositoryStub{}
	handler := NewKeyHandler(nil, repository)
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set("identity", httpapi.Claims{UserID: 3, OrganizationID: 2})
	})
	handler.RegisterRoutes(router.Group("/api/v1"))
	request := httptest.NewRequest(http.MethodPut, "/api/v1/keys/7", strings.NewReader(fmt.Sprintf(`{"%s":%d}`, field, limit)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
	if status != http.StatusOK {
		if repository.updates != nil {
			t.Fatal("invalid limit reached persistence")
		}
		return
	}
	var payload struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if value := string(payload.Data[field]); value != fmt.Sprint(limit) {
		t.Fatalf("returned limit = %s, want %d", value, limit)
	}
}

type createNameKeyRepository struct {
	application.KeyRepository
	name string
}

func (repository *createNameKeyRepository) Create(_ context.Context, key *domain.APIKey) error {
	repository.name = key.Name
	key.ID = 7
	return nil
}

func TestCreateKeyTrimsNameBeforeLengthValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name   string
		status int
	}{
		{name: "  " + strings.Repeat("密", 100) + "  ", status: http.StatusCreated},
		{name: strings.Repeat("密", 101), status: http.StatusBadRequest},
		{name: "   ", status: http.StatusBadRequest},
	} {
		repository := &createNameKeyRepository{}
		router := gin.New()
		router.Use(func(ctx *gin.Context) { ctx.Set("identity", httpapi.Claims{UserID: 3, OrganizationID: 2}) })
		NewKeyHandler(application.NewKeyService(repository), nil).RegisterRoutes(router.Group("/api/v1"))
		payload, err := json.Marshal(map[string]string{"name": test.name})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/keys", strings.NewReader(string(payload)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("name length %d: status=%d body=%s", len(test.name), response.Code, response.Body.String())
		}
		if test.status == http.StatusCreated && repository.name != strings.TrimSpace(test.name) {
			t.Fatalf("stored name=%q", repository.name)
		}
		if test.status == http.StatusBadRequest && repository.name != "" {
			t.Fatal("invalid name reached persistence")
		}
	}
}
