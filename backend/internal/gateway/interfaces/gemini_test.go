package interfaces

import (
	"bytes"
	"context"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adminDomain "bosapi/backend/internal/admin/domain"
	admininfra "bosapi/backend/internal/admin/infrastructure"
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type geminiMetadataLimiter struct {
	keys   []RateLimitKey
	denied bool
}

func (limiter *geminiMetadataLimiter) Allow(context.Context, RateLimitKey, time.Time) (bool, error) {
	return true, nil
}
func (limiter *geminiMetadataLimiter) Record(context.Context, RateLimitKey, time.Time) error {
	return nil
}
func (limiter *geminiMetadataLimiter) Decide(_ context.Context, key RateLimitKey, _ time.Time) (RateLimitDecision, error) {
	limiter.keys = append(limiter.keys, key)
	return RateLimitDecision{Allowed: !limiter.denied, RetryAfter: time.Second}, nil
}

type geminiInspectionSink struct{ inspections []SecurityInspection }

func (*geminiInspectionSink) Publish(context.Context, SecurityEvent) error { return nil }
func (sink *geminiInspectionSink) Inspect(_ context.Context, inspection SecurityInspection) (bool, error) {
	sink.inspections = append(sink.inspections, inspection)
	return true, nil
}

func geminiTestApp(t *testing.T) (*app, string) {
	t.Helper()
	a := testApp(t)
	key := "sk-gw-" + random(32)
	a.data.Providers = []provider{{ID: 1, Type: "api_key", APIFormat: "gemini", Platform: "gemini", Status: "active", APIKey: "provider-key", BaseURL: "https://upstream.example", Models: []string{"actual"}, ModelMapping: map[string]string{"alias": "actual"}, Pricing: map[string]any{"models": map[string]any{"actual": map[string]any{"input": 2.0, "output": 4.0, "cache_read": 0.5}, "spoof": map[string]any{"input": 100.0}}}}}
	a.data.Keys = []apiKey{{ID: 7, OrgID: 9, Hash: digest(key), Status: "active", ProviderID: 1}}
	a.usageQueue = make(chan usageEvent, 8)
	return a, key
}

func TestGeminiURLModelAcrossGatewayPipeline(t *testing.T) {
	for _, operation := range []string{"generateContent", "streamGenerateContent", "countTokens", "embedContent", "batchEmbedContents"} {
		t.Run(operation, func(t *testing.T) {
			a, key := geminiTestApp(t)
			limiter := &geminiMetadataLimiter{}
			sink := &geminiInspectionSink{}
			a.background.RateLimiter = limiter
			a.background.SecurityEvents = sink
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			body := `{"model":"body-spoof","stream":true,"contents":[{"parts":[{"text":"hello"}]}]}`
			if operation == "streamGenerateContent" {
				body = strings.Replace(body, `"stream":true`, `"stream":false`, 1)
			}
			response := `{"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":5,"thoughtsTokenCount":3,"cachedContentTokenCount":5}}`
			contentType := "application/json"
			if operation == "streamGenerateContent" {
				response = "data: " + response + "\n\n"
				contentType = "text/event-stream"
			}
			if operation == "countTokens" {
				response = `{"totalTokens":20}`
			}
			called := false
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				raw, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/v1beta/models/actual:"+operation || string(raw) != body || r.URL.Query().Has("model") || r.URL.Query().Has("key") {
					t.Fatalf("upstream routing: %s %s", r.URL, raw)
				}
				if r.Header.Get("x-goog-api-key") != "provider-key" {
					t.Fatal("wrong credential")
				}
				if operation == "streamGenerateContent" && r.URL.Query().Get("alt") != "sse" {
					t.Fatal("missing SSE option")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			path := "/v1beta/models/alias:" + operation
			r := httptest.NewRequest("POST", path+"?model=query-spoof&key=client-query-key", strings.NewReader(body))
			r.Header.Set("x-goog-api-key", key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 200 || !called {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if len(limiter.keys) != 1 || limiter.keys[0].Model != "alias" || limiter.keys[0].Operation != operation || limiter.keys[0].Route != "models/alias:"+operation {
				t.Fatalf("limiter: %+v", limiter.keys)
			}
			if len(sink.inspections) != 1 || sink.inspections[0].Model != "alias" || sink.inspections[0].Operation != operation {
				t.Fatalf("security audit: %+v", sink.inspections)
			}
			if !strings.Contains(logs.String(), "model=alias operation="+operation) || !strings.Contains(logs.String(), "endpoint=models/:model:"+operation) {
				t.Fatalf("missing audit metadata: %s", logs.String())
			}
			select {
			case event := <-a.usageQueue:
				if event.Model != "alias" || event.Endpoint != "models/alias:"+operation || event.IsStream != (operation == "streamGenerateContent") {
					t.Fatalf("usage attribution: %+v", event)
				}
				if operation != "countTokens" && (event.Metrics.TotalTokens != 28 || math.Abs(event.Metrics.Cost-64.5/1_000_000) > 1e-12) {
					t.Fatalf("wrong model pricing: %+v", event.Metrics)
				}
				if operation == "countTokens" && (event.Metrics.Known || event.Metrics.Cost != 0) {
					t.Fatal("count request billed as generation")
				}
				if operation != "countTokens" && (a.data.Keys[0].QuotaUsedTokens != 28 || math.Abs(a.data.Keys[0].QuotaUsedAmount-64.5/1_000_000) > 1e-12) {
					t.Fatalf("quota settlement skipped: %+v", a.data.Keys[0])
				}
				a.processUsageEvent(event)
				stats := a.data.Usage[event.KeyHash].Models
				if len(stats) != 1 || stats["alias"].Requests != 1 {
					t.Fatalf("persisted audit model: %+v", stats)
				}
			default:
				t.Fatal("missing usage event")
			}
		})
	}
}

func TestGeminiRejectsMalformedPathsBeforeBodyModelFallback(t *testing.T) {
	for _, path := range []string{"/v1beta/models", "/v1beta/models/", "/v1beta/models/:generateContent", "/v1beta/models/alias", "/v1beta/models/alias:unknown", "/v1beta/models/alias:generateContent/", "/v1beta/models/folder/alias:generateContent", "/v1beta/models/..:generateContent", "/v1beta/models/alias:generateContent:countTokens", "/v1beta/models/alias%20name:generateContent"} {
		t.Run(path, func(t *testing.T) {
			a, key := geminiTestApp(t)
			limiter := &geminiMetadataLimiter{}
			sink := &geminiInspectionSink{}
			a.background.RateLimiter = limiter
			a.background.SecurityEvents = sink
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("invalid path sent upstream"); return nil, nil })}
			r := httptest.NewRequest("POST", path+"?model=alias", strings.NewReader(`{"model":"alias","contents":[]}`))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 400 || len(limiter.keys) != 0 || len(sink.inspections) != 0 {
				t.Fatalf("invalid path admitted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestGeminiPermissionsAndRateLimitsUseURLModel(t *testing.T) {
	for _, scenario := range []string{"forbidden", "rate_limited"} {
		t.Run(scenario, func(t *testing.T) {
			a, key := geminiTestApp(t)
			limiter := &geminiMetadataLimiter{denied: scenario == "rate_limited"}
			a.background.RateLimiter = limiter
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("rejected request forwarded"); return nil, nil })}
			model := "alias"
			want := 429
			if scenario == "forbidden" {
				model = "unauthorized"
				want = 403
			}
			r := httptest.NewRequest("POST", "/v1beta/models/"+model+":generateContent?model=alias", strings.NewReader(`{"model":"alias","contents":[]}`))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if scenario == "rate_limited" && (len(limiter.keys) != 1 || limiter.keys[0].Model != "alias" || w.Header().Get("Retry-After") != "1") {
				t.Fatalf("limiter: %+v", limiter.keys)
			}
			if scenario == "forbidden" && len(limiter.keys) != 0 {
				t.Fatal("unauthorized model consumed rate admission")
			}
		})
	}
}

func TestGeminiSameModelNameChoosesNativeProvider(t *testing.T) {
	a, key := geminiTestApp(t)
	wrong := provider{ID: 2, Type: "api_key", Status: "active", APIFormat: "openai_chat", Models: []string{"alias"}, APIKey: "wrong-key", BaseURL: "https://openai.example"}
	a.data.Providers = append([]provider{wrong}, a.data.Providers...)
	selected, ok := a.providerForKeyModelFormat(context.Background(), apiKey{}, "alias", "gemini")
	if !ok || selected.ID != 1 || selected.APIFormat != "gemini" {
		t.Fatalf("wrong protocol provider: %+v", selected)
	}
	// A key explicitly bound to an OpenAI provider cannot cross that binding to
	// reach a Gemini provider, even when both advertise the requested model.
	a.data.Keys[0].ProviderID = 2
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("Gemini request sent to wrong protocol provider")
		return nil, nil
	})}
	r := httptest.NewRequest("POST", "/v1beta/models/alias:generateContent", strings.NewReader(`{"contents":[]}`))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestGeminiModelAndOperationDoNotSplitKeyQuota(t *testing.T) {
	a, key := geminiTestApp(t)
	a.data.Providers[0].Models = []string{"alias", "second"}
	a.data.Providers[0].ModelMapping = nil
	a.background.RateLimiter = gatewayinfra.NewLocalRateLimiter(gatewayRateLimitRepository{config: adminDomain.RateLimitUpdate{Keys: []adminDomain.KeyRateLimit{{KeyID: 7, RateLimitConfig: adminDomain.RateLimitConfig{Quota5H: int64Ptr(1)}}}}})
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	handler := a.routes()
	for index, path := range []string{"/v1beta/models/alias:generateContent", "/v1beta/models/second:countTokens"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"contents":[]}`))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := 200
		if index == 1 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("key quota bypass: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestGeminiDatabaseSelectionKeepsProtocolAndBindingModel(t *testing.T) {
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
	cipher, err := admininfra.NewCredentialCipher(a.encryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := cipher.Encrypt("provider-key")
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT \\* FROM `accounts`").WithArgs("active", true, 9, "active").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "platform", "api_format", "base_url", "credentials_ciphertext", "model_mapping_json"}).
		AddRow(2, "OpenAI same alias", "openai", "openai_chat", "https://wrong.example", []byte(credential), `{}`).
		AddRow(1, "Gemini", "gemini", "gemini", "https://upstream.example", []byte(credential), `{"models/alias":"models/actual"}`))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `organization_provider_bindings`").WithArgs(9).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT account_id, priority, model_allowlist FROM `organization_provider_bindings`").WithArgs(9, "active").WillReturnRows(sqlmock.NewRows([]string{"account_id", "priority", "model_allowlist"}).AddRow(2, 10, `["alias"]`).AddRow(1, 9, `["models/alias"]`))
	mock.ExpectQuery("SELECT model_name, price_input_per_1m, price_output_per_1m, price_cache_read_per_1m, price_cache_write_per_1m FROM `provider_models`").WithArgs(1, "active").WillReturnRows(sqlmock.NewRows([]string{"model_name", "price_input_per_1m", "price_output_per_1m", "price_cache_read_per_1m", "price_cache_write_per_1m"}).AddRow("models/actual", 2, 4, 0.5, 0))
	mock.ExpectQuery("SELECT status, allowed_provider_ids, allowed_models FROM `organizations`").WithArgs(9, 1).WillReturnRows(sqlmock.NewRows([]string{"status", "allowed_provider_ids", "allowed_models"}).AddRow("active", `[2]`, `["spoof"]`))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `organization_provider_bindings`").WithArgs(9).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT \\* FROM `organization_provider_bindings`").WithArgs(9, 1, "active", 1).WillReturnRows(sqlmock.NewRows([]string{"organization_id", "account_id", "model_allowlist", "status"}).AddRow(9, 1, `["models/alias"]`, "active"))
	selected, ok := a.providerForKeyModelFormat(context.Background(), apiKey{OrgID: 9}, "alias", "gemini")
	if !ok || selected.ID != 1 || selected.APIFormat != "gemini" || selected.ModelMapping["alias"] != "actual" {
		t.Fatalf("selected: %+v ok=%v expectations=%v", selected, ok, mock.ExpectationsWereMet())
	}
	// Authorizing an alias must not implicitly authorize its upstream ID.
	mock.ExpectQuery("SELECT status, allowed_provider_ids, allowed_models FROM `organizations`").WithArgs(9, 1).WillReturnRows(sqlmock.NewRows([]string{"status", "allowed_provider_ids", "allowed_models"}).AddRow("active", `[2]`, `["spoof"]`))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `organization_provider_bindings`").WithArgs(9).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT \\* FROM `organization_provider_bindings`").WithArgs(9, 1, "active", 1).WillReturnRows(sqlmock.NewRows([]string{"organization_id", "account_id", "model_allowlist", "status"}).AddRow(9, 1, `["alias"]`, "active"))
	if got := a.databasePolicyDenial(context.Background(), apiKey{OrgID: 9}, 1, "actual"); got != errorcode.ModelNotAllowed {
		t.Fatalf("alias binding permitted upstream ID: %s", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGeminiRejectsInvalidMappingTarget(t *testing.T) {
	a, key := geminiTestApp(t)
	a.data.Providers[0].Models = []string{"alias"}
	a.data.Providers[0].ModelMapping["alias"] = "../spoof"
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid model mapping sent upstream")
		return nil, nil
	})}
	r := httptest.NewRequest("POST", "/v1beta/models/alias:generateContent", strings.NewReader(`{"contents":[]}`))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 502 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
