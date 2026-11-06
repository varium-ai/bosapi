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
)

func TestNativeAPIKeyForwarding(t *testing.T) {
	cases := []struct{ name, format, path, body, endpoint, response, contentType string }{
		{"anthropic", "anthropic_messages", "/v1/messages?beta=true", `{"model":"claude-test","system":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}],"thinking":{"type":"enabled","budget_tokens":1000},"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hi"}],"max_tokens":2000,"custom":{"id":9007199254740993}}`, "https://upstream.example/v1/messages?beta=true", `{"id":"msg","type":"message","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":12,"output_tokens":7}}`, "application/json"},
		{"anthropic stream", "anthropic_messages", "/v1/messages", `{"model":"claude-test","messages":[{"role":"user","content":"hi"}],"max_tokens":2000,"stream":true}`, "https://upstream.example/v1/messages", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":12,\"output_tokens\":0}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "text/event-stream"},
		{"gemini", "gemini", "/v1beta/models/gemini-test:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"systemInstruction":{"parts":[{"text":"hello"}]},"generationConfig":{"thinkingConfig":{"thinkingBudget":1000}},"custom":{"id":9007199254740993}}`, "https://upstream.example/v1beta/models/gemini-test:generateContent", `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":5,"thoughtsTokenCount":2,"totalTokenCount":19}}`, "application/json"},
		{"gemini stream", "gemini", "/v1beta/models/gemini-test:streamGenerateContent?key=client-secret", `{"contents":[{"parts":[{"text":"hi"}]}]}`, "https://upstream.example/v1beta/models/gemini-test:streamGenerateContent?alt=sse", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]}}]}\n\ndata: {\"usageMetadata\":{\"promptTokenCount\":12,\"candidatesTokenCount\":7}}\n\n", "text/event-stream"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			key := "sk-gw-" + random(32)
			a.data.Providers = []provider{{ID: 1, Type: "api_key", Status: "active", APIFormat: tc.format, BaseURL: "https://upstream.example", APIKey: "provider-secret"}}
			a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				if string(raw) != tc.body || r.URL.String() != tc.endpoint {
					t.Fatalf("request changed: %s %s", r.URL, raw)
				}
				if r.Header.Get("Authorization") != "" {
					t.Fatal("client authorization forwarded")
				}
				if tc.format == "anthropic_messages" {
					if r.Header.Get("x-api-key") != "provider-secret" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("anthropic-beta") != "feature-a,feature-b" {
						t.Fatalf("headers: %v", r.Header)
					}
				} else if r.Header.Get("x-goog-api-key") != "provider-secret" {
					t.Fatal("missing Gemini credential")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.response))}, nil
			})}
			r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			r.Header.Set("x-goog-api-key", key)
			r.Header.Add("anthropic-beta", "feature-a")
			r.Header.Add("anthropic-beta", "feature-b")
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if strings.Contains(tc.contentType, "event-stream") && !w.Flushed {
				t.Fatal("stream was not flushed")
			}
			if !strings.Contains(w.Body.String(), "hi") && !strings.Contains(w.Body.String(), "hello") {
				t.Fatalf("response missing: %s", w.Body.String())
			}
		})
	}
}

func TestNativeUsageAndModelMapping(t *testing.T) {
	state := &usageMetricsState{}
	observeSSEUsage(state, []byte("event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":12,\"cache_read_input_tokens\":8,\"cache_creation_input_tokens\":3}}}\n\nevent: message_delta\ndata: {\"usage\":{\"output_tokens\":7}}\n\n"), true)
	if state.metrics.InputTokens != 23 || state.metrics.OutputTokens != 7 || state.metrics.TotalTokens != 30 {
		t.Fatalf("split usage: %+v", state.metrics)
	}
	metric := extractUsage(map[string]any{"usageMetadata": map[string]any{"promptTokenCount": float64(12), "candidatesTokenCount": float64(5), "thoughtsTokenCount": float64(2)}})
	if metric.TotalTokens != 19 {
		t.Fatalf("Gemini usage: %+v", metric)
	}
	raw := rewriteModelInJSON([]byte(`{"model":"alias","id":9007199254740993,"extra":true}`), map[string]string{"alias": "actual"})
	if !strings.Contains(string(raw), "9007199254740993") {
		t.Fatalf("integer precision lost: %s", raw)
	}
	r := httptest.NewRequest("POST", "/v1beta/models/alias:generateContent?model=bypass", nil)
	if requestModel(r) != "alias" {
		t.Fatal("model must come from Gemini path")
	}
}

func TestNativeErrorsAndUnsupportedProtocol(t *testing.T) {
	for _, format := range []string{"anthropic_messages", "gemini"} {
		t.Run(format, func(t *testing.T) {
			a := testApp(t)
			p := provider{APIFormat: format, BaseURL: "https://upstream.example", APIKey: "secret"}
			path := "messages"
			if format == "gemini" {
				path = "models/test:generateContent"
			}
			for _, status := range []int{400, 401, 429, 503} {
				a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"10"}}, Body: io.NopCloser(strings.NewReader(`{"error":"private upstream credential"}`))}, nil
				})}
				r := httptest.NewRequest("POST", "/"+path, strings.NewReader(`{}`))
				w := httptest.NewRecorder()
				a.nativeAPI(w, r, p, path)
				want := status
				if status == 401 || status == 503 {
					want = 502
				}
				if w.Code != want || strings.Contains(w.Body.String(), "private") {
					t.Fatalf("error %d: %d %s", status, w.Code, w.Body.String())
				}
				var result map[string]any
				if json.Unmarshal(w.Body.Bytes(), &result) != nil || result["error"] == nil {
					t.Fatal("missing native error")
				}
				if status == 429 && w.Header().Get("Retry-After") != "10" {
					t.Fatal("retry header missing")
				}
			}
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("unsupported protocol sent upstream")
				return nil, nil
			})}
			w := httptest.NewRecorder()
			a.nativeAPI(w, httptest.NewRequest("POST", "/chat/completions", strings.NewReader(`{}`)), p, "chat/completions")
			if w.Code != 400 {
				t.Fatal("expected explicit unsupported request")
			}
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })}
			w = httptest.NewRecorder()
			a.nativeAPI(w, httptest.NewRequest("POST", "/"+path, strings.NewReader(`{}`)), p, path)
			if w.Code != 504 {
				t.Fatalf("timeout status %d", w.Code)
			}
		})
	}
}

func TestNativeModelMappingAndPermissions(t *testing.T) {
	for _, format := range []string{"anthropic_messages", "gemini"} {
		t.Run(format, func(t *testing.T) {
			a := testApp(t)
			key := "sk-gw-" + random(32)
			p := provider{ID: 1, Type: "api_key", Status: "active", APIFormat: format, BaseURL: "https://upstream.example", APIKey: "secret", Models: []string{"alias"}, ModelMapping: map[string]string{"alias": "actual"}}
			a.data.Providers = []provider{p}
			a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}
			path := "/v1/messages/count_tokens"
			body := `{"model":"alias","system":"keep","messages":[{"role":"user","content":"hi"}],"unknown":9007199254740993}`
			if format == "gemini" {
				path = "/v1beta/models/alias:countTokens"
				body = `{"contents":[{"parts":[{"text":"hi"}]}],"unknown":9007199254740993}`
			}
			called := 0
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called++
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), "9007199254740993") {
					t.Fatal("unknown field lost")
				}
				if format == "gemini" && r.URL.Path != "/v1beta/models/actual:countTokens" {
					t.Fatal(r.URL)
				}
				if format == "anthropic_messages" && (!strings.Contains(string(raw), `"model":"actual"`) || r.URL.Path != "/v1/messages/count_tokens") {
					t.Fatalf("%s %s", r.URL, raw)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"input_tokens":3,"totalTokens":3}`))}, nil
			})}
			r := httptest.NewRequest("POST", path, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 200 || called != 1 {
				t.Fatalf("%d %s called=%d", w.Code, w.Body.String(), called)
			}
			p.Models = []string{"other"}
			a.data.Providers = []provider{p}
			r = httptest.NewRequest("POST", path, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w = httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 403 || called != 1 {
				t.Fatalf("permission bypass: %d calls=%d", w.Code, called)
			}
		})
	}
}

func TestNativeCachePricing(t *testing.T) {
	p := provider{Pricing: map[string]any{"input": 2.0, "output": 10.0, "cache_read": 0.2, "cache_write": 2.5}}
	m := usageMetrics{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 60, CacheWriteTokens: 20}
	want := 202.0 / 1_000_000
	if got := providerCost(p, "model", m); got != want {
		t.Fatalf("cost %g", got)
	}
	breakdown := providerCostBreakdown(p, "model", m)
	if breakdown.CacheReadCost != 12.0/1_000_000 || breakdown.CacheWriteCost != 50.0/1_000_000 {
		t.Fatalf("cache costs %g/%g", breakdown.CacheReadCost, breakdown.CacheWriteCost)
	}
	if breakdown.InputCost != 40.0/1_000_000 || breakdown.OutputCost != 100.0/1_000_000 {
		t.Fatalf("regular costs %g/%g", breakdown.InputCost, breakdown.OutputCost)
	}
	if breakdown.TotalCost != want {
		t.Fatalf("total %g", breakdown.TotalCost)
	}
}

func TestNativeRateLimitEnvelopeAudit(t *testing.T) {
	for _, format := range []string{"anthropic_messages", "gemini"} {
		rec := httptest.NewRecorder()
		writeUpstreamLimitError(&nativeLimitWriter{ResponseWriter: rec, format: format}, &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"insufficient_quota"}}`))}, time.Now())
		var result map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &result)
		details := result["error"].(map[string]any)
		if details["reason"] != "upstream_quota" || details["retryable"] != false || rec.Header().Get("Retry-After") != "" {
			t.Fatalf("quota changed: %s", rec.Body.String())
		}
		if format == "gemini" && (details["code"] != float64(429) || details["bos_code"] != "quota_exceeded") {
			t.Fatalf("Gemini error: %s", rec.Body.String())
		}
	}
}
