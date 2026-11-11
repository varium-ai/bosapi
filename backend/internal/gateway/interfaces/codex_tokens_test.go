package interfaces

import (
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCodexChatTokenLimitsGateway(t *testing.T) {
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		for _, streaming := range []bool{false, true} {
			for _, limited := range []bool{false, true} {
				name := field + map[bool]string{false: "/json", true: "/stream"}[streaming] + map[bool]string{false: "/complete", true: "/length"}[limited]
				t.Run(name, func(t *testing.T) {
					a, key := geminiTestApp(t)
					a.data.Providers[0] = provider{ID: 1, Type: "oauth", Platform: "openai", APIFormat: "openai_responses", Status: "active", AccessToken: "token", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour), Models: []string{"actual"}, ModelMapping: map[string]string{"alias": "actual"}}
					called := false
					a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						called = true
						var root map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&root); err != nil {
							t.Fatal(err)
						}
						if string(root["max_output_tokens"]) != "32" || root["max_tokens"] != nil || root["max_completion_tokens"] != nil {
							t.Fatalf("bad limit: %s", root)
						}
						stream := codexMetadataStream
						if limited {
							stream = strings.ReplaceAll(stream, "response.completed", "response.incomplete")
							stream = strings.Replace(stream, `"status":"completed"`, `"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}`, 1)
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
					})}
					request := `{"model":"alias","messages":[{"role":"user","content":"hi"}],"` + field + `":32,"stream":false}`
					if streaming {
						request = strings.Replace(request, `"stream":false`, `"stream":true,"stream_options":{"include_usage":true}`, 1)
					}
					r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(request))
					r.Header.Set("Authorization", "Bearer "+key)
					w := httptest.NewRecorder()
					a.routes().ServeHTTP(w, r)
					body := w.Body.String()
					finish := map[bool]string{false: "stop", true: "length"}[limited]
					if !called || w.Code != 200 || !strings.Contains(body, `"content":"OK"`) || !strings.Contains(body, `"finish_reason":"`+finish+`"`) || !strings.Contains(body, `"total_tokens":20`) || strings.Contains(body, "unsupported_upstream_feature") || streaming && !strings.Contains(body, "[DONE]") {
						t.Fatalf("response: %d %s", w.Code, body)
					}
					select {
					case event := <-a.usageQueue:
						if event.Metrics.TotalTokens != 20 {
							t.Fatalf("lost terminal usage: %+v", event)
						}
					default:
						t.Fatal("missing usage")
					}
				})
			}
		}
	}
}

func TestCodexChatRejectsOtherIncompleteReasons(t *testing.T) {
	for _, detail := range []string{`{"reason":"content_filter"}`, `{"reason":"unknown"}`, `null`} {
		for _, streaming := range []bool{false, true} {
			a := testApp(t)
			stream := strings.ReplaceAll(codexMetadataStream, "response.completed", "response.incomplete")
			stream = strings.Replace(stream, `"status":"completed"`, `"status":"incomplete","incomplete_details":`+detail, 1)
			r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			w := httptest.NewRecorder()
			q := gatewayinfra.RelayRequest{Stream: streaming}
			a.convertCodexChat(w, r, strings.NewReader(stream), q, "x")
			if !strings.Contains(w.Body.String(), "unsupported_upstream_feature") || strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("accepted incomplete %s: %s", detail, w.Body.String())
			}
		}
	}
}

func TestCodexTokenLimitEmptyOutputAndInvalidTool(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		wantError    bool
	}{
		{"empty output", `[]`, false},
		{"truncated tool", `[{"type":"function_call","call_id":"call","name":"read","arguments":"{\"path\":"}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := gatewayinfra.DecodeProtocolJSON([]byte(`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":` + tc.output + `,"usage":{"input_tokens":10,"output_tokens":16}}`))
			if err != nil {
				t.Fatal(err)
			}
			reply, err := codexReply(root, "id")
			if tc.wantError {
				if err == nil {
					t.Fatal("invalid tool arguments accepted")
				}
			} else if err != nil || reply.Stop != "length" || !reply.HasUsage || reply.Output != 16 {
				t.Fatalf("empty token-limited output lost terminal status: %+v %v", reply, err)
			}
		})
	}
}
