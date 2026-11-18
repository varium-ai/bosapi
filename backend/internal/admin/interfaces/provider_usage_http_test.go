package interfaces

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/subscription"
	"github.com/gin-gonic/gin"
)

type usageProviderRepository struct {
	permissionProviderRepository
	credential string
	reads      int
}

func (r *usageProviderRepository) ProviderCredential(context.Context, uint64) (domain.Provider, string, error) {
	r.reads++
	return r.provider, r.credential, nil
}

func TestSubscriptionUsageHTTPProtectsAccountAndSanitizesUpstreamFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	raw, err := json.Marshal(subscription.Credentials{
		AccessToken: "private-access", RefreshToken: "private-refresh", AccountID: "private-account",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, role, accountType string
		upstreamStatus, status  int
		body                    string
	}{
		{"super admin", "super_admin", "oauth", 200, 200, `"windows":[]`},
		{"assigned public", "org_admin", "oauth", 0, 403, "read-only"},
		{"API key channel", "super_admin", "apikey", 0, 400, "invalid_request"},
		{"upstream rate limit", "super_admin", "oauth", 429, 502, "provider_rate_limited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &usageProviderRepository{credential: string(raw)}
			repo.provider = domain.Provider{
				ID: 7, AccountType: tc.accountType, Platform: "openai", BaseURL: "https://custom.invalid",
			}
			client := subscription.Client{HTTP: &http.Client{Transport: oauthHTTPTransport(func(req *http.Request) (*http.Response, error) {
				if tc.upstreamStatus == 0 || req.URL.Host != "chatgpt.com" {
					t.Fatalf("unexpected upstream request to %s", req.URL)
				}
				return &http.Response{StatusCode: tc.upstreamStatus, Body: io.NopCloser(strings.NewReader(`{"email":"private-account","error":"private-access"}`))}, nil
			})}}
			service := application.NewProviderService(repo, nil, nil, func(v string) (string, error) { return v, nil }).WithSubscriptionClient(client)
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				ctx.Set("identity", httpapi.Claims{UserID: 7, OrganizationID: 11, Role: tc.role})
				ctx.Next()
			})
			NewProviderHandler(service).RegisterRoutes(router.Group("/admin"))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/providers/7/subscription-usage", nil))
			body := w.Body.String()
			if w.Code != tc.status || !strings.Contains(body, tc.body) || strings.Contains(body, "private-") {
				t.Fatalf("unexpected status=%d response=%s", w.Code, body)
			}
			if tc.upstreamStatus == 0 && repo.reads != 0 {
				t.Fatal("read credentials for an ineligible channel")
			}
			if w.Code == 200 && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("account usage must not be cached")
			}
		})
	}
}
