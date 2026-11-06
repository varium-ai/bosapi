package interfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
)

func TestEmbeddingsForwardToAPIKeyProvider(t *testing.T) {
	a := testApp(t)
	key := "sk-gw-" + random(32)
	a.data.Providers = []provider{{ID: 1, Type: "api_key", Platform: "openai", Status: "active", BaseURL: "https://api.openai.com/v1", APIKey: "upstream-secret"}}
	a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.openai.com/v1/embeddings" || r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Fatal("wrong upstream request")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}, nil
	})}
	r := httptest.NewRequest("POST", "/v1/embeddings", strings.NewReader(`{"model":"text-embedding-3-small","input":"hello"}`))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestPromptSecretsAreMaskedBeforeUpstreamForwarding(t *testing.T) {
	a := testApp(t)
	key := "sk-gw-" + random(32)
	a.data.Providers = []provider{{ID: 1, Type: "api_key", Platform: "openai", Status: "active", BaseURL: "https://api.openai.com/v1", APIKey: "upstream-secret"}}
	a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}
	a.background.SecurityEvents = gatewayinfra.RuleSecurityEventSink{}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte("hunter2")) || !bytes.Contains(body, []byte("[Password Hide]")) {
			t.Fatalf("upstream body was not sanitized: %s", body)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}, nil
	})}
	r := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"text-embedding-3-small","input":"password=hunter2"}`))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAPIKeyProxyCapturesJSONUsage(t *testing.T) {
	a := testApp(t)
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`))}, nil
	})}
	state := &usageMetricsState{}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test-model"}`))
	r = r.WithContext(context.WithValue(r.Context(), gatewayUsageStateContextKey{}, state))
	w := httptest.NewRecorder()
	a.proxyAPI(w, r, provider{BaseURL: "https://api.openai.com/v1"}, "/chat/completions")
	if !state.metrics.Known || state.metrics.InputTokens != 11 || state.metrics.OutputTokens != 7 || state.metrics.TotalTokens != 18 {
		t.Fatalf("captured metrics = %+v", state.metrics)
	}
}

func TestAPIKeyProxyCapturesStreamUsage(t *testing.T) {
	a := testApp(t)
	stream := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":13,\"output_tokens\":5,\"total_tokens\":18}}}\n\ndata: [DONE]\n\n"
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
	})}
	state := &usageMetricsState{}
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"test-model","stream":true}`))
	r = r.WithContext(context.WithValue(r.Context(), gatewayUsageStateContextKey{}, state))
	w := httptest.NewRecorder()
	a.proxyAPI(w, r, provider{BaseURL: "https://api.openai.com/v1"}, "/responses")
	if !state.metrics.Known || state.metrics.InputTokens != 13 || state.metrics.OutputTokens != 5 || state.metrics.TotalTokens != 18 {
		t.Fatalf("captured metrics = %+v", state.metrics)
	}
	if w.Body.String() != stream {
		t.Fatalf("stream response changed: %q", w.Body.String())
	}
}

func TestAPIKeyProxyAuditsOutputAndPreservesStream(t *testing.T) {
	var events []SecurityEvent
	a := testApp(t)
	a.background.SecurityEvents = &recordingSecurityEventSink{events: &events}
	stream := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<script\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\">bad()\"}\n\n"
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
	})}
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"test-model","stream":true}`))
	r.Header.Set("X-Request-ID", "req_test")
	r = r.WithContext(context.WithValue(r.Context(), gatewayAPIKeyRequestKey{}, apiKey{ID: 7}))
	w := httptest.NewRecorder()
	a.proxyAPI(w, r, provider{BaseURL: "https://api.openai.com/v1"}, "/responses")
	if w.Body.String() != stream {
		t.Fatalf("response changed: %q", w.Body.String())
	}
	if len(events) != 1 || events[0].RuleID != "model_output_code" || events[0].Action != "observe" || events[0].APIKeyID != 7 {
		t.Fatalf("events=%+v", events)
	}
}

type recordingSecurityEventSink struct{ events *[]SecurityEvent }

func (sink *recordingSecurityEventSink) Publish(_ context.Context, event SecurityEvent) error {
	*sink.events = append(*sink.events, event)
	return nil
}

func (*recordingSecurityEventSink) Inspect(context.Context, SecurityInspection) (bool, error) {
	return true, nil
}

func (*recordingSecurityEventSink) InspectResult(context.Context, SecurityInspection) (SecurityInspectionResult, error) {
	return SecurityInspectionResult{Allowed: true, Action: "allow"}, nil
}

func (*recordingSecurityEventSink) InspectContent(ctx context.Context, kind string, payload []byte) (SecurityInspectionResult, error) {
	return (gatewayinfra.RuleSecurityEventSink{}).InspectContent(ctx, kind, payload)
}

func TestSSEUsageAccumulatesAcrossChunksAndKeepsTerminalUsage(t *testing.T) {
	state := &usageMetricsState{}
	observeSSEUsage(state, []byte("data: {\"type\":\"response.output_text.delta\",\"response\":{\"usage\":{\"input_tokens\":13,\"output_tokens\":5"), false)
	observeSSEUsage(state, []byte(",\"total_tokens\":18}}}\n\n"), false)
	observeSSEUsage(state, []byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"), true)
	if !state.metrics.Known || state.metrics.InputTokens != 13 || state.metrics.OutputTokens != 5 || state.metrics.TotalTokens != 18 {
		t.Fatalf("accumulated metrics = %+v", state.metrics)
	}
}

func TestWebSocketUsageCapturesNestedUsageAndModel(t *testing.T) {
	state := &usageMetricsState{metrics: usageMetrics{Realtime: true}}
	observeWebSocketModel(state, []byte(`{"type":"response.create","response":{"model":"gpt-realtime"}}`))
	observeWebSocketUsage(state, []byte(`{"type":"response.done","response":{"usage":{"input_tokens":21,"output_tokens":9}}}`))
	observeWebSocketUsage(state, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":4,"output_tokens":2}}}`))
	if state.model != "gpt-realtime" {
		t.Fatalf("model = %q", state.model)
	}
	if !state.metrics.Known || !state.metrics.Realtime || state.metrics.InputTokens != 25 || state.metrics.OutputTokens != 11 || state.metrics.TotalTokens != 36 {
		t.Fatalf("websocket metrics = %+v", state.metrics)
	}
}

func TestWebSocketTerminalWithoutUsageFlushesCurrentTurn(t *testing.T) {
	state := &usageMetricsState{metrics: usageMetrics{Realtime: true}}
	observeWebSocketUsage(state, []byte(`{"type":"response.output_text.done","usage":{"input_tokens":8,"output_tokens":3}}`))
	observeWebSocketUsage(state, []byte(`{"type":"response.completed"}`))
	if state.metrics.InputTokens != 8 || state.metrics.OutputTokens != 3 || state.metrics.TotalTokens != 11 || usageHasTokens(state.realtimeTurn) {
		t.Fatalf("flushed metrics = %+v, pending turn = %+v", state.metrics, state.realtimeTurn)
	}
}

func TestCompactReturnsCompactionItem(t *testing.T) {
	a := testApp(t)
	key := "sk-gw-" + random(32)
	a.data.Providers = []provider{{ID: 1, Type: "oauth", Status: "active", AccessToken: "token", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour)}}
	a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&payload) != nil || !bytes.Contains(payload["input"], []byte("compaction_trigger")) {
			t.Fatal("compact trigger missing")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"compaction\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[]}}\n\n"))}, nil
	})}
	r := httptest.NewRequest("POST", "/v1/responses/compact", strings.NewReader(`{"model":"gpt-test","input":[{"role":"user","content":"hello"}]}`))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "compaction") {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}
