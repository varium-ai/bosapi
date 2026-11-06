package interfaces

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"bosapi/backend/internal/analytics/application"
	authinterfaces "bosapi/backend/internal/auth/interfaces"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

func TestUsageReportingRoutesRequireSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{"user", "org_admin", "super_admin"} {
		for _, route := range []struct{ method, path, body string }{{"GET", "/usage-reporting", ""}, {"PUT", "/usage-reporting", `{"enabled":true}`}, {"POST", "/usage-reporting/preview", "{}"}, {"POST", "/usage-reporting/reset-installation", "{}"}} {
			router := gin.New()
			group := router.Group("/api/v1/admin", func(c *gin.Context) { c.Set("identity", httpapi.Claims{UserID: 1, Role: role}) }, authinterfaces.RequireRoles("super_admin"))
			service := application.New(nil, application.Config{ForceDisabled: true}, nil, "")
			NewHandler(service).RegisterRoutes(group)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(route.method, "/api/v1/admin"+route.path, bytes.NewBufferString(route.body)))
			if role != "super_admin" && rec.Code != http.StatusForbidden {
				t.Fatalf("role=%s path=%s status=%d", role, route.path, rec.Code)
			}
			if role == "super_admin" && rec.Code == http.StatusForbidden {
				t.Fatal("super admin rejected")
			}
			service.Close()
		}
	}
}

func TestEnableRejectsMissingAndNonBooleanFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	service := application.New(nil, application.Config{}, nil, "")
	defer service.Close()
	NewHandler(service).RegisterRoutes(router.Group(""))
	for _, body := range []string{`{}`, `null`, `{"enabled":null}`, `{"enabled":"false"}`, `{"enabled":1}`, `{"enabled":false,"endpoint":"https://evil.invalid"}`, `{"enabled":false}{}`} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("PUT", "/usage-reporting", bytes.NewBufferString(body)))
		if rec.Code != 400 {
			t.Fatalf("body=%s status=%d", body, rec.Code)
		}
	}
}
