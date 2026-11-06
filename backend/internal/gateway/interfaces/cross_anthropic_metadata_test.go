package interfaces

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCrossAnthropicResponseMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, textExtra, toolExtra, errorField string
	}{
		{name: "direct caller", toolExtra: `,"caller":{"type":"direct"}`},
		{name: "null caller", toolExtra: `,"caller":null`},
		{name: "empty citations", textExtra: `,"citations":[]`},
		{name: "null citations", textExtra: `,"citations":null`},
		{name: "combined", textExtra: `,"citations":[]`, toolExtra: `,"caller":{"type":"direct"}`},
		{name: "programmatic caller", toolExtra: `,"caller":{"type":"code_execution_20260120","tool_id":"PRIVATE_SERVER_TOOL"}`, errorField: "caller.type"},
		{name: "malformed caller", toolExtra: `,"caller":"PRIVATE_CALLER"`, errorField: "caller"},
		{name: "array caller", toolExtra: `,"caller":[]`, errorField: "caller"},
		{name: "missing caller type", toolExtra: `,"caller":{}`, errorField: "caller.type"},
		{name: "malformed caller type", toolExtra: `,"caller":{"type":{}}`, errorField: "caller.type"},
		{name: "unknown caller field", toolExtra: `,"caller":{"type":"direct","tool_id":"PRIVATE_SERVER_TOOL"}`, errorField: "caller.tool_id"},
		{name: "populated citations", textExtra: `,"citations":[{"type":"web_search_result_location","cited_text":"PRIVATE_CITATION"}]`, errorField: "citations"},
		{name: "malformed citations", textExtra: `,"citations":"PRIVATE_CITATION"`, errorField: "citations"},
		{name: "object citations", textExtra: `,"citations":{}`, errorField: "citations"},
		{name: "unknown text field", textExtra: `,"future_field":"PRIVATE_METADATA"`, errorField: "future_field"},
		{name: "unknown tool field", toolExtra: `,"future_field":"PRIVATE_METADATA"`, errorField: "future_field"},
	} {
		for _, stream := range []bool{false, true} {
			name := tc.name + "/json"
			if stream {
				name = tc.name + "/sse"
			}
			t.Run(name, func(t *testing.T) {
				a, key := crossTestApp(t, "anthropic_messages")
				body, response, contentType := crossChatRequest, crossClaudeResponse, "application/json"
				if stream {
					body = strings.Replace(body, `"model":`, `"stream":true,"stream_options":{"include_usage":true},"model":`, 1)
					response, contentType = crossClaudeStream, "text/event-stream"
				}
				text := `"type":"text","text":"hello"`
				if stream {
					text = `"type":"text","text":""`
				}
				response = strings.Replace(response, text+`}`, text+tc.textExtra+`}`, 1)
				response = strings.Replace(response, `"type":"tool_use",`, `"type":"tool_use"`+tc.toolExtra+`,`, 1)
				a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(response)})}, nil
				})}
				r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+key)
				w := httptest.NewRecorder()
				a.routes().ServeHTTP(w, r)
				output := w.Body.String()
				select {
				case event := <-a.usageQueue:
					if tc.errorField != "" {
						wantStatus := http.StatusBadGateway
						if stream {
							wantStatus = http.StatusOK
						}
						if w.Code != wantStatus {
							t.Fatalf("HTTP status=%d want=%d", w.Code, wantStatus)
						}
						if event.Status != 502 || event.ErrorCode != "unsupported_upstream_feature" || !strings.Contains(output, "unsupported_upstream_feature") || !strings.Contains(output, tc.errorField) {
							t.Fatalf("missing conversion failure: status=%d code=%s body=%s", event.Status, event.ErrorCode, output)
						}
						if strings.Contains(output, "[DONE]") || strings.Contains(output, "PRIVATE_") || strings.Contains(output, "9007199254740993") {
							t.Fatalf("failed metadata validation leaked content or completed successfully: %s", output)
						}
						return
					}
					if event.Status != 200 || event.ErrorCode != "" || event.Metrics.TotalTokens != 24 {
						t.Fatalf("incorrect successful usage: %+v", event)
					}
				default:
					t.Fatal("missing usage event")
				}
				if w.Code != 200 || strings.Contains(output, "unsupported_upstream_feature") || !strings.Contains(output, "hello") || !strings.Contains(output, "call_new") || !strings.Contains(output, "9007199254740993") {
					t.Fatalf("metadata broke response: %d %s", w.Code, output)
				}
				if stream && !strings.Contains(output, "data: [DONE]") {
					t.Fatalf("missing successful terminal event: %s", output)
				}
			})
		}
	}
}

func TestCrossAnthropicDirectCallerStillChecksToolAuthorization(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "json"
		if stream {
			name = "sse"
		}
		t.Run(name, func(t *testing.T) {
			a, key := crossTestApp(t, "anthropic_messages")
			body, response, contentType := crossChatRequest, crossClaudeResponse, "application/json"
			if stream {
				body = strings.Replace(body, `"model":`, `"stream":true,"model":`, 1)
				response, contentType = crossClaudeStream, "text/event-stream"
			}
			response = strings.ReplaceAll(response, `"name":"read"`, `"name":"undeclared","caller":{"type":"direct"}`)
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			output := w.Body.String()
			if strings.Contains(output, "9007199254740993") || !strings.Contains(output, "unauthorized_tool") || strings.Contains(output, "unsupported_upstream_feature") || !strings.Contains(output, "hello") {
				t.Fatalf("direct caller bypassed tool authorization: %s", output)
			}
		})
	}
}
