package interfaces

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestAntigravityGenerationAcrossGatewayPipeline(t *testing.T) {
	for _, operation := range []string{"generateContent", "streamGenerateContent"} {
		t.Run(operation, func(t *testing.T) {
			a, key := geminiTestApp(t)
			p := &a.data.Providers[0]
			p.Type, p.Platform, p.BaseURL = "oauth", "antigravity", "https://cloudcode-pa.googleapis.com"
			p.APIKey, p.AccessToken, p.RefreshToken, p.ProjectID, p.ExpiresAt = "", "account-access", "account-refresh", "project", time.Now().Add(time.Hour)
			called := false
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				var body struct{ Model, Project string }
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "actual" || body.Project != "project" || r.Header.Get("Authorization") != "Bearer account-access" || r.URL.String() != "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse" {
					t.Fatal("gateway did not route the subscription model to Antigravity")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":1}}}\n\n"))}, nil
			})}
			r := httptest.NewRequest(http.MethodPost, "/v1beta/models/alias:"+operation, strings.NewReader(`{"contents":[{"parts":[{"text":"hello"}]}]}`))
			r.Header.Set("x-goog-api-key", key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 200 || !called || !strings.Contains(w.Body.String(), "answer") || strings.Contains(w.Body.String(), `"response":`) {
				t.Fatalf("gateway response: %d %s", w.Code, w.Body)
			}
			if operation == "generateContent" && !json.Valid(w.Body.Bytes()) {
				t.Fatal("non-streaming generation returned SSE")
			}
			if operation == "streamGenerateContent" && !strings.HasPrefix(w.Body.String(), "data:") {
				t.Fatal("streaming generation returned JSON")
			}
			select {
			case event := <-a.usageQueue:
				if event.Model != "alias" || event.Metrics.TotalTokens != 3 || event.IsStream != (operation == "streamGenerateContent") {
					t.Fatal("subscription usage attribution was lost")
				}
			default:
				t.Fatal("subscription usage was not recorded")
			}
		})
	}
}

func TestAntigravityEnvelopePreservesCallerInstructionsAndOpaqueFields(t *testing.T) {
	p := provider{Type: "oauth", Platform: "antigravity", ProjectID: "project"}
	payload := []byte(`{"model":"ignored","systemInstruction":{"role":"user","parts":[{"text":"Follow my instructions"},{"thoughtSignature":"signed","opaque":1790000000000000001}]},"contents":[{"parts":[{"functionCall":{"name":"read","args":{"id":1790000000000000001}},"thoughtSignature":"tool-signed"}]}],"generationConfig":{"temperature":0.2}}`)
	_, raw, err := geminiSubscriptionRequest(p, "models/model-test:generateContent", payload)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Model     string                     `json:"model"`
		RequestID string                     `json:"requestId"`
		Request   map[string]json.RawMessage `json:"request"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Model != "model-test" || len(envelope.Request["model"]) != 0 || !regexp.MustCompile(`^agent-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(envelope.RequestID) {
		t.Fatal("invalid agent request identity or model")
	}
	for _, retained := range []string{`"role":"user"`, "Follow my instructions", `"opaque":1790000000000000001`, `"id":1790000000000000001`, `"thoughtSignature":"tool-signed"`, `"temperature":0.2`} {
		if !strings.Contains(string(raw), retained) {
			t.Fatalf("lost caller field %s", retained)
		}
	}
	if strings.Count(string(raw), "You are Antigravity") != 1 {
		t.Fatal("missing or duplicated identity")
	}
	_, second, err := geminiSubscriptionRequest(p, "models/model-test:generateContent", payload)
	if err != nil || string(second) == string(raw) {
		t.Fatal("request IDs must be unique")
	}
	request, _ := json.Marshal(envelope.Request)
	_, repeated, err := geminiSubscriptionRequest(p, "models/model-test:generateContent", request)
	if err != nil || strings.Count(string(repeated), "You are Antigravity") != 1 {
		t.Fatal("identity was duplicated when wrapping an existing instruction")
	}
}

func TestAntigravityRejectsTokenCountingWithoutCallingUpstream(t *testing.T) {
	a := testApp(t)
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported token counting reached upstream")
		return nil, nil
	})}
	p := provider{Type: "oauth", Platform: "antigravity", APIFormat: "gemini", ProjectID: "project"}
	r := httptest.NewRequest(http.MethodPost, "/v1beta/models/model-test:countTokens", strings.NewReader(`{"contents":[]}`))
	w := httptest.NewRecorder()
	a.nativeAPI(w, r, p, "models/model-test:countTokens")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "provider_capability_unavailable") {
		t.Fatalf("status=%d %s", w.Code, w.Body)
	}
	if _, _, err := geminiSubscriptionRequest(p, "models/model-test:countTokens", []byte(`{}`)); err == nil {
		t.Fatal("unsupported token counting was accepted by relay")
	}
	if _, _, err := geminiSubscriptionRequest(p, "models/model-test:generateContent", []byte(`{"systemInstruction":[]}`)); err == nil {
		t.Fatal("malformed system instruction accepted")
	}
}

func TestAntigravityCatalogAdvertisesOnlyImplementedOperations(t *testing.T) {
	a := testApp(t)
	p := provider{Type: "oauth", Platform: "antigravity", APIFormat: "gemini", Models: []string{"model-test"}}
	w := httptest.NewRecorder()
	a.geminiModels(w, httptest.NewRequest(http.MethodGet, "/v1beta/models", nil), p, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "countTokens") || !strings.Contains(w.Body.String(), "generateContent") || !strings.Contains(w.Body.String(), "streamGenerateContent") {
		t.Fatalf("unsupported catalog operations: %d %s", w.Code, w.Body)
	}
}
