package infrastructure

import (
	"bytes"
	"encoding/json"
	"testing"

	"bosapi/backend/internal/gateway/domain"
)

func assertToolError(t *testing.T, content any) {
	t.Helper()
	text, ok := content.(string)
	if !ok {
		t.Fatalf("error content is not a string: %v", content)
	}
	var envelope struct {
		Error domain.ToolExecutionError `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &envelope); err != nil || envelope.Error != domain.UnauthorizedToolError() {
		t.Fatalf("invalid structured error: %s", text)
	}
}

func TestRejectedCallsReturnCorrelatedProtocolErrors(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		protocol      domain.ToolProtocol
	}{
		{"chat", `{"choices":[{"index":0,"message":{"role":"assistant","content":"ordinary","tool_calls":[{"id":"bad","type":"function","function":{"name":"shell","arguments":"{\"command\":\"danger\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":17}}`, domain.ToolProtocolOpenAI},
		{"responses", `{"output":[{"type":"function_call","id":"item","call_id":"bad","name":"shell","arguments":"{\"command\":\"danger\"}"}],"usage":{"total_tokens":17}}`, domain.ToolProtocolOpenAI},
		{"anthropic", `{"content":[{"type":"tool_use","id":"bad","name":"shell","input":{"command":"danger"}}],"stop_reason":"tool_use","usage":{"total_tokens":17}}`, domain.ToolProtocolAnthropic},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := deliveryTestFilter(t).JSON([]byte(test.payload))
			value, valid := deliveryObject(payload)
			if !valid || bytes.Contains(payload, []byte("danger")) || bytes.Contains(payload, []byte("<tool")) {
				t.Fatalf("unsafe result: %s", payload)
			}
			var result map[string]any
			switch test.name {
			case "chat":
				choices := value["choices"].([]any)
				result = object(object(choices[1])["message"])
				if result["role"] != "tool" || result["tool_call_id"] != "bad" {
					t.Fatalf("invalid tool message: %v", result)
				}
				assertToolError(t, result["content"])
			case "responses":
				result = object(value["output"].([]any)[0])
				if result["type"] != "function_call_output" || result["call_id"] != "bad" || result["id"] != "item" {
					t.Fatalf("invalid tool output: %v", result)
				}
				assertToolError(t, result["output"])
			case "anthropic":
				result = object(value["content"].([]any)[0])
				if result["type"] != "tool_result" || result["tool_use_id"] != "bad" || result["is_error"] != true || value["stop_reason"] != "end_turn" {
					t.Fatalf("invalid tool result: %v", result)
				}
				assertToolError(t, result["content"])
			}
			calls, err := NewToolCallParser(domain.ToolCallContext{}, nil).Parse(payload)
			if err != nil || len(calls) != 1 || calls[0].Kind != domain.ToolCallResult || !calls[0].IsError || calls[0].ID != "bad" || calls[0].Protocol != test.protocol {
				t.Fatalf("semantic round trip failed: calls=%+v err=%v", calls, err)
			}
		})
	}
}

func TestMCPUnauthorizedToolErrorPreservesTypedIDsAndBatch(t *testing.T) {
	filter := NewToolDeliveryFilter(domain.ToolCallContext{}, nil, func(call domain.ToolCall) bool { return call.IDIsNumber }, nil)
	batch := []byte(`[{"jsonrpc":"2.0","id":9007199254740993,"method":"tools/call","params":{"name":"shell","arguments":{}}},{"jsonrpc":"2.0","id":"9007199254740993","method":"tools/call","params":{"name":"shell","arguments":{}}}]`)
	result := filter.JSON(batch)
	var values []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.UseNumber()
	if err := decoder.Decode(&values); err != nil || len(values) != 2 {
		t.Fatalf("invalid batch: %s", result)
	}
	if values[0]["method"] != "tools/call" || values[0]["id"] != json.Number("9007199254740993") {
		t.Fatalf("allowed numeric ID changed: %v", values[0])
	}
	if values[1]["method"] != nil || values[1]["params"] != nil || values[1]["id"] != "9007199254740993" || object(values[1]["error"])["code"] != json.Number("-32601") {
		t.Fatalf("invalid MCP error: %v", values[1])
	}
	for _, id := range []string{`1`, `"1"`, `9007199254740993`, `null`} {
		filter := NewToolDeliveryFilter(domain.ToolCallContext{}, nil, func(domain.ToolCall) bool { return false }, nil)
		result := filter.JSON([]byte(`{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"shell","arguments":{"private":"danger"}}}`))
		if !bytes.Contains(result, []byte(`"id":`+id)) || bytes.Contains(result, []byte("danger")) || !bytes.Contains(result, []byte(`"code":-32601`)) {
			t.Fatalf("invalid result: %s", result)
		}
	}
	notification := NewToolDeliveryFilter(domain.ToolCallContext{}, nil, func(domain.ToolCall) bool { return false }, nil).JSON([]byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"shell"}}`))
	if len(notification) != 0 {
		t.Fatalf("notification produced a reply: %s", notification)
	}
}

func TestResponsesFallbackIdentityCannotBypassRejection(t *testing.T) {
	for _, payload := range []string{
		`{"type":"function_call","id":"fallback","name":"danger","arguments":"{}"}`,
		`{"output":[{"type":"function_call","id":"fallback","name":"danger","arguments":"{}"}]}`,
		`{"type":"response.output_item.done","item":{"type":"function_call","id":"fallback","name":"danger","arguments":"{}"}}`,
	} {
		filter := deliveryTestFilter(t)
		var result []byte
		if bytes.Contains([]byte(payload), []byte("response.output_item.done")) {
			output := filter.Frame([]byte(payload))
			output = append(output, filter.Finish()...)
			result = bytes.Join(output, nil)
		} else {
			result = filter.JSON([]byte(payload))
		}
		if bytes.Contains(result, []byte("danger")) || bytes.Contains(result, []byte(`"type":"function_call"`)) || !bytes.Contains(result, []byte(`"call_id":"fallback"`)) || !bytes.Contains(result, []byte("unauthorized_tool")) {
			t.Fatalf("fallback identity bypass: %s", result)
		}
	}
}

func TestMCPFramesClearVerdictsBetweenExchanges(t *testing.T) {
	filter := deliveryTestFilter(t)
	for index, name := range []string{"danger", "lookup"} {
		payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":{}}}`)
		output := bytes.Join(filter.Frame(payload), nil)
		if index == 0 && !bytes.Contains(output, []byte(`"code":-32601`)) {
			t.Fatalf("missing rejection: %s", output)
		}
		if index == 1 && !bytes.Equal(output, payload) {
			t.Fatalf("previous verdict contaminated new exchange: %s", output)
		}
	}
}

func TestChatSSEToolErrorEmittedOnceBeforeDone(t *testing.T) {
	filter := deliveryTestFilter(t)
	start := []byte(`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"bad","function":{"name":"shell","arguments":"{\"command\":\"dan"}}]}}]}` + "\n\n")
	if output := filter.SSE(start, false); len(output) != 0 {
		t.Fatal("unverified tool bytes escaped")
	}
	end := []byte(`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ger\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n")
	output := bytes.Join(filter.SSE(end, true), nil)
	if bytes.Count(output, []byte(`"role":"tool"`)) != 1 || bytes.Count(output, []byte(`"tool_call_id":"bad"`)) != 1 || bytes.Contains(output, []byte(`"tool_calls"`)) || bytes.Contains(output, []byte("danger")) || !bytes.Contains(output, []byte("unauthorized_tool")) {
		t.Fatalf("invalid stream: %s", output)
	}
	if bytes.Index(output, []byte("unauthorized_tool")) > bytes.Index(output, []byte("[DONE]")) {
		t.Fatal("tool error followed stream termination")
	}
}
