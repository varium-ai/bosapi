package interfaces

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

func TestToolSecurityPolicyHTTP(t *testing.T) {
	router, _ := setupTestRouter()
	for _, test := range []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{"GET", "/tool-security-policy", "", 200, `"mode":"observe"`},
		{"PUT", "/tool-security-policy", `{"mode":"block"}`, 200, `"mode":"block"`},
		{"GET", "/tool-security-policy", "", 200, `"mode":"block"`},
		{"PUT", "/tool-security-policy", `{"mode":"invalid"}`, 400, "invalid_request"},
		{"PUT", "/tool-security-policy", `{}`, 400, "invalid_request"},
		{"PUT", "/tool-security-policy", `{`, 400, "invalid_request"},
		{"PUT", "/settings/security.tool_call_mode", `{"value_text":"observe"}`, 400, "invalid_request"},
		{"DELETE", "/settings/security.tool_call_mode", "", 400, "invalid_request"},
		{"GET", "/tool-security-policy", "", 200, `"mode":"block"`},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(test.method, "/api/v1/admin"+test.path, strings.NewReader(test.body)))
		if w.Code != test.status || !strings.Contains(w.Body.String(), test.contains) {
			t.Fatalf("%s %s: %d %s", test.method, test.path, w.Code, w.Body.String())
		}
	}
}

func TestToolSecurityPolicyRequiresSuperAdmin(t *testing.T) {
	for _, role := range []string{"", "org_admin", "admin", "user"} {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			t.Run(role+method, func(t *testing.T) {
				router := gin.New()
				router.Use(func(ctx *gin.Context) {
					if role != "" {
						ctx.Set("identity", httpapi.Claims{UserID: 1, Role: role})
					}
				})
				repo := &memorySettingRepository{data: map[string]domain.SystemSetting{}}
				NewSystemSettingHandler(application.NewSystemSettingService(repo)).RegisterAdminRoutes(router.Group(""))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(method, "/tool-security-policy", strings.NewReader(`{"mode":"observe"}`)))
				want := 403
				if role == "" {
					want = 401
				}
				if w.Code != want || len(repo.data) != 0 {
					t.Fatalf("status=%d settings=%v", w.Code, repo.data)
				}
			})
		}
	}
}
