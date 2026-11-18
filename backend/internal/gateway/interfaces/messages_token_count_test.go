package interfaces

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessagesChatTokenCount(t *testing.T) {
	for _, format := range []string{"openai_chat", ""} {
		t.Run("format="+format, func(t *testing.T) {
			a, key := crossTestApp(t, format)
			a.data.Keys[0].QuotaTokens = int64Ptr(100000)
			a.data.Keys[0].QuotaAmount = float64Ptr(10)
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("local count must not call Chat or Responses upstream endpoints")
				return nil, nil
			})}
			count := func(body string) int {
				t.Helper()
				r := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens?beta=true", strings.NewReader(body))
				r.Header.Set("x-api-key", key)
				r.Header.Set("anthropic-version", "2023-06-01")
				r.Header.Set("anthropic-beta", "claude-code-20250219,thinking-token-count-2026-05-13,future-capability")
				r.Header.Set("anthropic-dangerous-direct-browser-access", "true")
				w := httptest.NewRecorder()
				a.routes().ServeHTTP(w, r)
				var result struct {
					InputTokens int `json:"input_tokens"`
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.InputTokens <= 0 {
					t.Fatalf("invalid count: %d %s", w.Code, w.Body.String())
				}
				if w.Header().Get("X-BOS-Token-Count-Estimated") != "true" || w.Header().Get("X-BOS-Token-Count-Tokenizer") != "o200k_base" {
					t.Fatalf("estimate not identified: %v", w.Header())
				}
				select {
				case event := <-a.usageQueue:
					if event.Metrics.Known || event.Metrics.TotalTokens != 0 || event.Metrics.Cost != 0 || event.Model != "alias" || event.Status != 200 {
						t.Fatalf("count treated as inference usage: %+v", event)
					}
				default:
					t.Fatal("missing request usage record")
				}
				if a.data.Keys[0].QuotaUsedTokens != 0 || a.data.Keys[0].QuotaUsedAmount != 0 {
					t.Fatalf("count charged quota: %+v", a.data.Keys[0])
				}
				return result.InputTokens
			}
			base := `{"model":"alias","messages":[{"role":"user","content":"hello"}]}`
			plain := count(base)
			withSystem := strings.Replace(base, `"messages":`, `"system":[{"type":"text","text":"`+strings.Repeat("system instructions ", 100)+`","cache_control":{"type":"ephemeral"}}],"messages":`, 1)
			if count(withSystem) <= plain {
				t.Fatal("system blocks not counted")
			}
			withTools := strings.Replace(base, `"messages":`, `"tools":[{"name":"read","description":"`+strings.Repeat("read files ", 100)+`","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}],"messages":`, 1)
			if count(withTools) <= plain {
				t.Fatal("tool definitions not counted")
			}
			history := `{"model":"alias","messages":[{"role":"user","content":"read a file"},{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"read","input":{"id":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"` + strings.Repeat("file contents ", 100) + `"}]}]}`
			if count(history) <= plain {
				t.Fatal("tool history not counted")
			}
			withSchema := strings.Replace(base, `"messages":`, `"output_config":{"format":{"type":"json_schema","schema":{"type":"object","description":"`+strings.Repeat("output schema ", 100)+`","properties":{"answer":{"type":"string"}}}}},"messages":`, 1)
			if count(withSchema) <= plain {
				t.Fatal("output schema not counted")
			}
			controls := strings.Replace(base, `"messages":`, `"max_tokens":4096,"stream":true,"thinking":{"type":"enabled","budget_tokens":0},"output_config":{"effort":"high"},"messages":`, 1)
			if count(controls) != plain {
				t.Fatal("generation controls affected input estimate")
			}
			if count(base) != plain {
				t.Fatal("identical request has unstable estimate")
			}
		})
	}
}

func TestMessagesChatTokenCountBoundaries(t *testing.T) {
	base := `{"model":"alias","messages":[{"role":"user","content":"hello"}]}`
	for _, tc := range []struct {
		name, body, header, value, field string
		status                           int
	}{
		{name: "malformed", body: `{`, field: "body", status: 400},
		{name: "trailing data", body: base + `{}`, field: "body", status: 400},
		{name: "missing messages", body: `{"model":"alias"}`, field: "messages", status: 400},
		{name: "empty messages", body: `{"model":"alias","messages":[]}`, field: "messages", status: 400},
		{name: "unsupported sampling", body: strings.Replace(base, `"model":`, `"top_k":3,"model":`, 1), field: "top_k", status: 400},
		{name: "unknown field", body: strings.Replace(base, `"model":`, `"future":true,"model":`, 1), field: "future", status: 400},
		{name: "invalid role", body: strings.Replace(base, `"role":"user"`, `"role":"invalid"`, 1), field: "messages[0].role", status: 400},
		{name: "unsupported image", body: strings.Replace(base, `"hello"`, `[{"type":"image","source":{}}]`, 1), field: "messages[0].content[0].source.type", status: 400},
		{name: "openai beta", body: base, header: "OpenAI-Beta", value: "future", field: "headers.OpenAI-Beta", status: 400},
		{name: "anthropic workspace", body: base, header: "anthropic-workspace-id", value: "private", field: "headers.anthropic-workspace-id", status: 400},
		{name: "unauthorized model", body: strings.Replace(base, `"alias"`, `"spoof"`, 1), status: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, key := crossTestApp(t, "openai_chat")
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid count reached upstream")
				return nil, nil
			})}
			r := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(tc.body))
			r.Header.Set("x-api-key", key)
			if tc.header != "" {
				r.Header.Set(tc.header, tc.value)
			}
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != tc.status || tc.field != "" && (!strings.Contains(w.Body.String(), `"type":"error"`) || !strings.Contains(w.Body.String(), `"param":"`+tc.field+`"`)) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestMessagesTokenCountUnsupportedProvider(t *testing.T) {
	for _, format := range []string{"openai_responses", "custom", "realtime"} {
		t.Run(format, func(t *testing.T) {
			a, key := crossTestApp(t, format)
			r := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(crossMessagesRequest))
			r.Header.Set("x-api-key", key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_conversion") || w.Header().Get("X-BOS-Token-Count-Estimated") != "" {
				t.Fatalf("unsupported provider: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
