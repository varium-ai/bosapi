package interfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	authinterfaces "bosapi/backend/internal/auth/interfaces"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type bindingRepositoryStub struct {
	id  uint64
	err error
}

func (repo *bindingRepositoryStub) ListProviderBindings(_ context.Context, id uint64) ([]domain.ProviderBinding, error) {
	repo.id = id
	return []domain.ProviderBinding{{AccountID: 1, Priority: 2, Status: "active", ModelAllowlist: []string{"glm"}}, {AccountID: 2, Priority: 1, Status: "disabled", ModelAllowlist: []string{}}}, repo.err
}
func (*bindingRepositoryStub) ReplaceProviderBindings(context.Context, uint64, []domain.ProviderBinding) error {
	return nil
}

func TestProviderBindingReadsRequirePlatformAdminAndReturnSavedValues(t *testing.T) {
	for _, test := range []struct {
		name, role, id string
		err            error
		status         int
		called         bool
	}{
		{"saved", "super_admin", "1", nil, 200, true},
		{"invalid_id", "super_admin", "bad", nil, 400, false},
		{"missing", "super_admin", "1", gorm.ErrRecordNotFound, 404, true},
		{"failed", "super_admin", "1", errors.New("db unavailable"), 500, true},
		{"other_org_admin", "org_admin", "1", nil, 403, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			repo := &bindingRepositoryStub{err: test.err}
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set("identity", httpapi.Claims{Role: test.role, OrganizationID: 7}); c.Next() })
			NewProviderBindingHandler(application.NewProviderBindingService(repo)).RegisterRoutes(router.Group("", authinterfaces.RequireRoles("super_admin")))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/organizations/"+test.id+"/provider-bindings", nil))
			if response.Code != test.status || (repo.id != 0) != test.called {
				t.Fatalf("status=%d org=%d body=%s", response.Code, repo.id, response.Body.String())
			}
			if test.status == 200 {
				var body struct {
					Data struct {
						Items []domain.ProviderBinding `json:"items"`
					} `json:"data"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if len(body.Data.Items) != 2 || body.Data.Items[0].Priority != 2 || body.Data.Items[1].Status != "disabled" {
					t.Fatalf("body=%s", response.Body.String())
				}
			}
		})
	}
}

type mockBindingRepo struct {
	bindings map[uint64][]domain.ProviderBinding
}

func (m *mockBindingRepo) ListProviderBindings(ctx context.Context, orgID uint64) ([]domain.ProviderBinding, error) {
	if items, ok := m.bindings[orgID]; ok {
		return items, nil
	}
	return []domain.ProviderBinding{}, nil
}

func (m *mockBindingRepo) ReplaceProviderBindings(ctx context.Context, orgID uint64, bindings []domain.ProviderBinding) error {
	m.bindings[orgID] = bindings
	return nil
}

func TestProviderBindingHandlerListAndReplace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &mockBindingRepo{
		bindings: map[uint64][]domain.ProviderBinding{
			1: {
				{
					AccountID:      10,
					Priority:       5,
					ModelAllowlist: []string{"gpt-4o"},
					Status:         "active",
				},
			},
		},
	}
	service := application.NewProviderBindingService(repo)
	handler := NewProviderBindingHandler(service)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/admin"))

	// Test GET list existing bindings
	req := httptest.NewRequest(http.MethodGet, "/admin/organizations/1/provider-bindings", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Code int `json:"code"`
		Data struct {
			Bindings []domain.ProviderBinding `json:"bindings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if len(res.Data.Bindings) != 1 || res.Data.Bindings[0].AccountID != 10 {
		t.Fatalf("unexpected bindings: %+v", res.Data.Bindings)
	}

	// Test GET list empty bindings
	reqEmpty := httptest.NewRequest(http.MethodGet, "/admin/organizations/2/provider-bindings", nil)
	recEmpty := httptest.NewRecorder()
	router.ServeHTTP(recEmpty, reqEmpty)
	if recEmpty.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recEmpty.Code)
	}

	// Test PUT replace
	body, _ := json.Marshal(map[string]any{
		"bindings": []domain.ProviderBinding{
			{
				AccountID:      20,
				Priority:       1,
				ModelAllowlist: []string{"claude-3-5-sonnet"},
				Status:         "active",
			},
		},
	})
	reqPut := httptest.NewRequest(http.MethodPut, "/admin/organizations/2/provider-bindings", bytes.NewReader(body))
	reqPut.Header.Set("Content-Type", "application/json")
	recPut := httptest.NewRecorder()
	router.ServeHTTP(recPut, reqPut)
	if recPut.Code != http.StatusOK {
		t.Fatalf("expected status 200 on PUT, got %d: %s", recPut.Code, recPut.Body.String())
	}

	// Verify PUT updated repo
	if len(repo.bindings[2]) != 1 || repo.bindings[2][0].AccountID != 20 {
		t.Fatalf("repo not updated properly: %+v", repo.bindings[2])
	}
}
