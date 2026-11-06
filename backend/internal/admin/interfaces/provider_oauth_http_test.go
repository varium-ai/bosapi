package interfaces

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/subscription"
	"github.com/gin-gonic/gin"
)

func TestSubscriptionHTTPPreservesSanitizedGoogleProjectFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := subscription.Client{HTTP: &http.Client{Transport: oauthHTTPTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"error":{"status":"PERMISSION_DENIED","message":"private-access private-account","details":[{"reason":"SERVICE_DISABLED"}]}}`))}, nil
	})}}
	_, err := client.CodeAssistProject(context.Background(), "gemini", "private-access", "")
	if err == nil {
		t.Fatal("expected Google project failure")
	}
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	providerFailure(ctx, err)
	body := w.Body.String()
	if w.Code != http.StatusBadRequest || !strings.Contains(body, "loadCodeAssist") || !strings.Contains(body, "HTTP 403 PERMISSION_DENIED") || !strings.Contains(body, "SERVICE_DISABLED") {
		t.Fatalf("HTTP handler discarded diagnostic details: %d %s", w.Code, body)
	}
	if strings.Contains(body, "private-access") || strings.Contains(body, "private-account") {
		t.Fatal("HTTP error exposed private upstream information")
	}
}

func TestSubscriptionHTTPConnectsGeminiWhenOnboardingOmitsProject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("BOS_GEMINI_OAUTH_CLIENT_ID", "test-client")
	t.Setenv("BOS_GEMINI_OAUTH_CLIENT_SECRET", "test-secret")
	repository := &scopedProviderRepository{}
	listings, onboards := 0, 0
	service := application.NewProviderService(repository, func(s string) (string, error) { return "encrypted:" + s, nil }, nil, nil).WithSubscriptionClient(subscription.Client{HTTP: &http.Client{Transport: oauthHTTPTransport(func(req *http.Request) (*http.Response, error) {
		raw := ""
		switch req.URL.Host + req.URL.Path {
		case "oauth2.googleapis.com/token":
			raw = `{"access_token":"private-access","refresh_token":"private-refresh","expires_in":3600}`
		case "cloudcode-pa.googleapis.com/v1internal:loadCodeAssist":
			raw = `{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`
		case "cloudcode-pa.googleapis.com/v1internal:onboardUser":
			onboards++
			raw = `{"done":true,"response":{}}`
		case "cloudresourcemanager.googleapis.com/v1/projects":
			listings++
			raw = `{"projects":[{"projectId":"existing-project","lifecycleState":"ACTIVE"}]}`
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(raw))}, nil
	})}})
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("identity", httpapi.Claims{UserID: 7, Role: "super_admin"}); c.Next() })
	NewProviderHandler(service).RegisterRoutes(router.Group("/admin"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/providers/oauth/gemini/start", nil))
	var auth struct {
		Data subscription.Authorization `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &auth) != nil {
		t.Fatal("could not start authorization")
	}
	payload, _ := json.Marshal(map[string]any{
		"session_id": auth.Data.SessionID, "callback": "test-code", "state": auth.Data.State,
		"provider": map[string]any{"name": "Gemini", "platform": "gemini", "account_type": "oauth", "api_format": "gemini", "base_url": "https://cloudcode-pa.googleapis.com", "models": []string{}},
	})
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/providers/oauth/gemini/complete", strings.NewReader(string(payload))))
	if w.Code != http.StatusCreated || repository.created.Platform != "gemini" || repository.created.AccountType != "oauth" || listings != 1 || onboards != 1 {
		t.Fatalf("fallback did not save the channel: status=%d listings=%d onboards=%d", w.Code, listings, onboards)
	}
	if strings.Contains(w.Body.String(), "private-access") || strings.Contains(w.Body.String(), "private-refresh") {
		t.Fatal("channel response exposed Google tokens")
	}
}

type oauthHTTPTransport func(*http.Request) (*http.Response, error)

func (f oauthHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSubscriptionHTTPBindsAdministratorSessionAndHidesTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := &scopedProviderRepository{}
	service := application.NewProviderService(repository, func(s string) (string, error) { return "encrypted:" + s, nil }, nil, nil).WithSubscriptionClient(subscription.Client{HTTP: &http.Client{Transport: oauthHTTPTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"private-access-token","refresh_token":"private-refresh-token","expires_in":3600}`))}, nil
	})}})
	org := uint64(11)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("identity", httpapi.Claims{UserID: 7, OrganizationID: org, Role: "super_admin"})
		c.Next()
	})
	NewProviderHandler(service).RegisterRoutes(router.Group("/admin"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/admin/providers/oauth/anthropic/start", nil))
	var auth struct {
		Data subscription.Authorization `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &auth) != nil || auth.Data.SessionID == "" {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	payload, _ := json.Marshal(map[string]any{"session_id": auth.Data.SessionID, "callback": "code#" + auth.Data.State, "state": auth.Data.State, "provider": map[string]any{"name": "Claude", "platform": "anthropic", "account_type": "oauth", "api_format": "anthropic_messages", "base_url": "https://api.anthropic.com", "models": []string{"claude-test"}}})
	finish := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", "/admin/providers/oauth/anthropic/complete", strings.NewReader(string(payload))))
		return w
	}
	org = 22
	w = finish()
	if w.Code != 400 {
		t.Fatalf("cross-organization session accepted: %d %s", w.Code, w.Body)
	}
	org = 11
	w = finish()
	if w.Code != 201 || strings.Contains(w.Body.String(), "private-access-token") || strings.Contains(w.Body.String(), "private-refresh-token") {
		t.Fatalf("finish: %d %s", w.Code, w.Body)
	}
	if replay := finish(); replay.Code != 400 {
		t.Fatal("authorization session replay accepted")
	}
}

func TestSubscriptionHTTPReportsIncompleteGoogleClientConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("identity", httpapi.Claims{UserID: 7, Role: "super_admin"})
		c.Next()
	})
	NewProviderHandler(application.NewProviderService(&scopedProviderRepository{}, nil, nil, nil)).RegisterRoutes(router.Group("/admin"))
	for _, platform := range []string{"gemini", "antigravity"} {
		prefix := "BOS_" + strings.ToUpper(platform) + "_OAUTH"
		for _, values := range []struct{ id, secret string }{{"", ""}, {"custom-client", ""}, {"", "custom-secret"}} {
			t.Setenv(prefix+"_CLIENT_ID", values.id)
			t.Setenv(prefix+"_CLIENT_SECRET", values.secret)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/providers/oauth/"+platform+"/start", nil))
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), prefix+"_CLIENT_ID") || !strings.Contains(w.Body.String(), prefix+"_CLIENT_SECRET") {
				t.Fatalf("expected an actionable configuration error: %d %s", w.Code, w.Body)
			}
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/providers/oauth/unsupported/start", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unsupported platform must remain a bad request: %d %s", w.Code, w.Body)
	}
}

func TestSubscriptionHTTPStartsGoogleWithEnvironmentConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("identity", httpapi.Claims{UserID: 7, Role: "super_admin"})
		c.Next()
	})
	NewProviderHandler(application.NewProviderService(&scopedProviderRepository{}, nil, nil, nil)).RegisterRoutes(router.Group("/admin"))
	for _, platform := range []string{"gemini", "antigravity"} {
		t.Run(platform, func(t *testing.T) {
			prefix := "BOS_" + strings.ToUpper(platform) + "_OAUTH"
			clientID, clientSecret := "test-"+platform+"-client", "test-"+platform+"-secret"
			t.Setenv(prefix+"_CLIENT_ID", clientID)
			t.Setenv(prefix+"_CLIENT_SECRET", clientSecret)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/providers/oauth/"+platform+"/start", nil))
			var auth struct {
				Data subscription.Authorization `json:"data"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &auth) != nil || auth.Data.SessionID == "" || auth.Data.State == "" {
				t.Fatalf("start with environment configuration failed: %d", w.Code)
			}
			uri, err := url.Parse(auth.Data.AuthURL)
			if err != nil || uri.Host != "accounts.google.com" || uri.Query().Get("client_id") != clientID || uri.Query().Get("state") != auth.Data.State || uri.Query().Get("code_challenge_method") != "S256" || strings.Contains(w.Body.String(), "client_secret") || strings.Contains(w.Body.String(), clientSecret) {
				t.Fatal("start did not return a valid public authorization link")
			}
		})
	}
}

func TestSubscriptionHTTPConnectsAntigravityUsingItsOwnAuthorization(t *testing.T) {
	t.Setenv("BOS_ANTIGRAVITY_OAUTH_CLIENT_ID", "test-antigravity-client")
	t.Setenv("BOS_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "test-antigravity-secret")
	gin.SetMode(gin.TestMode)
	repository := &scopedProviderRepository{}
	calls := 0
	service := application.NewProviderService(repository, func(s string) (string, error) { return "encrypted:" + s, nil }, nil, nil).WithSubscriptionClient(subscription.Client{HTTP: &http.Client{Transport: oauthHTTPTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		raw := `{"access_token":"private-access","refresh_token":"private-refresh","expires_in":3600}`
		if calls == 1 {
			if err := r.ParseForm(); err != nil || r.URL.Host != "oauth2.googleapis.com" || r.PostForm.Get("client_id") != "test-antigravity-client" || r.PostForm.Get("redirect_uri") != "http://localhost:8085/callback" {
				t.Fatal("wrong OAuth client or redirect")
			}
		} else {
			if r.URL.Path != "/v1internal:loadCodeAssist" {
				t.Fatalf("unexpected project endpoint %s", r.URL)
			}
			raw = `{"cloudaicompanionProject":"managed-project"}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}, nil
	})}})
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("identity", httpapi.Claims{UserID: 7, Role: "super_admin"}); c.Next() })
	NewProviderHandler(service).RegisterRoutes(router.Group("/admin"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/providers/oauth/antigravity/start", nil))
	var auth struct {
		Data subscription.Authorization `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &auth) != nil || auth.Data.AuthURL == "" || calls != 0 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	payload, _ := json.Marshal(map[string]any{"session_id": auth.Data.SessionID, "callback": "http://localhost:8085/callback?code=code&state=" + auth.Data.State, "state": auth.Data.State, "provider": map[string]any{"name": "Antigravity", "platform": "antigravity", "account_type": "oauth", "api_format": "gemini", "base_url": "https://cloudcode-pa.googleapis.com", "models": []string{"model-test"}}})
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/providers/oauth/antigravity/complete", strings.NewReader(string(payload))))
	if w.Code != 201 || calls != 2 || strings.Contains(w.Body.String(), "private-access") || strings.Contains(w.Body.String(), "private-refresh") {
		t.Fatalf("complete: %d %s", w.Code, w.Body)
	}
	if repository.created.Platform != "antigravity" || repository.created.AccountType != "oauth" || repository.created.APIFormat != "gemini" || repository.created.BaseURL != "https://cloudcode-pa.googleapis.com" {
		t.Fatal("saved channel lost its official account configuration")
	}
}

func TestSubscriptionHTTPConnectsOpenAIFromPastedLoopbackCallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := &scopedProviderRepository{}
	cfg, _ := subscription.Configuration("openai")
	const code = "test-authorization.code+with/slash=padding"
	access := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"test-account"}}`)) + ".signature"
	challenge := ""
	calls := 0
	service := application.NewProviderService(repository, func(s string) (string, error) { return "encrypted:" + s, nil }, nil, nil).WithSubscriptionClient(subscription.Client{HTTP: &http.Client{Transport: oauthHTTPTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if r.URL.String() != cfg.TokenURL || r.PostForm.Get("code") != code || r.PostForm.Get("redirect_uri") != cfg.RedirectURI || base64.RawURLEncoding.EncodeToString(hash[:]) != challenge {
			t.Fatal("pasted callback lost its code, redirect URI, or PKCE binding")
		}
		raw, _ := json.Marshal(map[string]any{"access_token": access, "refresh_token": "test-refresh", "expires_in": 3600})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("identity", httpapi.Claims{UserID: 7, Role: "super_admin"})
		c.Next()
	})
	NewProviderHandler(service).RegisterRoutes(router.Group("/admin"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/providers/oauth/openai/start", nil))
	var auth struct {
		Data subscription.Authorization `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &auth) != nil {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	authorizeURL, _ := url.Parse(auth.Data.AuthURL)
	challenge = authorizeURL.Query().Get("code_challenge")
	// No listener or browser request to localhost is needed for manual completion.
	callback := "http://localhost:1455/auth/callback?" + url.Values{"code": {code}, "state": {auth.Data.State}, "scope": {"openid profile email offline_access"}}.Encode()
	payload, _ := json.Marshal(map[string]any{
		"session_id": auth.Data.SessionID, "state": auth.Data.State, "callback": callback,
		"provider": map[string]any{"name": "ChatGPT", "platform": "openai", "account_type": "oauth", "api_format": cfg.APIFormat, "base_url": cfg.BaseURL, "models": []string{}},
	})
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/providers/oauth/openai/complete", strings.NewReader(string(payload))))
	if w.Code != http.StatusCreated || calls != 1 || repository.created.AccountType != "oauth" || repository.created.Platform != "openai" || strings.Contains(w.Body.String(), access) || strings.Contains(w.Body.String(), "test-refresh") {
		t.Fatalf("complete: %d %s", w.Code, w.Body)
	}
}
