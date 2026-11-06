package infrastructure

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"bosapi/backend/internal/gateway/domain"
)

func deliveryTestFilter(t *testing.T) *ToolDeliveryFilter {
	t.Helper()
	return NewToolDeliveryFilter(domain.ToolCallContext{OrganizationID: 9, Direction: domain.ToolDirectionResponse}, nil, func(call domain.ToolCall) bool {
		return !bytes.Contains(ToolCallInspectionPayload(call), []byte("danger"))
	}, func(error) {})
}

func TestToolDeliveryJSONPreservesTextAndAllowedCalls(t *testing.T) {
	for _, test := range []struct{ name, payload string }{
		{"chat", `{"choices":[{"index":0,"message":{"content":"normal text","tool_calls":[{"id":"bad","type":"function","function":{"name":"shell","arguments":"{\"command\":\"danger\"}"}},{"id":"good","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":17}}`},
		{"responses", `{"output":[{"type":"function_call","call_id":"bad","name":"shell","arguments":"{\"command\":\"danger\"}"},{"type":"function_call","call_id":"good","name":"lookup","arguments":"{}"},{"type":"message","content":[{"type":"output_text","text":"normal text"}]}],"usage":{"total_tokens":17}}`},
		{"anthropic", `{"content":[{"type":"tool_use","id":"bad","name":"shell","input":{"command":"danger"}},{"type":"tool_use","id":"good","name":"lookup","input":{"account_id":9007199254740993}},{"type":"text","text":"normal text"}],"stop_reason":"tool_use","usage":{"total_tokens":17}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := deliveryTestFilter(t).JSON([]byte(test.payload))
			if !json.Valid(result) || bytes.Contains(result, []byte("danger")) || !bytes.Contains(result, []byte(`"good"`)) || !bytes.Contains(result, []byte("normal text")) || !bytes.Contains(result, []byte("17")) {
				t.Fatalf("invalid output: %s", result)
			}
			if test.name == "anthropic" && !bytes.Contains(result, []byte("9007199254740993")) {
				t.Fatal("allowed argument lost integer precision")
			}
			if test.name == "chat" && !bytes.Contains(result, []byte(`"finish_reason":"tool_calls"`)) {
				t.Fatal("allowed call lost its finish reason")
			}
		})
	}
}

func TestToolDeliveryObservePreservesArguments(t *testing.T) {
	payload := []byte(`{"choices":[{"message":{"tool_calls":[{"id":"c","type":"function","function":{"name":"shell","arguments":"{\"command\":\"danger\"}"}}]}}]}`)
	filter := NewToolDeliveryFilter(domain.ToolCallContext{}, nil, func(domain.ToolCall) bool { return true }, nil)
	if result := filter.JSON(payload); !bytes.Equal(result, payload) {
		t.Fatalf("observe changed parameters: %s", result)
	}
}

func TestToolDeliveryChatSSEWithholdsSplitArgumentsAndReindexes(t *testing.T) {
	filter := deliveryTestFilter(t)
	start := `data: {"id":"r","choices":[{"index":0,"delta":{"content":"normal text","tool_calls":[{"index":0,"id":"bad","type":"function","function":{"name":"shell","arguments":"{\"command\":\"dan"}},{"index":1,"id":"good","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}` + "\n\n"
	if result := filter.SSE([]byte(start), false); len(result) != 0 {
		t.Fatalf("unverified arguments escaped: %s", bytes.Join(result, nil))
	}
	end := `data: {"id":"r","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ger\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	var output [][]byte
	for _, character := range []byte(end) {
		output = append(output, filter.SSE([]byte{character}, false)...)
	}
	output = append(output, filter.SSE(nil, true)...)
	result := string(bytes.Join(output, nil))
	if strings.Contains(result, "danger") || !strings.Contains(result, `"good"`) || !strings.Contains(result, "normal text") || !strings.Contains(result, "[DONE]") {
		t.Fatalf("invalid stream: %s", result)
	}
	if !strings.Contains(result, `"index":0,"type":"function"`) {
		t.Fatalf("surviving tool index was not repaired: %s", result)
	}
}

func TestToolDeliveryResponsesRemovesDeltasAndTerminalDuplicates(t *testing.T) {
	filter := deliveryTestFilter(t)
	frames := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"item","type":"function_call","call_id":"bad","name":"shell","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"item","output_index":0,"delta":"{\"command\":\"danger\"}"}`,
		`{"type":"response.function_call_arguments.done","item_id":"item","output_index":0,"arguments":"{\"command\":\"danger\"}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"item","type":"function_call","call_id":"bad","name":"shell","arguments":"{\"command\":\"danger\"}"}}`,
		`{"type":"response.output_text.delta","output_index":1,"delta":"normal text"}`,
		`{"type":"response.completed","response":{"output":[{"id":"item","type":"function_call","call_id":"bad","name":"shell","arguments":"{\"command\":\"danger\"}"},{"type":"message","content":[{"type":"output_text","text":"normal text"}]}]}}`,
	}
	var output [][]byte
	for index, frame := range frames {
		result := filter.Frame([]byte(frame))
		if index < len(frames)-1 && len(result) != 0 {
			t.Fatal("tool was delivered before terminal verification")
		}
		output = append(output, result...)
	}
	result := string(bytes.Join(output, nil))
	if strings.Contains(result, `"type":"function_call"`) || strings.Contains(result, "danger") || !strings.Contains(result, "normal text") {
		t.Fatalf("invalid response stream: %s", result)
	}
}

func TestToolDeliveryRechecksChangedAuthoritativeArguments(t *testing.T) {
	filter := deliveryTestFilter(t)
	frames := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"item","type":"function_call","call_id":"bad","name":"shell","arguments":""}}`,
		`{"type":"response.function_call_arguments.done","item_id":"item","arguments":"{}"}`,
		`{"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"bad","name":"shell","arguments":"{\"command\":\"danger\"}"}]}}`,
	}
	var output [][]byte
	for _, frame := range frames {
		output = append(output, filter.Frame([]byte(frame))...)
	}
	if result := string(bytes.Join(output, nil)); strings.Contains(result, `"type":"function_call"`) || strings.Contains(result, "danger") {
		t.Fatalf("terminal output bypassed the policy: %s", result)
	}
}

func TestToolDeliveryAnthropicRepairsBlockIndicesAndStopReason(t *testing.T) {
	filter := deliveryTestFilter(t)
	frames := []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"bad","name":"shell","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"danger\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"normal text"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`{"type":"message_stop"}`,
	}
	var output [][]byte
	for _, frame := range frames {
		output = append(output, filter.Frame([]byte(frame))...)
	}
	result := string(bytes.Join(output, nil))
	if strings.Contains(result, `"type":"tool_use"`) || strings.Contains(result, "danger") || !strings.Contains(result, "normal text") || !strings.Contains(result, "end_turn") {
		t.Fatalf("invalid Anthropic stream: %s", result)
	}
}

func TestToolDeliveryMalformedCallAndOverflowNeverLeak(t *testing.T) {
	for _, test := range []struct{ name, argument string }{{"malformed", "PRIVATE_INVALID"}, {"overflow", strings.Repeat("x", maxToolPayload)}} {
		t.Run(test.name, func(t *testing.T) {
			filter := deliveryTestFilter(t)
			frame := toolJSON(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "normal text", "tool_calls": []any{map[string]any{"index": 0, "id": "bad", "type": "function", "function": map[string]any{"name": "shell", "arguments": test.argument}}}}, "finish_reason": "tool_calls"}}})
			output := filter.Frame(frame)
			output = append(output, filter.Finish()...)
			result := string(bytes.Join(output, nil))
			if strings.Contains(result, "tool_calls") || strings.Contains(result, "PRIVATE") || !strings.Contains(result, "normal text") {
				t.Fatalf("unsafe output: %.200s", result)
			}
		})
	}
}

func TestToolDeliverySSEHandlesHeadersCRLFAndUnterminatedEvents(t *testing.T) {
	filter := deliveryTestFilter(t)
	stream := ": keepalive\r\n\r\nevent: response.completed\r\nid: event-1\r\ndata: {\"response\":\r\ndata: {\"output\":[{\"type\":\"function_call\",\"call_id\":\"bad\",\"name\":\"danger\",\"arguments\":\"{}\"},{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"normal text\"}]}]}}"
	result := bytes.Join(filter.SSE([]byte(stream), true), nil)
	if bytes.Contains(result, []byte("danger")) || bytes.Contains(result, []byte(`"type":"function_call"`)) || !bytes.Contains(result, []byte("normal text")) || !bytes.Contains(result, []byte("keepalive")) || !bytes.Contains(result, []byte("event: response.completed")) {
		t.Fatalf("invalid SSE: %s", result)
	}
}

func TestToolDeliveryBoundsUnterminatedSSEAndRejectsInvalidFrames(t *testing.T) {
	failures := 0
	filter := NewToolDeliveryFilter(domain.ToolCallContext{}, nil, func(domain.ToolCall) bool { return true }, func(error) { failures++ })
	if output := filter.SSE(bytes.Repeat([]byte("x"), maxToolPayload+1), false); len(output) != 0 || len(filter.buffer) != 0 || failures != 1 {
		t.Fatal("unbounded incomplete event retained")
	}
	if output := filter.Frame([]byte("PRIVATE_INVALID_FRAME")); len(output) != 0 {
		t.Fatal("invalid frame leaked")
	}
	if output := filter.JSON([]byte("PRIVATE_INVALID_JSON")); len(output) != 0 {
		t.Fatal("invalid JSON leaked")
	}
	output := bytes.Join(filter.Frame([]byte(`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"normal text"}]}]}}`)), nil)
	if !bytes.Contains(output, []byte("normal text")) {
		t.Fatal("parse failure stopped ordinary output")
	}
}

func TestToolDeliveryAllBlockedCallsFinishNormally(t *testing.T) {
	for _, payload := range []string{
		`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"bad","type":"function","function":{"name":"danger","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"content":[{"type":"tool_use","id":"bad","name":"danger","input":{}}],"stop_reason":"tool_use"}`,
	} {
		result := deliveryTestFilter(t).JSON([]byte(payload))
		if !json.Valid(result) || bytes.Contains(result, []byte("danger")) || bytes.Contains(result, []byte("tool_calls")) || bytes.Contains(result, []byte(`"type":"tool_use"`)) {
			t.Fatalf("all-blocked response did not terminate safely: %s", result)
		}
	}
}

func TestToolDeliveryKeepsBoundedCorrelationAcrossTurns(t *testing.T) {
	var observed []domain.ToolCall
	filter := NewToolDeliveryFilter(domain.ToolCallContext{Direction: domain.ToolDirectionResponse}, nil, func(call domain.ToolCall) bool { observed = append(observed, call); return true }, nil)
	filter.Frame([]byte(`{"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"c","name":"lookup","arguments":"{}"}]}}`))
	filter.Frame([]byte(`{"type":"function_call_output","call_id":"c","output":"result"}`))
	if len(observed) != 2 || observed[1].Kind != domain.ToolCallResult || observed[1].Name != "lookup" {
		t.Fatalf("correlation lost: %+v", observed)
	}
	filter.Remember([]domain.ToolCall{{ID: "history", Name: "other", Protocol: domain.ToolProtocolOpenAI, Kind: domain.ToolCallInvocation, Arguments: json.RawMessage(`{}`)}})
	filter.Frame([]byte(`{"type":"function_call_output","call_id":"history","output":"result"}`))
	if observed[len(observed)-1].Name != "other" {
		t.Fatal("request history correlation lost")
	}
}
