package infrastructure

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"bosapi/backend/internal/gateway/domain"
)

func TestParseToolProtocols(t *testing.T) {
	caller := domain.ToolCallContext{APIKeyID: 7, OrganizationID: 9, Direction: domain.ToolDirectionRequest}
	for _, test := range []struct {
		name, body string
		protocol   domain.ToolProtocol
	}{
		{"openai history", `{"api_key_id":999,"organization_id":999,"messages":[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"n\":9007199254740993,\"q\":\"hello\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"done"}]}`, domain.ToolProtocolOpenAI},
		{"responses history", `{"input":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"n\":9007199254740993,\"q\":\"hello\"}"},{"type":"function_call_output","call_id":"call_1","output":"done"}]}`, domain.ToolProtocolOpenAI},
		{"anthropic history", `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{"n":9007199254740993,"q":"hello"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"done"}],"is_error":true}]}]}`, domain.ToolProtocolAnthropic},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls, err := NewToolCallParser(caller, nil).Parse([]byte(test.body))
			if err != nil || len(calls) != 2 {
				t.Fatalf("calls=%+v err=%v", calls, err)
			}
			for index, call := range calls {
				if call.ToolCallContext != caller || call.ID != "call_1" || call.Name != "lookup" || call.Protocol != test.protocol {
					t.Fatalf("call[%d]=%+v", index, call)
				}
				if !strings.Contains(string(call.Arguments), "9007199254740993") {
					t.Fatalf("lost number precision: %s", call.Arguments)
				}
			}
			if calls[0].Kind != domain.ToolCallInvocation || calls[1].Kind != domain.ToolCallResult || len(calls[1].Result) == 0 {
				t.Fatal("call/result semantics lost")
			}
			if test.protocol == domain.ToolProtocolAnthropic && !calls[1].IsError {
				t.Fatal("tool result error flag lost")
			}
		})
	}
}

func TestParseToolResponsesAndDoNotTreatDefinitionsOrTextAsCalls(t *testing.T) {
	for _, body := range []string{
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`,
		`{"type":"message","content":[{"type":"text","text":"hello"},{"type":"tool_use","id":"c","name":"lookup","input":{}}]}`,
		`{"response":{"output":[{"type":"function_call","call_id":"c","name":"lookup","arguments":"{}"}]}}`,
	} {
		calls, err := NewToolCallParser(domain.ToolCallContext{Direction: domain.ToolDirectionResponse}, nil).Parse([]byte(body))
		if err != nil || len(calls) != 1 || calls[0].Name != "lookup" {
			t.Fatalf("calls=%+v err=%v", calls, err)
		}
	}
	for _, body := range []string{
		`{"tools":[{"type":"function","function":{"name":"lookup","parameters":{}}},{"name":"lookup","input_schema":{}}],"messages":[{"role":"user","content":"tool_calls tools/call tool_use"}]}`,
		`{"messages":[{"role":"assistant","tool_calls":null,"content":"hello"}]}`,
		`{"input":[{"type":"input_text","text":"{\"type\":\"tool_use\"}"}]}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"unrelated RPC"}]}}`,
	} {
		calls, err := NewToolCallParser(domain.ToolCallContext{}, nil).Parse([]byte(body))
		if err != nil || len(calls) != 0 {
			t.Fatalf("false positive: calls=%+v err=%v", calls, err)
		}
	}
}

func TestMCPInternalCallCorrelation(t *testing.T) {
	caller := domain.ToolCallContext{APIKeyID: 7, OrganizationID: 9, Direction: domain.ToolDirectionRequest}
	requests, err := NewToolCallParser(caller, nil).Parse([]byte(`[{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"lookup","arguments":{"query":"test"}}},{"jsonrpc":"2.0","id":"second","method":"tools/call","params":{"name":"ping"}}]`))
	if err != nil || len(requests) != 2 || requests[0].ID != "42" || requests[0].Name != "lookup" || string(requests[1].Arguments) != "{}" {
		t.Fatalf("requests=%+v err=%v", requests, err)
	}
	caller.Direction = domain.ToolDirectionResponse
	parser := NewToolCallParser(caller, requests)
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":42,"result":{"content":[{"type":"text","text":"failed"}],"isError":true}}`,
		`{"jsonrpc":"2.0","id":42,"error":{"code":-32603,"message":"private upstream detail"}}`,
	} {
		calls, err := parser.Parse([]byte(body))
		if err != nil || len(calls) != 1 || calls[0].Name != "lookup" || calls[0].Kind != domain.ToolCallResult || !calls[0].IsError || calls[0].ToolCallContext != caller {
			t.Fatalf("calls=%+v err=%v", calls, err)
		}
	}
	// JSON-RPC string and numeric IDs are different identities.
	calls, err := parser.Parse([]byte(`{"jsonrpc":"2.0","id":"42","result":{"content":[]}}`))
	if err != nil || len(calls) != 0 {
		t.Fatal("correlated a string ID with a numeric ID")
	}
}

func TestToolWebSocketFramesCorrelateAcrossDirections(t *testing.T) {
	response := NewToolCallStream(domain.ToolCallContext{Direction: domain.ToolDirectionResponse}, nil)
	request := NewToolCallStream(domain.ToolCallContext{Direction: domain.ToolDirectionRequest, APIKeyID: 7, OrganizationID: 9}, nil)
	calls, err := response.FeedJSON([]byte(`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc1","call_id":"c1","name":"lookup","arguments":"{\"q\":\"hello\"}"}}`))
	if err != nil || len(calls) != 1 {
		t.Fatalf("calls=%+v err=%v", calls, err)
	}
	request.Remember(calls)
	calls, err = request.FeedJSON([]byte(`{"type":"response.create","response":{"input":[{"type":"function_call_output","call_id":"c1","output":"done"}]}}`))
	if err != nil || len(calls) != 1 || calls[0].Name != "lookup" || calls[0].Kind != domain.ToolCallResult || calls[0].APIKeyID != 7 || calls[0].OrganizationID != 9 || calls[0].Direction != domain.ToolDirectionRequest {
		t.Fatalf("calls=%+v err=%v", calls, err)
	}
}

func TestToolStreamBoundsMetadataAndCorrelationMemory(t *testing.T) {
	stream := NewToolCallStream(domain.ToolCallContext{}, nil)
	for index := 0; index < 3; index++ {
		payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"name": strings.Repeat("x", maxToolPayload/2)}}}}}}})
		_, err := stream.FeedJSON(payload)
		if err != nil {
			break
		}
	}
	if !stream.failed || len(stream.pending) != 0 {
		t.Fatal("unbounded fragmented tool name")
	}
	parser := NewToolCallParser(domain.ToolCallContext{}, nil)
	for index := 0; index < 5; index++ {
		parser.remember(domain.ToolCall{Protocol: domain.ToolProtocolOpenAI, Kind: domain.ToolCallInvocation, ID: string(rune('a' + index)), Arguments: json.RawMessage(strings.Repeat("x", maxToolPayload/2))})
	}
	if parser.knownBytes > maxToolPayload {
		t.Fatal("unbounded retained arguments")
	}
}

func TestInvalidToolArgumentsAreSafeAndBounded(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"tool_calls":[{"type":"function","function":{"name":"lookup","arguments":"PRIVATE_INVALID_ARGUMENT"}}]}]}`,
		`{"output":[{"type":"function_call","name":"lookup","arguments":"[]"}]}`,
		`{"content":[{"type":"tool_use","name":"lookup","input":"PRIVATE_INVALID_ARGUMENT"}]}`,
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"lookup","arguments":null}}`,
		`{"messages":[{"tool_calls":{}}]}`,
	} {
		_, err := NewToolCallParser(domain.ToolCallContext{Direction: domain.ToolDirectionRequest}, nil).Parse([]byte(body))
		var failure *domain.ToolCallParseError
		if !errors.As(err, &failure) || strings.Contains(err.Error(), "PRIVATE") || failure.Direction != domain.ToolDirectionRequest {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	items := make([]any, maxToolCalls+1)
	for index := range items {
		items[index] = map[string]any{"type": "tool_use", "id": "c", "name": "lookup", "input": map[string]any{}}
	}
	body, _ := json.Marshal(map[string]any{"content": items})
	if _, err := NewToolCallParser(domain.ToolCallContext{}, nil).Parse(body); err == nil {
		t.Fatal("unbounded tool count")
	}
	if _, err := NewToolCallParser(domain.ToolCallContext{}, nil).Parse([]byte(strings.Repeat(" ", maxToolPayload+1))); err == nil {
		t.Fatal("unbounded payload")
	}
}

func TestToolStreamAssemblesAndDeduplicates(t *testing.T) {
	for _, test := range []struct {
		name, stream string
		count        int
	}{
		{"chat parallel", "data: " + `{"id":"completion_1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"shell","arguments":"{\"command\":\"rm "}},{"index":1,"id":"c2","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}` + "\r\n\r\ndata: " + `{"id":"completion_1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"hello\"}"}},{"index":0,"function":{"arguments":"-rf /\"}"}}]},"finish_reason":"tool_calls"}]}` + "\r\n\r\ndata: [DONE]\r\n\r\n", 2},
		{"anthropic", "event: content_block_start\ndata: " + `{"index":1,"content_block":{"type":"tool_use","id":"c1","name":"shell","input":{}}}` + "\n\nevent: content_block_delta\ndata: " + `{"index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"rm "}}` + "\n\nevent: content_block_delta\ndata: " + `{"index":1,"delta":{"type":"input_json_delta","partial_json":"-rf /\"}"}}` + "\n\nevent: content_block_stop\ndata: {\"index\":1}\n\n", 1},
		{"responses", "data: " + `{"type":"response.output_item.added","item":{"type":"function_call","id":"fc1","call_id":"c1","name":"shell","arguments":""}}` + "\n\ndata: " + `{"type":"response.function_call_arguments.delta","item_id":"fc1","delta":"{\"command\":\"rm "}` + "\n\ndata: " + `{"type":"response.function_call_arguments.delta","item_id":"fc1","delta":"-rf /\"}"}` + "\n\ndata: " + `{"type":"response.function_call_arguments.done","item_id":"fc1","arguments":"{\"command\":\"rm -rf /\"}"}` + "\n\ndata: " + `{"type":"response.output_item.done","item":{"type":"function_call","id":"fc1","call_id":"c1","name":"shell","arguments":"{\"command\":\"rm -rf /\"}"}}` + "\n\ndata: " + `{"type":"response.completed","response":{"output":[{"type":"function_call","id":"fc1","call_id":"c1","name":"shell","arguments":"{\"command\":\"rm -rf /\"}"}]}}` + "\n\n", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			caller := domain.ToolCallContext{APIKeyID: 7, OrganizationID: 9, Direction: domain.ToolDirectionResponse}
			stream := NewToolCallStream(caller, nil)
			var calls []domain.ToolCall
			for _, chunk := range []byte(test.stream) {
				parsed, err := stream.WriteSSE([]byte{chunk}, false)
				if err != nil {
					t.Fatal(err)
				}
				calls = append(calls, parsed...)
			}
			parsed, err := stream.WriteSSE(nil, true)
			calls = append(calls, parsed...)
			if err != nil || len(calls) != test.count {
				t.Fatalf("calls=%+v err=%v", calls, err)
			}
			var shell domain.ToolCall
			for _, call := range calls {
				if call.ToolCallContext != caller {
					t.Fatalf("wrong caller: %+v", call)
				}
				if call.Name == "shell" {
					shell = call
				}
			}
			if !strings.Contains(string(shell.Arguments), "rm -rf /") {
				t.Fatalf("incomplete arguments: %s", shell.Arguments)
			}
		})
	}
}

func TestToolStreamHandlesIncompleteOversizedAndMultilineEvents(t *testing.T) {
	stream := NewToolCallStream(domain.ToolCallContext{Direction: domain.ToolDirectionResponse}, nil)
	_, err := stream.WriteSSE([]byte("data: "+`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"lookup","arguments":"{"}}]}}]}`+"\n\n"), true)
	if err == nil {
		t.Fatal("incomplete arguments accepted")
	}
	stream = NewToolCallStream(domain.ToolCallContext{}, nil)
	_, err = stream.WriteSSE([]byte("data: "+strings.Repeat("x", maxToolPayload+1)), false)
	if err == nil || !stream.failed || len(stream.buffer) != 0 {
		t.Fatal("oversized event retained")
	}
	stream = NewToolCallStream(domain.ToolCallContext{}, nil)
	calls, err := stream.WriteSSE([]byte("data: {\"content\": [\ndata: {\"type\":\"tool_use\",\"id\":\"c\",\"name\":\"lookup\",\"input\":{}}]}"), true)
	if err != nil || len(calls) != 1 {
		t.Fatalf("multiline final event calls=%+v err=%v", calls, err)
	}
}
