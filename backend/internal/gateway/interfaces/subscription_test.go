package interfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	admininfra "bosapi/backend/internal/admin/infrastructure"
	"bosapi/backend/internal/platform/subscription"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestSubscriptionNativeAuthenticationAndGeminiEnvelope(t *testing.T) {
	for _, platform := range []string{"anthropic", "gemini", "antigravity"} {
		for _, stream := range []bool{false, true} {
			t.Run(platform+map[bool]string{false: "_json", true: "_sse"}[stream], func(t *testing.T) {
				a := testApp(t)
				cfg, _ := subscription.Configuration(platform)
				p := withSubscriptionCredential(provider{ID: 1, Platform: platform, APIFormat: cfg.APIFormat, BaseURL: cfg.BaseURL}, subscription.Credentials{AccessToken: "subscription-access", RefreshToken: "subscription-refresh", ProjectID: "project", ExpiresAt: time.Now().Add(time.Hour)})
				path := "messages"
				payload := `{"model":"claude-test","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`
				if platform != "anthropic" {
					path = "models/gemini-test:generateContent"
					if stream {
						path = "models/gemini-test:streamGenerateContent"
					}
					payload = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
				}
				if stream && platform == "anthropic" {
					payload = strings.TrimSuffix(payload, "}") + `,"stream":true}`
				}
				a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.Header.Get("Authorization") != "Bearer subscription-access" || r.Header.Get("x-api-key") != "" || r.Header.Get("x-goog-api-key") != "" {
						t.Fatal("wrong upstream credential")
					}
					if platform == "anthropic" {
						if r.Header.Get("User-Agent") != "claude-code/test" || r.Header.Get("X-App") != "cli" || r.Header.Get("X-Stainless-Runtime") != "node" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
							t.Fatal("CLI metadata or credential isolation was lost")
						}
						if !strings.Contains(r.Header.Get("anthropic-beta"), "oauth-2025-04-20") || !strings.Contains(r.Header.Get("anthropic-beta"), "custom-beta") {
							t.Fatal("missing beta headers")
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"type":"message","content":[{"type":"text","text":"ok"}]}`))}, nil
					}
					if r.URL.String() != "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse" {
						t.Fatalf("endpoint=%s", r.URL)
					}
					var wrapped struct {
						Model, Project string
						Request        json.RawMessage
						RequestID      string `json:"requestId"`
						UserAgent      string `json:"userAgent"`
						RequestType    string `json:"requestType"`
					}
					if json.NewDecoder(r.Body).Decode(&wrapped) != nil || wrapped.Model != "gemini-test" || wrapped.Project != "project" || !strings.Contains(string(wrapped.Request), "hello") {
						t.Fatal("invalid Code Assist envelope")
					}
					if platform == "antigravity" && (r.Header.Get("User-Agent") != subscription.AntigravityUserAgent || !strings.HasPrefix(wrapped.RequestID, "agent-") || wrapped.UserAgent != "antigravity" || wrapped.RequestType != "agent" || !strings.Contains(string(wrapped.Request), "You are Antigravity")) {
						t.Fatal("missing Antigravity agent metadata")
					}
					if platform == "gemini" && (wrapped.RequestID != "" || strings.Contains(string(wrapped.Request), "You are Antigravity")) {
						t.Fatal("Antigravity metadata leaked into Gemini")
					}
					body := "data: {\"response\":{\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]}}]}}\n\ndata: {\"response\":{\"candidates\":[{\"index\":0,\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2}}}\n\n"
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})}
				r := httptest.NewRequest("POST", "/v1/"+path+"?key=client-secret", strings.NewReader(payload))
				r.Header.Set("Authorization", "Bearer bos-key")
				r.Header.Set("x-api-key", "client-secret")
				r.Header.Set("anthropic-beta", "custom-beta")
				r.Header.Set("User-Agent", "claude-code/test")
				r.Header.Set("X-App", "cli")
				r.Header.Set("X-Stainless-Runtime", "node")
				r.Header.Set("Cookie", "client-session=private")
				r.Header.Set("Proxy-Authorization", "private")
				res, cancel, err := a.nativeRoundTrip(r, p, path, []byte(payload))
				if err != nil {
					t.Fatal(err)
				}
				defer cancel()
				defer res.Body.Close()
				raw, err := io.ReadAll(res.Body)
				if err != nil {
					t.Fatal(err)
				}
				if platform != "anthropic" {
					if strings.Contains(string(raw), `"response":`) || !strings.Contains(string(raw), "promptTokenCount") {
						t.Fatal("wrapped response leaked")
					}
					if !stream && !json.Valid(raw) {
						t.Fatalf("invalid aggregate %s", raw)
					}
				}
			})
		}
	}
}

func TestGeminiSubscriptionStreamPreservesSignaturesAndRejectsTruncation(t *testing.T) {
	source := "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"read\",\"args\":{\"id\":1790000000000000001}},\"thoughtSignature\":\"signed\"}]},\"finishReason\":\"STOP\"}]}}\n\n"
	body := io.NopCloser(strings.NewReader(source))
	reader := &geminiSubscriptionBody{source: body, reader: bufio.NewReader(body), streaming: true}
	raw, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(raw), "1790000000000000001") || !strings.Contains(string(raw), `"thoughtSignature":"signed"`) {
		t.Fatal("lost opaque tool fields")
	}
	if _, err := aggregateGeminiSubscription(strings.NewReader(strings.ReplaceAll(source, `,"finishReason":"STOP"`, ""))); err == nil {
		t.Fatal("accepted incomplete stream")
	}
}

func TestSubscriptionRefreshDispatchesNonOpenAIToNativeProxy(t *testing.T) {
	a := testApp(t)
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.anthropic.com" || r.URL.Path != "/v1/messages" {
			t.Fatalf("wrong subscription upstream %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":2,"output_tokens":1}}`))}, nil
	})}
	p := provider{ID: 1, Platform: "anthropic", Type: "oauth", APIFormat: "anthropic_messages", BaseURL: "https://api.anthropic.com", AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour)}
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`))
	r = r.WithContext(context.WithValue(r.Context(), gatewayProviderRequestKey{}, p))
	w := httptest.NewRecorder()
	a.gateway(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("status=%d %s", w.Code, w.Body)
	}
}

func TestDatabaseSubscriptionRoutingDecodesCredentialsWithoutAPIKeyFallback(t *testing.T) {
	for _, platform := range []string{"openai", "anthropic", "gemini", "antigravity"} {
		for _, hasModels := range []bool{true, false} {
			t.Run(platform+map[bool]string{true: "_configured", false: "_no_models"}[hasModels], func(t *testing.T) {
				sqlDB, mock, err := sqlmock.New()
				if err != nil {
					t.Fatal(err)
				}
				defer sqlDB.Close()
				db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
				if err != nil {
					t.Fatal(err)
				}
				a := testApp(t)
				a.database = db
				cfg, _ := subscription.Configuration(platform)
				raw, _ := json.Marshal(subscription.Credentials{AccessToken: "account-access", RefreshToken: "account-refresh", AccountID: "account-id", ProjectID: "project", ExpiresAt: time.Now().Add(time.Hour)})
				cipher, _ := admininfra.NewCredentialCipher(a.encryptionKey)
				encrypted, err := cipher.Encrypt(string(raw))
				if err != nil {
					t.Fatal(err)
				}
				mock.ExpectQuery("SELECT \\* FROM `accounts`").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "platform", "account_type", "api_format", "base_url", "credentials_ciphertext"}).AddRow(1, "subscription", platform, "oauth", cfg.APIFormat, cfg.BaseURL, []byte(encrypted)))
				mock.ExpectQuery("SELECT count\\(\\*\\) FROM `organization_provider_bindings`").WithArgs(11).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
				mock.ExpectQuery("SELECT account_id, priority, model_allowlist FROM `organization_provider_bindings`").WithArgs(11, "active").WillReturnRows(sqlmock.NewRows([]string{"account_id", "priority", "model_allowlist"}).AddRow(1, 0, `[]`))
				rows := sqlmock.NewRows([]string{"model_name", "price_input_per_1m", "price_output_per_1m"})
				if hasModels {
					rows.AddRow("model", 0, 0)
				}
				mock.ExpectQuery("SELECT model_name, price_input_per_1m.*FROM `provider_models`").WithArgs(1, "active").WillReturnRows(rows)
				if hasModels {
					mock.ExpectQuery("SELECT status, allowed_provider_ids, allowed_models FROM `organizations`").WithArgs(11, 1).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))
					mock.ExpectQuery("SELECT count.*FROM `organization_provider_bindings`").WithArgs(11).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
					mock.ExpectQuery("SELECT .* FROM `organization_provider_bindings`").WithArgs(11, 1, "active", 1).WillReturnRows(sqlmock.NewRows([]string{"organization_id", "account_id", "status", "model_allowlist"}).AddRow(11, 1, "active", `[]`))
				}
				selected, ok := a.providerForKeyModelFormat(context.Background(), apiKey{OrgID: 11}, "model", cfg.APIFormat)
				if !hasModels && ok {
					t.Fatal("a saved account without configured models must not receive requests")
				}
				if hasModels && (!ok || selected.Type != "oauth" || selected.APIKey != "" || selected.AccessToken != "account-access" || selected.AccountID != "account-id" || selected.ProjectID != "project") {
					t.Fatal("subscription was not selected with its own credential")
				}
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
