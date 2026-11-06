package interfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
)

type mockHTTPAdminUserRepo struct {
	users             map[uint64]domain.AdminUser
	lastListFilter    domain.AdminUserFilter
	organizationError error
}

func (m *mockHTTPAdminUserRepo) ListAdminUsers(ctx context.Context, filter domain.AdminUserFilter, page, size int) ([]domain.AdminUser, int64, error) {
	m.lastListFilter = filter
	var res []domain.AdminUser
	for _, u := range m.users {
		if filter.OrganizationID > 0 && u.OrganizationID != filter.OrganizationID {
			continue
		}
		res = append(res, u)
	}
	return res, int64(len(res)), nil
}

func (m *mockHTTPAdminUserRepo) FindByID(ctx context.Context, id uint64) (domain.AdminUser, error) {
	u, ok := m.users[id]
	if !ok {
		return domain.AdminUser{}, errors.New("user not found")
	}
	return u, nil
}

func (m *mockHTTPAdminUserRepo) UpdatePassword(ctx context.Context, id uint64, hash string) error {
	return nil
}

func (m *mockHTTPAdminUserRepo) ResetMFA(ctx context.Context, id uint64) error {
	return nil
}

func (m *mockHTTPAdminUserRepo) UpdateRole(ctx context.Context, id uint64, role string, organizationIDs ...uint64) error {
	u, ok := m.users[id]
	if !ok {
		return errors.New("user not found")
	}
	u.Role = role
	m.users[id] = u
	return nil
}

func (m *mockHTTPAdminUserRepo) UpdateStatus(ctx context.Context, id uint64, status string) error {
	u, ok := m.users[id]
	if !ok {
		return errors.New("user not found")
	}
	u.Status = status
	m.users[id] = u
	return nil
}

func (m *mockHTTPAdminUserRepo) ListLoginRecords(ctx context.Context, userID uint64, page, size int) ([]domain.AdminLoginRecord, int64, error) {
	return nil, 0, nil
}

func (m *mockHTTPAdminUserRepo) UpdateOrganization(ctx context.Context, id, organizationID uint64) error {
	if m.organizationError != nil {
		return m.organizationError
	}
	user, ok := m.users[id]
	if !ok {
		return domain.ErrAdminUserNotFound
	}
	user.OrganizationID = organizationID
	m.users[id] = user
	return nil
}

func TestAdminUserUpdateOrganizationHTTP(t *testing.T) {
	for _, test := range []struct {
		name, method, role, userID, body string
		repositoryError                  error
		wantStatus                       int
	}{
		{"put", "PUT", "super_admin", "1", `{"organization_id":20}`, nil, 200},
		{"post", "POST", "super_admin", "1", `{"organization_id":20}`, nil, 200},
		{"organization admin denied", "PUT", "org_admin", "1", `{"organization_id":20}`, nil, 403},
		{"self denied", "PUT", "super_admin", "9", `{"organization_id":20}`, nil, 400},
		{"invalid user id", "PUT", "super_admin", "invalid", `{"organization_id":20}`, nil, 400},
		{"zero organization", "PUT", "super_admin", "1", `{"organization_id":0}`, nil, 400},
		{"negative organization", "PUT", "super_admin", "1", `{"organization_id":-1}`, nil, 400},
		{"fractional organization", "PUT", "super_admin", "1", `{"organization_id":1.5}`, nil, 400},
		{"missing organization", "PUT", "super_admin", "1", `{}`, nil, 400},
		{"unknown user", "PUT", "super_admin", "99", `{"organization_id":20}`, nil, 404},
		{"inactive organization", "PUT", "super_admin", "1", `{"organization_id":20}`, domain.ErrTargetOrganizationUnavailable, 400},
		{"database failure", "PUT", "super_admin", "1", `{"organization_id":20}`, errors.New("database failure"), 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &mockHTTPAdminUserRepo{users: map[uint64]domain.AdminUser{1: {ID: 1, OrganizationID: 10}}, organizationError: test.repositoryError}
			router := setupAdminUserTestRouter(repo, httpapi.Claims{UserID: 9, Role: test.role})
			req := httptest.NewRequest(test.method, "/admin-users/"+test.userID+"/organization", bytes.NewBufferString(test.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, test.wantStatus, rec.Body.String())
			}
			if rec.Code == 200 && repo.users[1].OrganizationID != 20 || rec.Code != 200 && repo.users[1].OrganizationID != 10 {
				t.Fatalf("unexpected user organization: %d", repo.users[1].OrganizationID)
			}
		})
	}
}

func setupAdminUserTestRouter(repo domain.AdminUserRepository, claims httpapi.Claims) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set("identity", claims)
		ctx.Next()
	})
	svc := application.NewAdminUserService(repo)
	handler := NewAdminUserHandler(svc)
	handler.RegisterRoutes(router.Group(""))
	return router
}

func TestAdminUserUpdateStatusHTTP(t *testing.T) {
	now := time.Now()
	makeRepo := func() *mockHTTPAdminUserRepo {
		return &mockHTTPAdminUserRepo{
			users: map[uint64]domain.AdminUser{
				1: {ID: 1, OrganizationID: 10, Email: "target@example.com", Role: "org_admin", Status: "active", CreatedAt: now, UpdatedAt: now},
				2: {ID: 2, OrganizationID: 10, Email: "disabled@example.com", Role: "org_admin", Status: "disabled", CreatedAt: now, UpdatedAt: now},
				3: {ID: 3, OrganizationID: 1, Email: "super@example.com", Role: "super_admin", Status: "active", CreatedAt: now, UpdatedAt: now},
			},
		}
	}

	t.Run("super_admin disables active user", func(t *testing.T) {
		repo := makeRepo()
		router := setupAdminUserTestRouter(repo, httpapi.Claims{UserID: 3, OrganizationID: 1, Role: "super_admin"})

		body, _ := json.Marshal(map[string]string{"status": "disabled"})
		req := httptest.NewRequest("POST", "/admin-users/1/status", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if repo.users[1].Status != "disabled" {
			t.Fatalf("expected status disabled, got %s", repo.users[1].Status)
		}
	})

	t.Run("super_admin enables disabled user", func(t *testing.T) {
		repo := makeRepo()
		router := setupAdminUserTestRouter(repo, httpapi.Claims{UserID: 3, OrganizationID: 1, Role: "super_admin"})

		body, _ := json.Marshal(map[string]string{"status": "active"})
		req := httptest.NewRequest("PUT", "/admin-users/2/status", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if repo.users[2].Status != "active" {
			t.Fatalf("expected status active, got %s", repo.users[2].Status)
		}
	})

	t.Run("org_admin forbidden from updating status", func(t *testing.T) {
		repo := makeRepo()
		router := setupAdminUserTestRouter(repo, httpapi.Claims{UserID: 1, OrganizationID: 10, Role: "org_admin"})

		body, _ := json.Marshal(map[string]string{"status": "disabled"})
		req := httptest.NewRequest("POST", "/admin-users/2/status", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("super_admin cannot disable own account", func(t *testing.T) {
		repo := makeRepo()
		router := setupAdminUserTestRouter(repo, httpapi.Claims{UserID: 3, OrganizationID: 1, Role: "super_admin"})

		body, _ := json.Marshal(map[string]string{"status": "disabled"})
		req := httptest.NewRequest("POST", "/admin-users/3/status", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("invalid status rejected", func(t *testing.T) {
		repo := makeRepo()
		router := setupAdminUserTestRouter(repo, httpapi.Claims{UserID: 3, OrganizationID: 1, Role: "super_admin"})

		body, _ := json.Marshal(map[string]string{"status": "deleted"})
		req := httptest.NewRequest("POST", "/admin-users/1/status", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("org_admin list is scoped to own organization", func(t *testing.T) {
		repo := makeRepo()
		router := setupAdminUserTestRouter(repo, httpapi.Claims{UserID: 1, OrganizationID: 10, Role: "org_admin"})

		req := httptest.NewRequest("GET", "/admin-users?organization_id=99", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if repo.lastListFilter.OrganizationID != 10 {
			t.Fatalf("expected forced organization_id=10, got %d", repo.lastListFilter.OrganizationID)
		}
	})
}
