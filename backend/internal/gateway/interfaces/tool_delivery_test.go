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

type selectiveToolSink struct{ toolRecordingSink }

func (sink *selectiveToolSink) InspectToolCall(_ context.Context, call domain.ToolCall) (SecurityInspectionResult, error) {
	sink.calls = append(sink.calls, call)
	blocked := call.Name == "shell"
	action := "observe"
	if blocked {
		action = "block"
	}
	return SecurityInspectionResult{Allowed: !blocked, Matches: []domain.SecurityRuleMatch{{RuleID: "123", Version: 4, Scope: "global", Action: action, HitCount: 1}}}, sink.failure
}

func TestGatewayBlocksOnlyMatchedResponseToolAndKeepsUsage(t *testing.T) {
	response := `{"choices":[{"message":{"content":"normal text","tool_calls":[{"id":"blocked-id","type":"function","function":{"name":"shell","arguments":"{\"command\":\"PRIVATE_DANGEROUS_COMMAND\"}"}},{"id":"allowed-id","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	sink := &selectiveToolSink{}
	a, secret := setupToolGateway(t, sink, response, "application/json")
	a.usageQueue = make(chan usageEvent, 1)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"shell"}},{"type":"function","function":{"name":"lookup"}}]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	a.routes().ServeHTTP(recorder, request)
	if recorder.Code != 200 || strings.Contains(recorder.Body.String(), "PRIVATE") || !strings.Contains(recorder.Body.String(), "allowed-id") || !strings.Contains(recorder.Body.String(), "normal text") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(sink.events) != 2 || sink.events[0].Action != "block" || sink.events[0].HTTPStatus != 200 || sink.events[0].RuleVersion != 4 || sink.events[0].RuleScope != "global" || sink.events[0].ToolCall.Name != "shell" {
		t.Fatalf("events=%+v", sink.events)
	}
	encoded, _ := json.Marshal(sink.events)
	if bytes.Contains(encoded, []byte("PRIVATE")) || bytes.Contains(encoded, []byte("blocked-id")) {
		t.Fatal("parameters or raw call identity leaked to audit")
	}
	select {
	case event := <-a.usageQueue:
		if event.Metrics.TotalTokens != 15 {
			t.Fatalf("usage=%+v", event.Metrics)
		}
	default:
		t.Fatal("usage not recorded")
	}
}

func TestGatewayInspectionFailureDropsOnlyResponseTools(t *testing.T) {
	sink := &selectiveToolSink{toolRecordingSink: toolRecordingSink{failure: errors.New("PRIVATE_FAILURE")}}
	response := `{"choices":[{"message":{"content":"normal text","tool_calls":[{"id":"bad","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`
	a, secret := setupToolGateway(t, sink, response, "application/json")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"shell"}},{"type":"function","function":{"name":"lookup"}}]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	a.routes().ServeHTTP(recorder, request)
	if recorder.Code != 200 || strings.Contains(recorder.Body.String(), "tool_calls") || !strings.Contains(recorder.Body.String(), "normal text") || strings.Contains(recorder.Body.String(), "PRIVATE") {
		t.Fatalf("output=%s", recorder.Body.String())
	}
	if len(sink.events) != 1 || sink.events[0].Action != "block" {
		t.Fatalf("events=%+v", sink.events)
	}
}

func TestGatewayObserveKeepsHistoryParametersOutOfAsyncContext(t *testing.T) {
	sink := &toolRecordingSink{}
	a, secret := setupToolGateway(t, sink, `{}`, "application/json")
	a.usageQueue = make(chan usageEvent, 1)
	payload := `{"model":"test","messages":[{"role":"assistant","tool_calls":[{"id":"history","type":"function","function":{"name":"lookup","arguments":"{\"password\":\"PRIVATE_PARAMETER\"}"}}]}]}`
	a.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte("PRIVATE_PARAMETER")) {
			t.Fatal("observe mutated tool parameters")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	a.routes().ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("status=%d", recorder.Code)
	}
	select {
	case event := <-a.usageQueue:
		if event.Context.Value(gatewayToolCallsRequestKey{}) != nil {
			t.Fatal("tool parameters retained by async usage event")
		}
	default:
		t.Fatal("missing usage event")
	}
}

func TestLargeNonToolJSONResponsesRemainStreamed(t *testing.T) {
	a := testApp(t)
	payload := `{"data":[{"b64_json":"` + strings.Repeat("x", (2<<20)+1) + `"}]}`
	r := httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	w := httptest.NewRecorder()
	a.forwardInspectedBody(w, r, strings.NewReader(payload), http.StatusOK, "application/json")
	if w.Code != http.StatusOK || w.Body.String() != payload {
		t.Fatal("tool inspection limited an image response")
	}
}

func TestGatewayUndeclaredToolsReturnProtocolErrorsWithoutInspection(t *testing.T) {
	for _, test := range []struct{ name, path, request, response string }{
		{"chat", "/v1/chat/completions", `{"model":"test","tools":[{"type":"function","function":{"name":"lookup"}}],"messages":[]}`, `{"choices":[{"message":{"role":"assistant","content":"ordinary","tool_calls":[{"id":"bad","type":"function","function":{"name":"shell","arguments":"{\"command\":\"PRIVATE_ARGUMENT\"}"}},{"id":"good","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`},
		{"responses", "/v1/responses", `{"model":"test","tools":[{"type":"function","name":"lookup"}],"input":[]}`, `{"output":[{"type":"function_call","call_id":"bad","name":"shell","arguments":"{\"command\":\"PRIVATE_ARGUMENT\"}"},{"type":"function_call","call_id":"good","name":"lookup","arguments":"{}"}]}`},
		{"anthropic", "/v1/messages", `{"model":"test","max_tokens":20,"tools":[{"name":"lookup","input_schema":{}}],"messages":[{"role":"user","content":"hello"}]}`, `{"type":"message","content":[{"type":"text","text":"ordinary"},{"type":"tool_use","id":"bad","name":"shell","input":{"command":"PRIVATE_ARGUMENT"}},{"type":"tool_use","id":"good","name":"lookup","input":{}}],"stop_reason":"tool_use"}`},
		{"no declarations", "/v1/chat/completions", `{"model":"test","messages":[]}`, `{"choices":[{"message":{"tool_calls":[{"id":"bad","type":"function","function":{"name":"shell","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`},
		{"tool choice none", "/v1/responses", `{"model":"test","tools":[{"type":"function","name":"shell"}],"tool_choice":"none","input":[]}`, `{"output":[{"type":"function_call","call_id":"bad","name":"shell","arguments":"{}"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			sink := &toolRecordingSink{}
			a, secret := setupToolGateway(t, sink, test.response, "application/json")
			if test.name == "anthropic" {
				a.data.Providers[0].APIFormat = "anthropic_messages"
			}
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.request))
			request.Header.Set("Authorization", "Bearer "+secret)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, request)
			payload := w.Body.Bytes()
			if w.Code != 200 || bytes.Contains(payload, []byte("PRIVATE")) || !bytes.Contains(payload, []byte("unauthorized_tool")) || !bytes.Contains(payload, []byte("-32601")) {
				t.Fatalf("status=%d body=%s", w.Code, payload)
			}
			calls, err := gatewayinfra.NewToolCallParser(domain.ToolCallContext{}, nil).Parse(payload)
			if err != nil {
				t.Fatal(err)
			}
			results, invocations := 0, 0
			for _, call := range calls {
				if call.Kind == domain.ToolCallInvocation {
					invocations++
					if call.Name != "lookup" {
						t.Fatalf("unauthorized invocation delivered: %+v", call)
					}
				} else if call.ID == "bad" && call.IsError {
					results++
				}
			}
			if results != 1 || len(sink.calls) != invocations || len(sink.events) != invocations+1 || sink.events[0].RuleID != "unauthorized_tool" || sink.events[0].Action != "block" {
				t.Fatalf("calls=%+v events=%+v", calls, sink.events)
			}
		})
	}
}

func TestWebSocketPipelinedRequestsCannotExpandEarlierToolAuthorization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for range 2 {
			if _, _, err := connection.Read(ctx); err != nil {
				return
			}
		}
		for _, id := range []string{"first", "second"} {
			response := `{"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"` + id + `","name":"lookup","arguments":"{}"}]}}`
			if connection.Write(ctx, websocket.MessageText, []byte(response)) != nil {
				return
			}
		}
		_, _, _ = connection.Read(ctx)
	}))
	defer upstream.Close()
	sink := &toolRecordingSink{}
	a := testApp(t)
	a.background.SecurityEvents = sink
	a.client = upstream.Client()
	done := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		a.bridgeWebSocket(w, r, strings.Replace(upstream.URL, "http://", "ws://", 1), nil)
	}))
	defer gateway.Close()
	connection, _, err := websocket.Dial(ctx, strings.Replace(gateway.URL, "http://", "ws://", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	for _, request := range []string{`{"type":"response.create","input":[]}`, `{"type":"response.create","input":[],"tools":[{"type":"function","name":"lookup"}]}`} {
		if err := connection.Write(ctx, websocket.MessageText, []byte(request)); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 2 {
		_, payload, err := connection.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		calls, err := gatewayinfra.NewToolCallParser(domain.ToolCallContext{}, nil).Parse(payload)
		if err != nil || len(calls) != 1 {
			t.Fatalf("calls=%+v err=%v", calls, err)
		}
		if index == 0 && (calls[0].Kind != domain.ToolCallResult || !calls[0].IsError || calls[0].ID != "first") {
			t.Fatalf("later authorization bypassed earlier request: %s", payload)
		}
		if index == 1 && (calls[0].Kind != domain.ToolCallInvocation || calls[0].ID != "second") {
			t.Fatalf("later authorized request was blocked: %s", payload)
		}
	}
	_ = connection.Close(websocket.StatusNormalClosure, "done")
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("bridge did not stop")
	}
	if len(sink.calls) != 1 || len(sink.events) != 2 || sink.events[0].RuleID != "unauthorized_tool" {
		t.Fatalf("events=%+v", sink.events)
	}
}
