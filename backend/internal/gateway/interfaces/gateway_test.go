package interfaces

import (
	admindomain "bosapi/backend/internal/admin/domain"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/usagetime"
	userdomain "bosapi/backend/internal/user/domain"
	userinfra "bosapi/backend/internal/user/infrastructure"
)

type stubGatewayKeyRepository struct{ key userdomain.APIKey }

type recordingGatewayKeyRepository struct {
	stubGatewayKeyRepository
	record userinfra.GatewayUsageRecord
}

func (repository *recordingGatewayKeyRepository) RecordGatewayUsageAndQuota(_ context.Context, record userinfra.GatewayUsageRecord) (bool, error) {
	repository.record = record
	return true, nil
}

func (repository stubGatewayKeyRepository) FindGatewayKey(context.Context, string, time.Time) (userdomain.APIKey, bool, error) {
	return repository.key, true, nil
}

func (stubGatewayKeyRepository) RecordGatewayUsageAndQuota(context.Context, userinfra.GatewayUsageRecord) (bool, error) {
	return false, nil
}

func testApp(t *testing.T) *app {
	t.Helper()
	return &app{path: filepath.Join(t.TempDir(), "data.enc"), encryptionKey: bytes.Repeat([]byte{7}, 32), sessions: map[string]oauthSession{}, toolSecurityPolicy: func(context.Context) (admindomain.ToolSecurityPolicy, error) {
		return admindomain.ToolSecurityPolicy{Mode: "block"}, nil
	}}
}
func TestEncryptedStorage(t *testing.T) {
	a := testApp(t)
	a.data.Providers = []provider{{ID: 1, AccessToken: "secret-access", RefreshToken: "secret-refresh"}}
	if err := a.save(); err != nil {
		t.Fatal(err)
	}
	b := testApp(t)
	b.path = a.path
	b.encryptionKey = a.encryptionKey
	if err := b.load(); err != nil {
		t.Fatal(err)
	}
	if b.data.Providers[0].AccessToken != "secret-access" {
		t.Fatal("credential not restored")
	}
	raw, err := os.ReadFile(a.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("secret-access")) {
		t.Fatal("credential stored in plaintext")
	}
}
func TestGatewayAuthentication(t *testing.T) {
	a := testApp(t)
	a.data.Providers = []provider{{ID: 1, Status: "active"}}
	key := "sk-gw-" + random(32)
	a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}
	for _, tc := range []struct {
		name, token string
		allowed     bool
	}{{"valid", key, true}, {"invalid", "sk-gw-invalid", false}, {"empty", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/models", nil)
			r.Header.Set("Authorization", "Bearer "+tc.token)
			_, ok := a.authenticateKey(r)
			if ok != tc.allowed {
				t.Fatalf("allowed=%v", ok)
			}
		})
	}
}

func TestDatabaseKeyAcceptsCustomFormat(t *testing.T) {
	const secret = "vendor:custom-token/123"
	a := testApp(t)
	a.keyRepository = stubGatewayKeyRepository{key: userdomain.APIKey{ID: 9, Name: "imported", KeyPrefix: secret, Status: "active", OrganizationID: 1}}
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+secret)
	key, ok := a.databaseKey(r)
	if !ok || key.ID != 9 {
		t.Fatalf("custom database key not accepted: key=%+v ok=%v", key, ok)
	}
	if !a.databaseKeyCandidate(r) {
		t.Fatal("custom key should be classified as a database key candidate")
	}
}

func TestGatewayUsageStatisticsUseShanghaiDay(t *testing.T) {
	now := time.Now()
	day := now.In(usagetime.Location()).Format("2006-01-02")
	previousLocation := time.Local
	for _, offsetHours := range []int{-12, 14} {
		location := time.FixedZone("non-Shanghai host", offsetHours*60*60)
		if now.In(location).Format("2006-01-02") != day {
			time.Local = location
			break
		}
	}
	t.Cleanup(func() { time.Local = previousLocation })
	if now.In(time.Local).Format("2006-01-02") == day {
		t.Fatal("test host must have a different calendar date from Shanghai")
	}

	a := testApp(t)
	secret := "sk-gw-timezone-regression-secret"
	key := apiKey{ID: 1, Hash: digest(secret), Status: "active"}
	a.data.Keys = []apiKey{key}
	a.processUsageEvent(usageEvent{
		Context: context.Background(), Key: key, KeyHash: key.Hash, Model: "timezone-model", Status: http.StatusOK,
		Duration: time.Second, Metrics: usageMetrics{InputTokens: 2, OutputTokens: 3, TotalTokens: 5, Known: true},
	})
	if daily := a.data.Usage[key.Hash].Daily; len(daily) != 1 || daily[day].Requests != 1 {
		t.Fatalf("usage should belong to Shanghai day %s, got=%v", day, daily)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	a.usage(response, request)
	var value struct {
		Data struct {
			Today struct {
				Requests    int64 `json:"requests"`
				TotalTokens int64 `json:"total_tokens"`
			} `json:"today"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value.Data.Today.Requests != 1 || value.Data.Today.TotalTokens != 5 {
		t.Fatalf("today lookup should use Shanghai day: %s", response.Body.String())
	}
}

func TestGatewayRequestLogCorrelatesFailureWithoutCredentials(t *testing.T) {
	a := testApp(t)
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })

	r := httptest.NewRequest(http.MethodGet, "/v1/models?api_key=secret-in-query", nil)
	r.Header.Set("Authorization", "Bearer secret-in-header")
	r.Header.Set("X-Request-ID", "bad\nrequest-id")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", w.Code)
	}
	id := w.Header().Get("X-Request-ID")
	if !strings.HasPrefix(id, "req_") || r.Header.Get("X-Request-ID") != id {
		t.Fatalf("request id not correlated: response=%q request=%q", id, r.Header.Get("X-Request-ID"))
	}
	logs := output.String()
	for _, expected := range []string{"request_id=" + id, "method=GET", "endpoint=models", "status=401", "error_code=unauthorized", "outcome=error", "reason=key_not_found_or_unusable"} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("missing %q in log %q", expected, logs)
		}
	}
	for _, secret := range []string{"secret-in-query", "secret-in-header", "bad\nrequest-id"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("credential or unsafe request id leaked in log: %q", logs)
		}
	}
}

func TestEarlyGatewayFailureRecordsActualErrorCode(t *testing.T) {
	repository := &recordingGatewayKeyRepository{}
	a := testApp(t)
	a.keyRepository = repository
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("X-Request-ID", "request-invalid-provider")
	a.recordEarlyError(r, apiKey{ID: 7, OrgID: 9}, http.StatusBadRequest, errorcode.InvalidProvider)
	if repository.record.RequestID != "request-invalid-provider" || repository.record.ErrorCode != string(errorcode.InvalidProvider) || repository.record.StatusCode != http.StatusBadRequest {
		t.Fatalf("early error record=%+v", repository.record)
	}
}

func TestRequestClientIPSupportsIPv6AndForwardedHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.RemoteAddr = "[2001:db8::1]:443"
	if got := requestClientIP(r); got == nil || !got.Equal(net.ParseIP("2001:db8::1")) {
		t.Fatalf("client ip=%v", got)
	}
	r.Header.Set("X-Forwarded-For", "192.0.2.10, 198.51.100.1")
	if got := requestClientIP(r); got == nil || !got.Equal(net.ParseIP("192.0.2.10")) {
		t.Fatalf("forwarded client ip=%v", got)
	}
}

func TestKeyUsableAppliesCIDRLists(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.RemoteAddr = "192.0.2.44:1234"
	key := apiKey{Status: "active", IPAllowlist: []string{"192.0.2.0/24"}}
	if !keyUsable(key, r) {
		t.Fatal("expected CIDR allowlist to match")
	}
	key.IPBlocklist = []string{"192.0.2.0/25"}
	if keyUsable(key, r) {
		t.Fatal("expected CIDR blocklist to reject")
	}
}

func TestDatabaseKeyAppliesIPLists(t *testing.T) {
	for _, test := range []struct {
		name      string
		clientIP  string
		allowlist []string
		blocklist []string
		allowed   bool
	}{
		{name: "allowlisted", clientIP: "192.0.2.44", allowlist: []string{"192.0.2.0/24"}, allowed: true},
		{name: "outside allowlist", clientIP: "198.51.100.44", allowlist: []string{"192.0.2.0/24"}},
		{name: "blocklist overrides allowlist", clientIP: "192.0.2.44", allowlist: []string{"192.0.2.0/24"}, blocklist: []string{"192.0.2.44"}},
		{name: "unrestricted", clientIP: "198.51.100.44", allowed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := testApp(t)
			a.keyRepository = stubGatewayKeyRepository{key: userdomain.APIKey{
				ID: 1, Status: "active", IPAllowlist: test.allowlist, IPBlocklist: test.blocklist,
			}}
			request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			request.RemoteAddr = net.JoinHostPort(test.clientIP, "1234")
			request.Header.Set("Authorization", "Bearer sk-gw-test-key")
			key, ok := a.authenticatedKey(request)
			if ok != test.allowed {
				t.Fatalf("allowed=%v, want %v", ok, test.allowed)
			}
			if ok && key.ID != 1 {
				t.Fatalf("key ID=%d, want 1", key.ID)
			}
		})
	}
}
func TestOAuthStateIsSingleUse(t *testing.T) {
	a := testApp(t)
	a.sessions["session"] = oauthSession{State: "expected", Verifier: "verifier", Expires: time.Now().Add(time.Minute)}
	for _, state := range []string{"wrong", "expected"} {
		raw, _ := json.Marshal(map[string]string{"session_id": "session", "code": "code", "state": state})
		r := httptest.NewRequest("POST", "/api/v1/admin/openai/create-from-oauth", strings.NewReader(string(raw)))
		w := httptest.NewRecorder()
		a.finishOAuth(w, r)
		if w.Code != 400 {
			t.Fatalf("status=%d", w.Code)
		}
	}
}
func TestInvalidResponses(t *testing.T) {
	for _, payload := range []string{`{}`, `{"model":"gpt","input":null}`, `{"model":"","input":"hello"}`} {
		var q map[string]json.RawMessage
		if err := json.Unmarshal([]byte(payload), &q); err != nil {
			t.Fatal(err)
		}
		if validResponses(q) {
			t.Fatalf("accepted %s", payload)
		}
	}
}

func TestOrganizationModelAllowed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		models []string
		model  string
		want   bool
	}{
		{"default organization wildcard", []string{"*"}, "zai-org/GLM-5.3-Flash", true},
		{"configured model", []string{"gpt-5"}, "gpt-5", true},
		{"unlisted model", []string{"gpt-5"}, "zai-org/GLM-5.3-Flash", false},
		{"empty policy", nil, "zai-org/GLM-5.3-Flash", true},
		{"model-free request", []string{"gpt-5"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := organizationModelAllowed(tc.models, tc.model); got != tc.want {
				t.Fatalf("allowed=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestOrganizationPolicyDenial(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      string
		providerIDs []uint64
		models      []string
		providerID  int
		model       string
		want        errorcode.Code
	}{
		{name: "disabled organization", status: "disabled", providerIDs: []uint64{2}, providerID: 2, model: "glm-5.3", want: errorcode.PolicyNotAllowed},
		{name: "no active provider", status: "active", providerIDs: []uint64{2}, providerID: 0, model: "glm-5.3", want: errorcode.InvalidProvider},
		{name: "provider not allowed", status: "active", providerIDs: []uint64{2}, providerID: 3, model: "glm-5.3", want: errorcode.InvalidProvider},
		{name: "model not allowed", status: "active", providerIDs: []uint64{2}, models: []string{"gpt-5"}, providerID: 2, model: "glm-5.3", want: errorcode.ModelNotAllowed},
		{name: "allowed", status: "active", providerIDs: []uint64{2}, models: []string{"glm-5.3"}, providerID: 2, model: "glm-5.3"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := organizationPolicyDenial(test.status, test.providerIDs, test.models, test.providerID, test.model); got != test.want {
				t.Fatalf("denial=%q, want %q", got, test.want)
			}
		})
	}
}

func TestGatewayPolicyErrorMessages(t *testing.T) {
	for _, test := range []struct {
		code errorcode.Code
		want string
	}{
		{
			code: errorcode.InvalidProvider,
			want: "Invalid provider. Please contact the platform administrator to check whether the provider assigned to this API key is valid.",
		},
		{
			code: errorcode.ModelNotAllowed,
			want: "Invalid model. Please contact the platform administrator to check whether this API key supports the requested model.",
		},
	} {
		if got := gatewayErrorMessage(test.code); got != test.want {
			t.Errorf("message=%q, want %q", got, test.want)
		}
	}
}

func TestQuotaExceeded(t *testing.T) {
	tests := []struct {
		name string
		key  apiKey
		want bool
	}{
		{name: "tokens", key: apiKey{QuotaTokens: int64Ptr(100), QuotaUsedTokens: 100}, want: true},
		{name: "amount", key: apiKey{QuotaAmount: float64Ptr(0.001), QuotaUsedAmount: 0.001}, want: true},
		{name: "under", key: apiKey{QuotaTokens: int64Ptr(100), QuotaUsedTokens: 99}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := quotaExceeded(test.key); got != test.want {
				t.Fatalf("quotaExceeded() = %v, want %v", got, test.want)
			}
		})
	}
}

func int64Ptr(value int64) *int64       { return &value }
func float64Ptr(value float64) *float64 { return &value }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestResponsesRoute(t *testing.T) {
	a := testApp(t)
	key := "sk-gw-" + random(32)
	a.data.Providers = []provider{{ID: 1, Status: "active", AccessToken: "upstream-token", AccountID: "acct", ExpiresAt: time.Now().Add(time.Hour)}}
	a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != upstream+"/responses" || r.Header.Get("Authorization") != "Bearer upstream-token" || r.Header.Get("ChatGPT-Account-ID") != "acct" {
			t.Fatal("invalid upstream request")
		}
		var q map[string]any
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Fatal(err)
		}
		if q["stream"] != true || q["store"] != false {
			t.Fatal("codex request not normalized")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[]}}\n\n")), Header: make(http.Header)}, nil
	})}
	for _, stream := range []bool{false, true} {
		raw, _ := json.Marshal(map[string]any{"model": "gpt-test", "input": "hello", "stream": stream})
		r := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if stream && !strings.Contains(w.Body.String(), "response.completed") {
			t.Fatal("missing stream event")
		}
		if !stream && !strings.Contains(w.Body.String(), "resp_1") {
			t.Fatal("missing response")
		}
	}
}

func TestOAuthCallbackRejectsInvalidState(t *testing.T) {
	a := testApp(t)
	for _, path := range []string{"/auth/callback", "/auth/callback?code=abc&state=wrong"} {
		w := httptest.NewRecorder()
		a.oauthCallback(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatalf("status=%d", w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("callback may be cached")
		}
	}
}

func TestOAuthCompletionRetry(t *testing.T) {
	a := testApp(t)
	a.completed = map[string]completedOAuth{"session": {StateHash: digest("state"), CodeHash: digest("code"), ProviderID: 7, Expires: time.Now().Add(time.Minute)}}
	for _, tc := range []struct {
		name, code, state string
		want              int
	}{{"same request", "code", "state", 200}, {"different code", "other", "state", 400}, {"different state", "code", "other", 400}} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]string{"session_id": "session", "code": tc.code, "state": tc.state})
			w := httptest.NewRecorder()
			a.finishOAuth(w, httptest.NewRequest("POST", "/api/v1/admin/openai/create-from-oauth", bytes.NewReader(raw)))
			if w.Code != tc.want {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}

func TestCompletedResponseCollectsOutputItems(t *testing.T) {
	stream := "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello\"}]}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[]}}\n\n"
	response, err := completedResponse(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if got := extractText(response); got != "Hello" {
		t.Fatalf("text=%q", got)
	}
}
