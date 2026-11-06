package interfaces

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestGatewayQwenReasoningEffortCompatibility(t *testing.T) {
	for _, endpoint := range []struct{ path, format string }{
		{"/responses", "openai_responses"},
		{"/chat/completions", "openai_chat"},
	} {
		for _, stream := range []bool{false, true} {
			for _, tc := range []struct {
				name, model, target, effort, wantEffort string
			}{
				{"high", "Qwen3.8-27B", "", "high", "xhigh"},
				{"alias", "qwen-alias", "Qwen3.8-27B", "high", "xhigh"},
				{"case_insensitive", "qWeN3.8-27b", "", "high", "xhigh"},
				{"other_model", "Qwen3.8-72B", "", "high", "high"},
				{"alias_to_other_model", "Qwen3.8-27B", "other-model", "high", "high"},
				{"xhigh", "Qwen3.8-27B", "", "xhigh", "xhigh"},
				{"medium", "Qwen3.8-27B", "", "medium", "medium"},
				{"low", "Qwen3.8-27B", "", "low", "low"},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", endpoint.path, stream, tc.name), func(t *testing.T) {
					a := testApp(t)
					const key = "sk-gw-qwen-reasoning-test"
					upstreamModel := tc.model
					p := provider{ID: 1, Type: "api_key", Status: "active", APIFormat: endpoint.format, BaseURL: "https://upstream.example/v1", APIKey: "test-provider-key"}
					if tc.target != "" {
						p.ModelMapping = map[string]string{tc.model: tc.target}
						upstreamModel = tc.target
					}
					p.Models = []string{upstreamModel}
					a.data.Providers = []provider{p}
					a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}

					fields := fmt.Sprintf(`"input":"hello","reasoning":{"effort":%q,"summary":"auto","extension":{"id":9007199254740993}}`, tc.effort)
					response := `{"id":"resp_test","object":"response","status":"completed","output":[]}`
					if endpoint.path == "/chat/completions" {
						fields = fmt.Sprintf(`"messages":[{"role":"user","content":"hello"}],"reasoning_effort":%q`, tc.effort)
						response = `{"id":"chat_test","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`
					}
					payload := fmt.Sprintf(`{"model":%q,"stream":%t,%s,"extension":{"id":9007199254740993,"keep":[true,null,"high",{"effort":"high"}]}}`, tc.model, stream, fields)
					want := decodeQwenReasoningTestJSON(t, []byte(payload))
					want["model"] = upstreamModel
					if endpoint.path == "/responses" {
						want["reasoning"].(map[string]any)["effort"] = tc.wantEffort
					} else {
						want["reasoning_effort"] = tc.wantEffort
						if stream {
							want["stream_options"] = map[string]any{"include_usage": true}
						}
					}
					contentType := "application/json"
					if stream {
						contentType = "text/event-stream"
						if endpoint.path == "/responses" {
							response = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
						} else {
							response = "data: {\"id\":\"chat_test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
						}
					}
					calls := 0
					a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						calls++
						if r.URL.Path != "/v1"+endpoint.path {
							t.Fatalf("upstream path = %q", r.URL.Path)
						}
						raw, err := io.ReadAll(r.Body)
						if err != nil {
							t.Fatal(err)
						}
						if got := decodeQwenReasoningTestJSON(t, raw); !reflect.DeepEqual(got, want) {
							t.Fatalf("upstream payload = %s; want %+v", raw, want)
						}
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
					})}
					r := httptest.NewRequest(http.MethodPost, "/v1"+endpoint.path, strings.NewReader(payload))
					r.Header.Set("Authorization", "Bearer "+key)
					w := httptest.NewRecorder()
					a.routes().ServeHTTP(w, r)
					if w.Code != http.StatusOK || calls != 1 {
						t.Fatalf("status=%d upstream calls=%d body=%s", w.Code, calls, w.Body.String())
					}
					if stream && !w.Flushed {
						t.Fatal("stream response was not flushed")
					}
				})
			}
		}
	}
}

func TestRewriteQwenReasoningEffortLeavesUnmatchedPayloadUnchanged(t *testing.T) {
	for _, tc := range []struct{ name, path, payload string }{
		{"other_path", "/responses/compact", ` {"model":"Qwen3.8-27B", "reasoning":{"effort":"high"}} `},
		{"other_model", "/responses", ` {"model":"other", "reasoning":{"effort":"high"}} `},
		{"missing_effort", "/responses", ` {"model":"Qwen3.8-27B", "reasoning":{"summary":"auto"}} `},
		{"missing_reasoning", "/responses", ` {"model":"Qwen3.8-27B"} `},
		{"null_reasoning", "/responses", ` {"model":"Qwen3.8-27B", "reasoning":null} `},
		{"wrong_reasoning_type", "/responses", ` {"model":"Qwen3.8-27B", "reasoning":"high"} `},
		{"wrong_effort_type", "/responses", ` {"model":"Qwen3.8-27B", "reasoning":{"effort":123}} `},
		{"uppercase_effort", "/responses", ` {"model":"Qwen3.8-27B", "reasoning":{"effort":"HIGH"}} `},
		{"chat_missing_effort", "/chat/completions", ` {"model":"Qwen3.8-27B"} `},
		{"chat_wrong_effort_type", "/chat/completions", ` {"model":"Qwen3.8-27B", "reasoning_effort":{}} `},
		{"responses_wrong_field", "/responses", ` {"model":"Qwen3.8-27B", "reasoning_effort":"high"} `},
		{"chat_wrong_field", "/chat/completions", ` {"model":"Qwen3.8-27B", "reasoning":{"effort":"high"}} `},
		{"invalid_json", "/responses", `{"model":"Qwen3.8-27B","reasoning":{"effort":"high"}`},
		{"empty", "/responses", ""},
		{"null", "/responses", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(tc.payload)
			if got := rewriteQwenReasoningEffort(payload, tc.path); !bytes.Equal(got, payload) {
				t.Fatalf("unmatched payload changed: got %q, want %q", got, payload)
			}
		})
	}
}

func decodeQwenReasoningTestJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
