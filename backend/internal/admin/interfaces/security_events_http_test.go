package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/securityevidence"
)

type eventFilterRepository struct {
	filter                       domain.SecurityEventFilter
	called                       bool
	detailID, detailOrganization uint64
	detail                       domain.SecurityEventDetail
	detailErr                    error
	readScope                    domain.SecurityEventReadScope
	readErr                      error
}

func TestSecurityEventDetailRequiresAdminAndScopesOrganization(t *testing.T) {
	for _, test := range []struct {
		name, role, path    string
		organization, scope uint64
		status              int
		found               bool
	}{
		{"organization own event", "org_admin", "/security-events/42?organization_id=10", 9, 9, 200, true},
		{"foreign event", "org_admin", "/security-events/42", 9, 9, 404, false},
		{"platform admin", "super_admin", "/security-events/42", 1, 0, 200, true},
		{"key viewer", "key_viewer", "/security-events/42", 9, 0, 403, true},
		{"user", "user", "/security-events/42", 9, 0, 403, true},
		{"missing organization", "org_admin", "/security-events/42", 0, 0, 403, true},
		{"invalid id", "org_admin", "/security-events/0", 9, 0, 400, true},
		{"unauthenticated", "", "/security-events/42", 0, 0, 401, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			repository := &eventFilterRepository{detail: domain.SecurityEventDetail{SecurityEvent: domain.SecurityEvent{ID: 42}, EvidenceStatus: "available", Evidence: []securityevidence.View{{Evidence: securityevidence.Evidence{Source: "prompt", ReasonCode: "credential_format", Snippet: "[Credential hidden]"}}}}}
			if !test.found {
				repository.detailErr = domain.ErrSecurityEventNotFound
			}
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				if test.role != "" {
					ctx.Set("identity", httpapi.Claims{UserID: 1, OrganizationID: test.organization, Role: test.role})
				}
				ctx.Next()
			})
			NewSecurityEventHandler(application.NewSecurityEventService(repository)).RegisterRoutes(router.Group(""))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("GET", test.path, nil))
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.status == 200 || test.status == 404 {
				if !repository.called || repository.detailID != 42 || repository.detailOrganization != test.scope {
					t.Fatalf("detail scope=%+v", repository)
				}
			} else if repository.called {
				t.Fatal("unauthorized request reached evidence repository")
			}
		})
	}
}

func (repository *eventFilterRepository) GetSecurityEventDetail(_ context.Context, id, organizationID uint64) (domain.SecurityEventDetail, error) {
	repository.called, repository.detailID, repository.detailOrganization = true, id, organizationID
	return repository.detail, repository.detailErr
}

func (repository *eventFilterRepository) ListSecurityEvents(_ context.Context, filter domain.SecurityEventFilter, _, _ int) ([]domain.SecurityEvent, int64, error) {
	repository.called = true
	repository.filter = filter
	return []domain.SecurityEvent{}, 0, nil
}

func TestSecurityEventsCombinedFiltersAndTenantScope(t *testing.T) {
	for _, test := range []struct {
		name, query string
		status      int
	}{
		{"combined filters", "tool_name=exec_command&client_ip=192.0.2.42&key_id=7&rule_id=baseline&action=block&event_type=tool_call&severity=high", 200},
		{"invalid event type", "event_type=unknown", 400},
		{"invalid severity", "severity=unknown", 400},
		{"invalid minimum severity", "min_severity=unknown", 400},
		{"invalid ip", "tool_name=exec_command&client_ip=invalid", 400},
		{"foreign organization", "organization_id=10&tool_name=exec_command&client_ip=192.0.2.42", 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			repository := &eventFilterRepository{}
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				ctx.Set("identity", httpapi.Claims{UserID: 1, OrganizationID: 9, Role: "org_admin"})
				ctx.Next()
			})
			NewSecurityEventHandler(application.NewSecurityEventService(repository)).RegisterRoutes(router.Group(""))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("GET", "/security-events?"+test.query, nil))
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.status != 200 {
				if repository.called {
					t.Fatal("invalid or foreign filter reached repository")
				}
				return
			}
			want := domain.SecurityEventFilter{OrganizationID: 9, APIKeyID: 7, RuleID: "baseline", Action: "block", ToolName: "exec_command", ClientIP: "192.0.2.42", EventType: "tool_call", Severity: "high"}
			if !repository.called || repository.filter != want {
				t.Fatalf("filter=%+v want=%+v", repository.filter, want)
			}
		})
	}
}

func (repository *eventFilterRepository) CountUnreadSecurityEvents(_ context.Context, scope domain.SecurityEventReadScope) (int64, error) {
	repository.called, repository.readScope = true, scope
	return 3, repository.readErr
}

func (repository *eventFilterRepository) MarkSecurityEventsRead(_ context.Context, scope domain.SecurityEventReadScope) error {
	repository.called, repository.readScope = true, scope
	return repository.readErr
}

func TestSecurityEventEndpointsRequireScopedAdministrators(t *testing.T) {
	for _, route := range []struct {
		method, path string
	}{
		{http.MethodGet, "/security-events"},
		{http.MethodGet, "/security-events/42"},
		{http.MethodGet, "/security-events/unread"},
		{http.MethodPost, "/security-events/read"},
	} {
		for _, test := range []struct {
			name   string
			claims httpapi.Claims
			status int
		}{
			{name: "unauthenticated", status: http.StatusUnauthorized},
			{name: "viewer", claims: httpapi.Claims{UserID: 1, Role: "key_viewer", OrganizationID: 9}, status: http.StatusForbidden},
			{name: "unknown role", claims: httpapi.Claims{UserID: 1, Role: "admin", OrganizationID: 9}, status: http.StatusForbidden},
			{name: "missing tenant", claims: httpapi.Claims{UserID: 1, Role: "org_admin"}, status: http.StatusForbidden},
			{name: "missing user", claims: httpapi.Claims{Role: "super_admin"}, status: http.StatusForbidden},
		} {
			t.Run(route.method+route.path+test.name, func(t *testing.T) {
				repository := &eventFilterRepository{}
				router := securityEventTestRouter(repository, test.claims)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
				if response.Code != test.status || repository.called {
					t.Fatalf("status=%d body=%s repository called=%v", response.Code, response.Body.String(), repository.called)
				}
			})
		}
	}
}

func TestSecurityEventNotificationEndpointsUseAuthenticatedScope(t *testing.T) {
	for _, route := range []struct {
		method, path string
		want         domain.SecurityEventReadState
	}{
		{http.MethodGet, "/security-events/unread?organization_id=999", domain.SecurityEventReadState{UnreadCount: 3}},
		{http.MethodPost, "/security-events/read?organization_id=999&last_read_event_id=999999", domain.SecurityEventReadState{}},
	} {
		for _, role := range []string{"org_admin", "super_admin"} {
			for _, fail := range []bool{false, true} {
				t.Run(route.method+role+map[bool]string{false: " success", true: " failure"}[fail], func(t *testing.T) {
					repository := &eventFilterRepository{}
					if fail {
						repository.readErr = errors.New("database unavailable")
					}
					router := securityEventTestRouter(repository, httpapi.Claims{UserID: 7, Role: role, OrganizationID: 9})
					response := httptest.NewRecorder()
					router.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
					wantScope := domain.SecurityEventReadScope{UserID: 7, OrganizationID: 9}
					if role == "super_admin" {
						wantScope.OrganizationID = 0
					}
					if !repository.called || repository.readScope != wantScope {
						t.Fatalf("scope=%+v want=%+v called=%v", repository.readScope, wantScope, repository.called)
					}
					if fail {
						if response.Code != http.StatusInternalServerError {
							t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
						}
						return
					}
					var envelope struct {
						Data domain.SecurityEventReadState `json:"data"`
					}
					if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}
					if response.Code != http.StatusOK || envelope.Data != route.want {
						t.Fatalf("status=%d data=%+v want=%+v", response.Code, envelope.Data, route.want)
					}
				})
			}
		}
	}
}

func TestSecurityEventListAcceptsMinimumSeverity(t *testing.T) {
	repository := &eventFilterRepository{}
	router := securityEventTestRouter(repository, httpapi.Claims{UserID: 7, Role: "org_admin", OrganizationID: 9})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/security-events?min_severity=warning&severity=high", nil))
	want := domain.SecurityEventFilter{OrganizationID: 9, Severity: "high", MinSeverity: "warning"}
	if response.Code != http.StatusOK || !repository.called || repository.filter != want {
		t.Fatalf("status=%d filter=%+v want=%+v", response.Code, repository.filter, want)
	}
}

func securityEventTestRouter(repository *eventFilterRepository, claims httpapi.Claims) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		if claims.Role != "" {
			ctx.Set("identity", claims)
		}
		ctx.Next()
	})
	NewSecurityEventHandler(application.NewSecurityEventService(repository)).RegisterRoutes(router.Group(""))
	return router
}
