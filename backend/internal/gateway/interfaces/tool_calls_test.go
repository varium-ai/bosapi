package interfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/gateway/domain"
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"

	"github.com/coder/websocket"
)

type toolRecordingSink struct {
	inspections []SecurityInspection
	calls       []domain.ToolCall
	events      []SecurityEvent
	block       bool
	failure     error
}

func (sink *toolRecordingSink) Publish(_ context.Context, event SecurityEvent) error {
	sink.events = append(sink.events, event)
	return nil
}
func (sink *toolRecordingSink) Inspect(_ context.Context, inspection SecurityInspection) (bool, error) {
	sink.inspections = append(sink.inspections, inspection)
	return true, nil
}
func (sink *toolRecordingSink) InspectToolCall(_ context.Context, call domain.ToolCall) (SecurityInspectionResult, error) {
	sink.calls = append(sink.calls, call)
	return SecurityInspectionResult{Allowed: !sink.block, RuleID: "test_tool_policy", HitCount: 2}, sink.failure
}

func setupToolGateway(t *testing.T, sink SecurityEventSink, response, contentType string) (*app, string) {
	t.Helper()
	a := testApp(t)
	secret := "sk-gw-" + random(32)
	a.data.Providers = []provider{{ID: 1, Type: "api_key", Status: "active", BaseURL: "https://api.openai.com/v1", APIKey: "upstream-secret"}}
	a.data.Keys = []apiKey{{ID: 7, OrgID: 9, Hash: digest(secret), Status: "active", ProviderID: 1}}
	a.background.SecurityEvents = sink
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}
	return a, secret
}

func TestGatewayRetainsToolHistoryForCorrelationAndScansOnlyNewInvocations(t *testing.T) {
	for _, test := range []struct {
		name, path, request, response string
		protocol                      domain.ToolProtocol
	}{
		{"openai", "/v1/chat/completions", `{"tools":[{"type":"function","function":{"name":"lookup"}}],"model":"test","organization_id":999,"api_key_id":999,"messages":[{"role":"assistant","tool_calls":[{"id":"private-call-id","type":"function","function":{"name":"private-tool-name","arguments":"{\"q\":\"private-parameter\"}"}}]}]}`, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"response-call","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"response-parameter\"}"}}]}}]}`, domain.ToolProtocolOpenAI},
		{"anthropic", "/v1/messages", `{"tools":[{"name":"lookup","input_schema":{}}],"model":"test","max_tokens":20,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"private-call-id","name":"private-tool-name","input":{"q":"private-parameter"}}]}]}`, `{"type":"message","content":[{"type":"text","text":"hello"},{"type":"tool_use","id":"response-call","name":"lookup","input":{"q":"response-parameter"}}]}`, domain.ToolProtocolAnthropic},
	} {
		t.Run(test.name, func(t *testing.T) {
			sink := &toolRecordingSink{}
			a, secret := setupToolGateway(t, sink, test.response, "application/json")
			if test.protocol == domain.ToolProtocolAnthropic {
				a.data.Providers[0].APIFormat = "anthropic_messages"
			}
			r := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.request))
			r.Header.Set("Authorization", "Bearer "+secret)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 200 || len(sink.calls) != 1 || len(sink.inspections) != 1 || len(sink.inspections[0].ToolCalls) != 1 {
				t.Fatalf("status=%d calls=%+v inspections=%+v", w.Code, sink.calls, sink.inspections)
			}
			if sink.calls[0].Protocol != test.protocol || sink.calls[0].Direction != domain.ToolDirectionResponse {
				t.Fatalf("protocol/direction lost: %+v", sink.calls)
			}
			for _, call := range sink.calls {
				if call.APIKeyID != 7 || call.OrganizationID != 9 {
					t.Fatal("untrusted caller identity")
				}
			}
			if test.protocol == domain.ToolProtocolOpenAI && w.Body.String() != test.response {
				t.Fatal("proxy response changed")
			}
			if len(sink.events) != 1 {
				t.Fatalf("events=%+v", sink.events)
			}
			for _, event := range sink.events {
				if event.ToolCall == nil || event.ToolCall.CallIDHash == "" || event.HitCount != 2 || event.APIKeyID != 7 || event.OrganizationID != 9 {
					t.Fatalf("metadata=%+v", event)
				}
				encoded, _ := json.Marshal(event)
				for _, private := range []string{"private-call-id", "private-parameter", "response-parameter", secret} {
					if bytes.Contains(encoded, []byte(private)) {
						t.Fatalf("private content in event: %s", encoded)
					}
				}
			}
		})
	}
}

func TestGatewayKeepsHistoryPolicyFailuresLocalAndRejectsMalformedInput(t *testing.T) {
	for _, test := range []struct {
		name, arguments string
		block           bool
		failure         error
		status          int
		code            string
	}{
		{"invalid arguments", "PRIVATE_INVALID_ARGUMENT", false, nil, 400, "invalid_request"},
		{"blocked history", "{}", true, nil, 200, ""},
		{"history scanner unavailable", "{}", false, errors.New("PRIVATE_SCANNER_FAILURE"), 200, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			sink := &toolRecordingSink{block: test.block, failure: test.failure}
			a, secret := setupToolGateway(t, sink, "{}", "application/json")
			forwarded := false
			a.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				forwarded = true
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
			})
			payload, _ := json.Marshal(map[string]any{"model": "test", "messages": []any{map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "private-call", "type": "function", "function": map[string]any{"name": "private-name", "arguments": test.arguments}}}}}})
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
			r.Header.Set("Authorization", "Bearer "+secret)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if forwarded != (test.status == 200) {
				t.Fatal("history verdict affected request forwarding")
			}
			if w.Code != test.status || !strings.Contains(w.Body.String(), test.code) || strings.Contains(w.Body.String(), "PRIVATE") || strings.Contains(w.Body.String(), "private-") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	for _, direction := range []domain.ToolDirection{domain.ToolDirectionRequest, domain.ToolDirectionResponse} {
		status, code := toolCallErrorMapping(&domain.ToolCallParseError{Protocol: domain.ToolProtocolMCP, Direction: direction})
		if direction == domain.ToolDirectionRequest && (status != 400 || code != "invalid_request") || direction == domain.ToolDirectionResponse && (status != 502 || code != "upstream_error") {
			t.Fatalf("status=%d code=%s", status, code)
		}
	}
}

func TestGatewayToolSSEAuditPreservesBytesAndAssemblesArguments(t *testing.T) {
	stream := "data: " + `{"id":"completion","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"shell","arguments":"{\"command\":\"rm "}}]}}]}` + "\n\ndata: " + `{"id":"completion","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"-rf /\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	sink := &toolRecordingSink{}
	a, secret := setupToolGateway(t, sink, stream, "text/event-stream")
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"shell"}}],"stream":true}`))
	r.Header.Set("Authorization", "Bearer "+secret)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != stream || len(sink.calls) != 1 || !strings.Contains(string(sink.calls[0].Arguments), "rm -rf /") {
		t.Fatalf("status=%d calls=%+v", w.Code, sink.calls)
	}
}

func TestDefaultToolScannerSeesEscapedParametersAndResults(t *testing.T) {
	for _, call := range []domain.ToolCall{
		{Arguments: json.RawMessage(`{"command":"\u0072m -rf /"}`)},
		{Kind: domain.ToolCallResult, Result: json.RawMessage(`[{"type":"text","text":"\u0072m -rf /"}]`)},
		{Arguments: json.RawMessage(`{"password":"hunter2"}`)},
	} {
		result, err := (gatewayinfra.RuleSecurityEventSink{}).InspectToolCall(context.Background(), call)
		if err != nil || result.RuleID == "" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
}

func TestMCPIsNotACustomerEndpoint(t *testing.T) {
	a, secret := setupToolGateway(t, &toolRecordingSink{}, "{}", "application/json")
	a.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("MCP routed upstream"); return nil, nil })
	for _, path := range []string{"/mcp", "/tools/call", "/v1/mcp", "/v1/tools/call"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lookup","arguments":{}}}`))
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("path=%s status=%d", path, w.Code)
		}
	}
}

func TestWebSocketToolPolicyBlocksOnlyInvocationAndKeepsConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	upstreamErrors := make(chan error, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			upstreamErrors <- err
			return
		}
		defer func() { _ = connection.CloseNow() }()
		for _, response := range []string{
			`{"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"private-id","name":"private-name","arguments":"{\"q\":\"private-parameter\"}"},{"type":"message","content":[{"type":"output_text","text":"first text"}]}]}}`,
			`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"second text"}]}]}}`,
		} {
			if _, _, err := connection.Read(ctx); err != nil {
				upstreamErrors <- err
				return
			}
			if err := connection.Write(ctx, websocket.MessageText, []byte(response)); err != nil {
				upstreamErrors <- err
				return
			}
		}
		// Keep upstream open until the client ends the conversation.
		_, _, _ = connection.Read(ctx)
	}))
	defer upstream.Close()
	sink := &toolRecordingSink{block: true}
	a := testApp(t)
	a.background.SecurityEvents = sink
	a.client = upstream.Client()
	done := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		r = r.WithContext(context.WithValue(r.Context(), gatewayAPIKeyRequestKey{}, apiKey{ID: 7, OrgID: 9}))
		a.bridgeWebSocket(w, r, strings.Replace(upstream.URL, "http://", "ws://", 1), nil)
	}))
	defer gateway.Close()
	connection, _, err := websocket.Dial(ctx, strings.Replace(gateway.URL, "http://", "ws://", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.CloseNow() }()
	for _, expected := range []string{"first text", "second text"} {
		if err := connection.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","response":{"input":[],"tools":[{"type":"function","name":"private-name"}]}}`)); err != nil {
			t.Fatal(err)
		}
		_, payload, err := connection.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(payload, []byte(expected)) || bytes.Contains(payload, []byte("private-parameter")) || bytes.Contains(payload, []byte(`"type":"function_call"`)) {
			t.Fatalf("unsafe or missing output: %s", payload)
		}
	}
	_ = connection.Close(websocket.StatusNormalClosure, "done")
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("bridge did not stop")
	}
	select {
	case err := <-upstreamErrors:
		t.Fatal(err)
	default:
	}
	if len(sink.calls) != 1 || len(sink.events) != 1 || sink.events[0].Action != "block" || sink.events[0].HTTPStatus != 200 || sink.events[0].ToolCall.Name != "private-name" {
		t.Fatalf("events=%+v", sink.events)
	}
}
