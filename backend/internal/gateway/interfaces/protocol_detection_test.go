package interfaces

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const legacyInputError = `{"error":{"message":"239 validation errors: {'type': 'string_type', 'loc': 'body.input', 'msg': 'Input should be a valid string'}"}}`
const probeChatResponse = `{"id":"probe","choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`

func TestProtocolDetectionThroughGeminiGateway(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[streaming], func(t *testing.T) {
			a, key := geminiTestApp(t)
			a.data.Providers[0].APIFormat = "openai_responses"
			a.data.Providers[0].BaseURL = "https://upstream.example/prefix/v1/responses"
			calls := 0
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatal(err)
				}
				if body["model"] != "actual" || r.Header.Get("Authorization") != "Bearer provider-key" {
					t.Fatal("wrong model or auth")
				}
				status, kind, response := 200, "application/json", probeChatResponse
				if calls == 1 {
					if r.URL.Path != "/prefix/v1/responses" || strings.Contains(string(raw), "PRIVATE_HISTORY") {
						t.Fatal("invalid Responses probe")
					}
					status, response = 400, legacyInputError
				} else {
					if r.URL.Path != "/prefix/v1/chat/completions" {
						t.Fatalf("path=%s", r.URL.Path)
					}
					if calls == 2 && strings.Contains(string(raw), "PRIVATE_HISTORY") {
						t.Fatal("probe contained history")
					}
					if calls > 2 {
						if !strings.Contains(string(raw), `"reasoning_content":"PRIVATE_THOUGHT"`) || !strings.Contains(string(raw), "PRIVATE_HISTORY") {
							t.Fatalf("history lost: %s", raw)
						}
						if streaming {
							kind = "text/event-stream"
							response = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
						}
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			operation := "generateContent"
			if streaming {
				operation = "streamGenerateContent"
			}
			for i := 0; i < 2; i++ {
				r := httptest.NewRequest("POST", "/v1beta/models/alias:"+operation, strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"PRIVATE_HISTORY"}]},{"role":"model","parts":[{"text":"PRIVATE_THOUGHT","thought":true},{"text":"previous answer"}]},{"role":"user","parts":[{"text":"next"}]}]}`))
				r.Header.Set("x-goog-api-key", key)
				w := httptest.NewRecorder()
				a.routes().ServeHTTP(w, r)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"text":"OK"`) {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
			}
			if calls != 4 {
				t.Fatalf("expected two probes and two user requests, got %d", calls)
			}
			if a.data.Providers[0].Capabilities["protocol_detection"] == nil {
				t.Fatal("observation not persisted")
			}
		})
	}
}

func TestProtocolDetectionRejectsFailedChatProbe(t *testing.T) {
	for _, response := range []struct {
		status int
		body   string
	}{{401, `{"error":"denied"}`}, {200, `{"ok":true}`}, {400, legacyInputError}} {
		a, _ := geminiTestApp(t)
		p := a.data.Providers[0]
		p.APIFormat = "openai_responses"
		calls := 0
		a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			status, raw := 400, legacyInputError
			if calls == 2 {
				status, raw = response.status, response.body
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(raw)), Header: http.Header{}}, nil
		})}
		if got := a.detectProviderProtocol(context.Background(), p, "alias"); got.APIFormat != "openai_responses" {
			t.Fatal("failed probe changed protocol")
		}
		if a.data.Providers[0].Capabilities["protocol_detection"] != nil {
			t.Fatal("failed probe saved")
		}
	}
}

func TestProtocolDetectionCacheConcurrentAndInvalidation(t *testing.T) {
	a, _ := geminiTestApp(t)
	p := a.data.Providers[0]
	p.APIFormat = "openai_responses"
	var calls atomic.Int32
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		status, raw := 400, legacyInputError
		if strings.HasSuffix(r.URL.Path, "chat/completions") {
			status, raw = 200, probeChatResponse
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(raw)), Header: http.Header{}}, nil
	})}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := a.detectProviderProtocol(context.Background(), p, "alias"); got.APIFormat != "openai_chat" {
				t.Error("Chat not identified")
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 2 {
		t.Fatalf("concurrent probes=%d", calls.Load())
	}
	result := protocolDetection{APIFormat: "openai_chat", Model: "actual", Fingerprint: protocolFingerprint(p), CheckedAt: time.Now()}
	p.Capabilities = map[string]any{"protocol_detection": result}
	if effectiveProviderProtocol(p, "alias").APIFormat != "openai_chat" {
		t.Fatal("persisted result not applied")
	}
	if effectiveProviderProtocol(p, "other").APIFormat != "openai_responses" {
		t.Fatal("cross model leak")
	}
	expired := result
	expired.CheckedAt = time.Now().Add(-25 * time.Hour)
	p.Capabilities = map[string]any{"protocol_detection": expired}
	if effectiveProviderProtocol(p, "alias").APIFormat != "openai_responses" {
		t.Fatal("expired observation applied")
	}
	p.Capabilities = map[string]any{"protocol_detection": result}
	p.APIKey = "rotated"
	if effectiveProviderProtocol(p, "alias").APIFormat != "openai_responses" {
		t.Fatal("stale credential observation")
	}
}

func TestProtocolDetectionPersistsAtomicJSONField(t *testing.T) {
	sql, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sql, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	a := testApp(t)
	a.database = db
	p := provider{ID: 9, Type: "api_key", APIFormat: "openai_responses", BaseURL: "https://example.com/v1", ConfigurationVersion: time.Now().UTC()}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `accounts` SET `capabilities_json`=JSON_SET").WithArgs(sqlmock.AnyArg(), 9, p.BaseURL, p.APIFormat, p.ConfigurationVersion).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	a.persistProtocolDetection(context.Background(), p, protocolDetection{APIFormat: "openai_chat", CheckedAt: time.Now()})
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResponsesInputStringValidation(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{legacyInputError, true},
		{`{"detail":[{"type":"string_type","loc":["body","input"],"msg":"Input should be a valid string"}]}`, true},
		{`{"detail":[{"loc":["body","model"],"msg":"Input should be a valid string"}]}`, false},
		{`{"error":{"message":"invalid model"}}`, false},
	} {
		if got := responsesInputStringValidation([]byte(tc.raw)); got != tc.want {
			t.Fatalf("detected=%t for %s", got, tc.raw)
		}
	}
}

func TestProtocolDetectionLeavesStandardResponsesAndUnrelatedErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{200, `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`},
		{400, `{"error":{"message":"invalid model"}}`},
		{401, `{"error":{"message":"unauthorized"}}`},
	} {
		a, _ := geminiTestApp(t)
		p := a.data.Providers[0]
		p.APIFormat = "openai_responses"
		calls := 0
		a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}
		got := a.detectProviderProtocol(context.Background(), p, "alias")
		if got.APIFormat != "openai_responses" || calls != 1 {
			t.Fatalf("format=%s calls=%d", got.APIFormat, calls)
		}
	}
}
