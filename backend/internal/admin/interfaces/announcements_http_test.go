package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
	userdomain "bosapi/backend/internal/user/domain"
)

type announcementHTTPService struct {
	failure        error
	mutated        bool
	filter         domain.AnnouncementFilter
	organizationID uint64
}

func (service *announcementHTTPService) List(_ context.Context, filter domain.AnnouncementFilter, _, _ int) ([]domain.Announcement, int64, error) {
	service.filter = filter
	return []domain.Announcement{{ID: 7, Title: "Release", Body: "# Update", CreatedAt: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)}}, 1, service.failure
}

func (service *announcementHTTPService) Create(_ context.Context, organizationID uint64, title, body string) (domain.Announcement, error) {
	service.mutated = true
	service.organizationID = organizationID
	return domain.Announcement{ID: 8, OrganizationID: organizationID, Title: title, Body: body}, service.failure
}

func (service *announcementHTTPService) Update(_ context.Context, item domain.Announcement) (domain.Announcement, error) {
	service.mutated = true
	service.organizationID = item.OrganizationID
	return item, service.failure
}

func (service *announcementHTTPService) Delete(_ context.Context, organizationID, _ uint64) error {
	service.mutated = true
	service.organizationID = organizationID
	return service.failure
}

func announcementTestRouter(role string, service *announcementHTTPService) *gin.Engine {
	router := gin.New()
	if role != "" {
		router.Use(func(ctx *gin.Context) {
			ctx.Set("identity", httpapi.Claims{UserID: 1, OrganizationID: 2, Role: role})
		})
	}
	NewAnnouncementHandler(service).RegisterAdminRoutes(router.Group("/admin"))
	return router
}

func TestAnnouncementPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{"", "super_admin", "org_admin", "user", "key_viewer"} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			t.Run(role+method, func(t *testing.T) {
				service := &announcementHTTPService{}
				router := announcementTestRouter(role, service)
				path := "/admin/announcements"
				if method == "DELETE" || method == "PUT" {
					path += "/7"
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(`{"title":"Release","body":"# Update"}`)))
				expected := 200
				if method == "POST" {
					expected = 201
				}
				if role == "" {
					expected = 401
				} else if method != "GET" && role != "super_admin" && role != "org_admin" {
					expected = 403
				}
				if response.Code != expected || service.mutated != (expected < 300 && method != "GET") {
					t.Fatalf("got %d: %s, expected %d", response.Code, response.Body.String(), expected)
				}
			})
		}
	}
}

func TestAnnouncementMalformedRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct{ name, method, path, body string }{
		{"invalid_json", "POST", "/admin/announcements", "invalid"},
		{"empty", "POST", "/admin/announcements", `{}`},
		{"blank_title", "POST", "/admin/announcements", `{"title":" ","body":"body"}`},
		{"blank_body", "POST", "/admin/announcements", `{"title":"Title","body":" \n"}`},
		{"long_title", "POST", "/admin/announcements", `{"title":"` + strings.Repeat("x", 201) + `","body":"body"}`},
		{"long_body", "POST", "/admin/announcements", `{"title":"Title","body":"` + strings.Repeat("x", 65537) + `"}`},
		{"oversized_request", "POST", "/admin/announcements", `{"title":"Title","body":"` + strings.Repeat("x", 512*1024) + `"}`},
		{"spoof_organization", "POST", "/admin/announcements", `{"title":"Title","body":"body","organization_id":99}`},
		{"unknown_field", "POST", "/admin/announcements", `{"title":"Title","body":"body","role":"super_admin"}`},
		{"trailing_json", "POST", "/admin/announcements", `{"title":"Title","body":"body"} {}`},
		{"wrong_type", "POST", "/admin/announcements", `{"title":123,"body":"body"}`},
		{"invalid_id", "DELETE", "/admin/announcements/invalid", ""},
		{"zero_id", "DELETE", "/admin/announcements/0", ""},
		{"negative_id", "DELETE", "/admin/announcements/-1", ""},
		{"injection_id", "DELETE", "/admin/announcements/1%20OR%201=1", ""},
		{"invalid_page", "GET", "/admin/announcements?page=0", ""},
		{"invalid_size", "GET", "/admin/announcements?page_size=101", ""},
	} {
		methods := []string{test.method}
		if test.method == "POST" || test.method == "DELETE" {
			methods = append(methods, "PUT")
		}
		for _, method := range methods {
			t.Run(test.name+method, func(t *testing.T) {
				path := test.path
				if method == "PUT" && test.method == "POST" {
					path += "/7"
				}
				service := &announcementHTTPService{}
				response := httptest.NewRecorder()
				announcementTestRouter("super_admin", service).ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(test.body)))
				if response.Code != 400 || service.mutated {
					t.Fatalf("invalid request reached service: %d %s", response.Code, response.Body.String())
				}
			})
		}
	}
}

func TestAnnouncementFailuresAreSanitized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		failure error
		status  int
	}{
		{domain.ErrInvalidAnnouncement, 400},
		{domain.ErrAnnouncementNotFound, 404},
		{errors.New("private storage details"), 500},
	} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			service := &announcementHTTPService{failure: test.failure}
			response := httptest.NewRecorder()
			path := "/admin/announcements"
			if method == "DELETE" || method == "PUT" {
				path += "/7"
			}
			announcementTestRouter("super_admin", service).ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(`{"title":"Title","body":"body"}`)))
			if response.Code != test.status || strings.Contains(response.Body.String(), "private storage details") {
				t.Fatalf("unexpected error envelope: %d %s", response.Code, response.Body.String())
			}
		}
	}
}

func TestAnnouncementsRequireAnActiveKeyPortalSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := &stubKeyPortalRepository{key: userdomain.APIKey{ID: 42, OrganizationID: 7, KeyHash: "hash", Status: "active"}, active: true}
	portal := NewKeyPortalHandler(keys, []byte("announcement-test-signing-secret"), nil, nil, nil)
	router := gin.New()
	portal.RegisterPublicRoutes(router.Group("/auth"))
	group := router.Group("/key-portal")
	portal.RegisterRoutes(group)
	service := &announcementHTTPService{}
	NewAnnouncementHandler(service).RegisterReadRoutes(group)
	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/auth/key-login", strings.NewReader(`{"api_key":"sk-test-key-123456"}`)))
	var session struct {
		Data struct {
			Token string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.Data.Token == "" {
		t.Fatalf("key login failed: %s %v", login.Body.String(), err)
	}
	for _, test := range []struct {
		name, token string
		active      bool
		status      int
	}{
		{"active", session.Data.Token, true, 200},
		{"missing", "", true, 401},
		{"invalid", "invalid", true, 401},
		{"revoked", session.Data.Token, false, 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			keys.active = test.active
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/key-portal/announcements", nil)
			request.Header.Set("Authorization", "Bearer "+test.token)
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("unexpected key readership: %d %s", response.Code, response.Body.String())
			}
			if test.status == 200 && (!strings.Contains(response.Body.String(), "# Update") || service.filter.OrganizationID != keys.key.OrganizationID || !service.filter.IncludeGlobal) {
				t.Fatal("key readership did not use the active key organization")
			}
		})
	}
	keys.active = true
	keys.key.OrganizationID = 99
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/key-portal/announcements?organization_id=7", nil)
	request.Header.Set("Authorization", "Bearer "+session.Data.Token)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.filter.OrganizationID != 99 {
		t.Fatalf("key readership used stale membership: %d %+v", response.Code, service.filter)
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, "/key-portal/announcements", nil))
		if response.Code != http.StatusNotFound || service.mutated {
			t.Fatal("key portal exposes announcement management")
		}
	}
}

func TestAnnouncementUpdateReturnsEditedContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	announcementTestRouter("super_admin", &announcementHTTPService{}).ServeHTTP(response,
		httptest.NewRequest(http.MethodPut, "/admin/announcements/7", strings.NewReader(`{"title":"Revised","body":"# Revised\n\n**Markdown**"}`)))
	var payload struct {
		Data domain.Announcement `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || payload.Data.ID != 7 || payload.Data.Title != "Revised" || payload.Data.Body != "# Revised\n\n**Markdown**" {
		t.Fatalf("unexpected edit response: %d %s", response.Code, response.Body.String())
	}
}

func TestAnnouncementScopesAreDerivedFromAuthenticatedMembership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{"super_admin", "org_admin"} {
		for _, operation := range []string{"overview", "manage", "create", "update", "delete"} {
			t.Run(role+operation, func(t *testing.T) {
				service := &announcementHTTPService{}
				method, path := http.MethodGet, "/admin/announcements"
				switch operation {
				case "manage":
					path += "/manage"
				case "create":
					method = http.MethodPost
				case "update":
					method, path = http.MethodPut, path+"/7"
				case "delete":
					method, path = http.MethodDelete, path+"/7"
				}
				response := httptest.NewRecorder()
				request := httptest.NewRequest(method, path+"?organization_id=99&include_global=true", strings.NewReader(`{"title":"Title","body":"Body"}`))
				announcementTestRouter(role, service).ServeHTTP(response, request)
				if response.Code >= 300 {
					t.Fatalf("operation failed: %d %s", response.Code, response.Body.String())
				}
				organizationID := uint64(2)
				if role == "super_admin" {
					organizationID = 0
				}
				if method == http.MethodGet {
					expected := domain.AnnouncementFilter{OrganizationID: organizationID, IncludeGlobal: operation == "overview"}
					if service.filter != expected {
						t.Fatalf("untrusted query changed scope: %+v, want %+v", service.filter, expected)
					}
				} else if service.organizationID != organizationID {
					t.Fatalf("wrong publication owner: %d", service.organizationID)
				}
			})
		}
	}
}

func TestAnnouncementManagerListRequiresAnAdministrator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{"", "super_admin", "org_admin", "user", "key_viewer"} {
		response := httptest.NewRecorder()
		announcementTestRouter(role, &announcementHTTPService{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/announcements/manage", nil))
		expected := http.StatusForbidden
		if role == "" {
			expected = http.StatusUnauthorized
		}
		if role == "super_admin" || role == "org_admin" {
			expected = http.StatusOK
		}
		if response.Code != expected {
			t.Fatalf("role %s: got %d, want %d", role, response.Code, expected)
		}
	}
}

func TestAnnouncementOrganizationCannotBeSpoofedInPublication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		service := &announcementHTTPService{}
		response := httptest.NewRecorder()
		path := "/admin/announcements"
		if method == http.MethodPut {
			path += "/7"
		}
		announcementTestRouter("org_admin", service).ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(`{"title":"Title","body":"Body","organization_id":0}`)))
		if response.Code != http.StatusBadRequest || service.mutated {
			t.Fatalf("organization spoof accepted: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestAnnouncementSessionsWithoutOrganizationFailClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, identity := range []string{"organization", "key"} {
		service := &announcementHTTPService{}
		router := gin.New()
		router.Use(func(ctx *gin.Context) {
			if identity == "key" {
				ctx.Set("key_portal_key", userdomain.APIKey{ID: 7})
				ctx.Set("identity", httpapi.Claims{UserID: 1, Role: "super_admin"})
			} else {
				ctx.Set("identity", httpapi.Claims{UserID: 1, Role: "org_admin"})
			}
		})
		NewAnnouncementHandler(service).RegisterAdminRoutes(router.Group("/admin"))
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(method, "/admin/announcements", strings.NewReader(`{"title":"Title","body":"Body"}`)))
			if response.Code != http.StatusUnauthorized || service.mutated || service.filter.IncludeGlobal {
				t.Fatalf("missing membership gained global access: %d %s", response.Code, response.Body.String())
			}
		}
	}
}
