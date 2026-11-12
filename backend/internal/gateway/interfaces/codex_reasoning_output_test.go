package interfaces

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
)

// Replay an actual terra SSE sequence, with identity and encrypted data replaced
// by fixtures. Validate the full route, including missing upstream Content-Type.
func TestCodexReasoningOutputGateway(t *testing.T) {
	fixture, err := os.ReadFile("testdata/codex_reasoning.sse")
	if err != nil {
		t.Fatal(err)
	}
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[streaming], func(t *testing.T) {
			a, key := geminiTestApp(t)
			a.data.Providers[0] = provider{ID: 1, Type: "oauth", Platform: "openai", APIFormat: "openai_responses", Status: "active", AccessToken: "token", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour), Models: []string{"actual"}, ModelMapping: map[string]string{"alias": "actual"}}
			called := false
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				raw, _ := io.ReadAll(r.Body)
				body, err := gatewayinfra.DecodeProtocolJSON(raw)
				if err != nil {
					t.Fatal(err)
				}
				format := body["text"].(map[string]any)["format"].(map[string]any)
				if format["type"] != "json_object" || body["response_format"] != nil || body["reasoning"].(map[string]any)["effort"] != "high" || !reflect.DeepEqual(body["include"], []any{"reasoning.encrypted_content"}) {
					t.Fatalf("bad upstream mapping: %s", raw)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(string(fixture))})}, nil
			})}
			request := `{"model":"alias","response_format":{"type":"json_object"},"reasoning_effort":"high","messages":[{"role":"user","content":"hi"}],"stream":false}`
			if streaming {
				request = strings.Replace(request, `"stream":false`, `"stream":true,"stream_options":{"include_usage":true}`, 1)
			}
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(request))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if !called || w.Code != 200 || strings.Contains(w.Body.String(), "unsupported_") || streaming && !strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("failed conversion: %d %s", w.Code, w.Body.String())
			}
			message := map[string]any{"role": "assistant"}
			var usage map[string]any
			if !streaming {
				root, err := gatewayinfra.DecodeProtocolJSON(w.Body.Bytes())
				if err != nil {
					t.Fatal(err)
				}
				message = root["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
				usage = root["usage"].(map[string]any)
			} else {
				for _, line := range strings.Split(w.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: {") {
						continue
					}
					root, err := gatewayinfra.DecodeProtocolJSON([]byte(strings.TrimPrefix(line, "data: ")))
					if err != nil {
						t.Fatal(err)
					}
					if value, ok := root["usage"].(map[string]any); ok {
						usage = value
					}
					for _, entry := range root["choices"].([]any) {
						choice := entry.(map[string]any)
						delta := choice["delta"].(map[string]any)
						for _, field := range []string{"content", "reasoning_content"} {
							if value, ok := delta[field].(string); ok {
								old, _ := message[field].(string)
								message[field] = old + value
								if strings.Contains(value, "opaque-fixture") {
									t.Fatal("ciphertext exposed as text")
								}
							}
						}
						if items, ok := delta["codex_reasoning"].([]any); ok {
							previous, _ := message["codex_reasoning"].([]any)
							message["codex_reasoning"] = append(previous, items...)
						}
					}
				}
			}
			content, _ := message["content"].(string)
			summary, _ := message["reasoning_content"].(string)
			items, ok := message["codex_reasoning"].([]any)
			if !strings.Contains(content, "4046") || strings.Contains(content, "Checking") || summary != "**Checking n = 4046**" || !ok || len(items) != 1 || usage == nil || usage["completion_tokens_details"] == nil {
				t.Fatalf("reasoning/answer/usage lost: %v %v", message, usage)
			}
			// Replay exactly what a client accumulated, retaining the native item.
			raw, _ := json.Marshal(map[string]any{"model": "alias", "messages": []any{message, map[string]any{"role": "user", "content": "continue"}}})
			q, err := gatewayinfra.ParseRelayRequest(raw, "chat", "openai_responses")
			if err != nil {
				t.Fatal(err)
			}
			native, err := q.CodexUpstream()
			if err != nil || !strings.Contains(string(native), `"encrypted_content":"opaque-fixture"`) {
				t.Fatalf("reasoning not replayed: %s %v", native, err)
			}
			select {
			case event := <-a.usageQueue:
				if event.Metrics.TotalTokens <= 0 || event.Model != "alias" {
					t.Fatalf("missing usage: %+v", event)
				}
			default:
				t.Fatal("usage not persisted")
			}
		})
	}
}
