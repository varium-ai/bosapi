package interfaces

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bosapi/backend/internal/gateway/domain"
)

func TestLegacyMessagesRejectsRatherThanLosingFields(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
		for _, stream := range []string{"false", "true"} {
			a, key := setupToolGateway(t, &toolRecordingSink{}, "{}", "application/json")
			a.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("unsupported conversion reached upstream")
				return nil, nil
			})
			r := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"test","system":"retain me","top_k":3,"stream":`+stream+`,"messages":[{"role":"user","content":"hello"}]}`))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_conversion") || !strings.Contains(w.Body.String(), `"type":"error"`) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		}
	}
}

func TestGeminiNativeDiscoveryMetadataAndPagination(t *testing.T) {
	a, key := geminiTestApp(t)
	calls := 0
	a.client = &http.Client{}
	a.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v1beta/models/actual" || r.URL.RawQuery != "" || r.Header.Get("x-goog-api-key") != "provider-key" {
			t.Fatalf("wrong discovery request: %s %v", r.URL, r.Header)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"name":"models/actual","version":"001","inputTokenLimit":1000,"outputTokenLimit":200,"supportedGenerationMethods":["generateContent","countTokens","createCachedContent"],"description":"native metadata"}`))}, nil
	})
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("x-goog-api-key", key)
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		return w
	}
	first := get("/v1beta/models?pageSize=1")
	var page struct {
		Models []map[string]any `json:"models"`
		Token  string           `json:"nextPageToken"`
	}
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &page) != nil || len(page.Models) != 1 || page.Token == "" || page.Models[0]["name"] != "models/actual" {
		t.Fatalf("%d %s", first.Code, first.Body.String())
	}
	second := get("/v1beta/models?pageSize=1&pageToken=" + page.Token)
	if second.Code != 200 || !strings.Contains(second.Body.String(), "models/alias") || strings.Contains(second.Body.String(), "createCachedContent") || strings.Contains(second.Body.String(), `"data"`) {
		t.Fatalf("%d %s", second.Code, second.Body.String())
	}
	single := get("/v1beta/models/alias")
	if single.Code != 200 || !strings.Contains(single.Body.String(), `"inputTokenLimit":1000`) || !strings.Contains(single.Body.String(), "native metadata") {
		t.Fatalf("%d %s", single.Code, single.Body.String())
	}
	for _, path := range []string{"/v1beta/models/spoof", "/v1beta/models?pageSize=0", "/v1beta/models?pageToken=bad"} {
		w := get(path)
		if w.Code < 400 {
			t.Fatalf("invalid discovery succeeded: %s", path)
		}
	}
	if calls != 3 {
		t.Fatalf("unexpected upstream requests: %d", calls)
	}
}

func TestGeminiNativeToolSecurityJSONAndSSE(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, denied := range []bool{false, true} {
			a, key := geminiTestApp(t)
			a.client = &http.Client{}
			sink := &toolRecordingSink{block: denied}
			a.background.SecurityEvents = sink
			// IDs and thought signatures are native wire fields and must survive an allowed call.
			response := `{"candidates":[{"content":{"role":"model","parts":[{"text":"ordinary text"},{"functionCall":{"name":"lookup","args":{"q":"PRIVATE_ARGUMENT"}},"thoughtSignature":"signed"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`
			kind := "application/json"
			operation := "generateContent"
			if streaming {
				kind = "text/event-stream"
				operation = "streamGenerateContent"
				response = "data: " + response + "\n\n"
			}
			a.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{kind}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})
			body := `{"contents":[{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"q":"old"}},"thoughtSignature":"old-signed"}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"result":"done"}}}]},{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}]}`
			r := httptest.NewRequest("POST", "/v1beta/models/alias:"+operation, strings.NewReader(body))
			r.Header.Set("x-goog-api-key", key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 200 || len(sink.calls) != 1 || sink.calls[0].Protocol != domain.ToolProtocolGemini || sink.calls[0].Name != "lookup" {
				t.Fatalf("stream=%v denied=%v status=%d calls=%+v body=%s", streaming, denied, w.Code, sink.calls, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "ordinary text") {
				t.Fatal("text lost")
			}
			if denied {
				if strings.Contains(w.Body.String(), "PRIVATE_ARGUMENT") || strings.Contains(w.Body.String(), "functionCall") || strings.Contains(w.Body.String(), "thoughtSignature") {
					t.Fatalf("unsafe tool delivered: %s", w.Body.String())
				}
			} else if w.Body.String() != response {
				t.Fatalf("allowed native bytes changed: %s", w.Body.String())
			}
			if len(sink.inspections) != 1 || len(sink.inspections[0].ToolCalls) != 2 || sink.inspections[0].ToolCalls[1].Name != "lookup" {
				t.Fatalf("history correlation lost: %+v", sink.inspections)
			}
			select {
			case event := <-a.usageQueue:
				if event.Metrics.TotalTokens != 15 {
					t.Fatalf("usage lost: %+v", event.Metrics)
				}
			default:
				t.Fatal("missing usage")
			}
			audit, _ := json.Marshal(sink.events)
			if strings.Contains(string(audit), "PRIVATE_ARGUMENT") {
				t.Fatal("arguments leaked into audit")
			}
		}
	}
}
