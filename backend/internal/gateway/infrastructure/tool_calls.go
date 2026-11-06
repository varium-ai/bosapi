package infrastructure

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"bosapi/backend/internal/gateway/domain"
)

const (
	maxToolPayload = 20 << 20
	maxToolCalls   = 128
)

// ToolCallParser recognizes protocol positions, not arbitrary keys inside user
// text, tool definitions, arguments or results. MCP is an internal semantic only.
type ToolCallParser struct {
	caller          domain.ToolCallContext
	known           map[string]domain.ToolCall
	knownBytes      int
	maxPayloadBytes int64
}

// NewToolCallParser bounds inspection using an optional request payload limit.
func NewToolCallParser(caller domain.ToolCallContext, known []domain.ToolCall, limits ...int64) *ToolCallParser {
	limit := int64(maxToolPayload)
	if len(limits) > 0 && limits[0] > 0 {
		limit = limits[0]
	}
	parser := &ToolCallParser{maxPayloadBytes: limit, caller: caller, known: make(map[string]domain.ToolCall)}
	for _, call := range known {
		parser.remember(call)
	}
	return parser
}

func (parser *ToolCallParser) invalid(protocol domain.ToolProtocol) error {
	return &domain.ToolCallParseError{Protocol: protocol, Direction: parser.caller.Direction}
}

func (parser *ToolCallParser) remember(call domain.ToolCall) {
	if call.ID != "" && call.Kind == domain.ToolCallInvocation {
		key := toolCorrelationKey(call)
		cost := len(call.ID) + len(call.Name) + len(call.Arguments)
		if int64(cost) > parser.maxPayloadBytes {
			return
		}
		previous := parser.known[key]
		parser.knownBytes -= len(previous.ID) + len(previous.Name) + len(previous.Arguments)
		if len(parser.known) >= maxToolCalls || int64(parser.knownBytes+cost) > parser.maxPayloadBytes {
			parser.known = make(map[string]domain.ToolCall)
			parser.knownBytes = 0
		}
		parser.known[key] = call
		parser.knownBytes += cost
	}
}

func toolCorrelationKey(call domain.ToolCall) string {
	if call.IDIsNumber {
		return string(call.Protocol) + ":number:" + call.ID
	}
	return string(call.Protocol) + ":string:" + call.ID
}

func (parser *ToolCallParser) Parse(payload []byte) ([]domain.ToolCall, error) {
	if int64(len(payload)) > parser.maxPayloadBytes {
		return nil, parser.invalid("")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil || !json.Valid(payload) {
		return nil, parser.invalid("")
	}
	var calls []domain.ToolCall
	if batch, ok := value.([]any); ok {
		// Only JSON-RPC batches have top-level array semantics.
		for _, item := range batch {
			if object(item)["jsonrpc"] == "2.0" {
				if err := parser.envelope(object(item), &calls); err != nil {
					return nil, err
				}
			}
		}
	} else if err := parser.envelope(object(value), &calls); err != nil {
		return nil, err
	}
	if len(calls) > maxToolCalls {
		return nil, parser.invalid("")
	}
	for _, call := range calls {
		parser.remember(call)
	}
	for index := range calls {
		call := &calls[index]
		if call.Kind == domain.ToolCallResult {
			if source, ok := parser.known[toolCorrelationKey(*call)]; ok {
				call.Name = source.Name
				call.Arguments = source.Arguments
			}
		}
	}
	return calls, nil
}

func object(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func stringField(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}

func responseCallID(value map[string]any) string {
	if id := stringField(value, "call_id"); id != "" {
		return id
	}
	return stringField(value, "id")
}

func toolID(value any) string {
	switch id := value.(type) {
	case string:
		return id
	case json.Number:
		return id.String()
	default:
		return ""
	}
}

func toolJSON(value any) json.RawMessage {
	result, _ := json.Marshal(value)
	return result
}

func toolArguments(value any, encoded bool) (json.RawMessage, bool) {
	if encoded {
		if text, ok := value.(string); ok {
			decoder := json.NewDecoder(strings.NewReader(text))
			decoder.UseNumber()
			if !json.Valid([]byte(text)) || decoder.Decode(&value) != nil {
				return nil, false
			}
		} else {
			return nil, false
		}
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, false
	}
	return toolJSON(value), true
}

func (parser *ToolCallParser) invocation(protocol domain.ToolProtocol, id, name string, input any, encoded bool, calls *[]domain.ToolCall) error {
	arguments, valid := toolArguments(input, encoded)
	if strings.TrimSpace(name) == "" || !valid || len(*calls) >= maxToolCalls || (id == "" && protocol != domain.ToolProtocolMCP) {
		return parser.invalid(protocol)
	}
	*calls = append(*calls, domain.ToolCall{ToolCallContext: parser.caller, ID: id, Name: name, Protocol: protocol, Kind: domain.ToolCallInvocation, Arguments: arguments})
	return nil
}

func (parser *ToolCallParser) result(protocol domain.ToolProtocol, id string, value any, isError bool, calls *[]domain.ToolCall) error {
	if id == "" || len(*calls) >= maxToolCalls {
		return parser.invalid(protocol)
	}
	*calls = append(*calls, domain.ToolCall{ToolCallContext: parser.caller, ID: id, Protocol: protocol, Kind: domain.ToolCallResult, Result: toolJSON(value), IsError: isError || isStructuredToolError(value)})
	return nil
}

func (parser *ToolCallParser) envelope(value map[string]any, calls *[]domain.ToolCall) error {
	if value == nil {
		return nil
	}
	if value["jsonrpc"] == "2.0" {
		if value["method"] == "tools/call" {
			params := object(value["params"])
			arguments, exists := params["arguments"]
			if !exists {
				arguments = map[string]any{}
			}
			if err := parser.invocation(domain.ToolProtocolMCP, toolID(value["id"]), stringField(params, "name"), arguments, false, calls); err != nil {
				return err
			}
			_, (*calls)[len(*calls)-1].IDIsNumber = value["id"].(json.Number)
			return nil
		}
		id := toolID(value["id"])
		_, numeric := value["id"].(json.Number)
		identity := domain.ToolCall{Protocol: domain.ToolProtocolMCP, ID: id, IDIsNumber: numeric}
		if source, ok := parser.known[toolCorrelationKey(identity)]; ok && source.Kind == domain.ToolCallInvocation && value["method"] == nil {
			if result, exists := value["result"]; exists {
				isError, _ := object(result)["isError"].(bool)
				if err := parser.result(domain.ToolProtocolMCP, id, result, isError, calls); err != nil {
					return err
				}
				(*calls)[len(*calls)-1].IDIsNumber = numeric
				return nil
			}
			if failure, exists := value["error"]; exists {
				if err := parser.result(domain.ToolProtocolMCP, id, failure, true, calls); err != nil {
					return err
				}
				(*calls)[len(*calls)-1].IDIsNumber = numeric
				return nil
			}
		}
		return nil
	}
	if err := parser.geminiEnvelope(value, calls); err != nil {
		return err
	}
	for _, field := range []string{"messages", "input", "output"} {
		if items, ok := value[field].([]any); ok {
			for _, item := range items {
				if err := parser.message(object(item), calls); err != nil {
					return err
				}
			}
		}
	}
	if choices, ok := value["choices"].([]any); ok {
		for _, choice := range choices {
			if err := parser.message(object(object(choice)["message"]), calls); err != nil {
				return err
			}
		}
	}
	// Responses/Realtime envelopes contain one nested response, not arbitrary recursion.
	if response := object(value["response"]); response != nil {
		for _, field := range []string{"input", "output"} {
			if items, ok := response[field].([]any); ok {
				for _, item := range items {
					if err := parser.message(object(item), calls); err != nil {
						return err
					}
				}
			}
		}
	}
	return parser.message(value, calls)
}

func (parser *ToolCallParser) message(value map[string]any, calls *[]domain.ToolCall) error {
	if value == nil {
		return nil
	}
	if entries, exists := value["tool_calls"]; exists && entries != nil {
		items, ok := entries.([]any)
		if !ok {
			return parser.invalid(domain.ToolProtocolOpenAI)
		}
		for _, item := range items {
			call := object(item)
			if call["type"] != "function" {
				continue
			}
			function := object(call["function"])
			if err := parser.invocation(domain.ToolProtocolOpenAI, stringField(call, "id"), stringField(function, "name"), function["arguments"], true, calls); err != nil {
				return err
			}
		}
	}
	if value["role"] == "tool" {
		if err := parser.result(domain.ToolProtocolOpenAI, stringField(value, "tool_call_id"), value["content"], false, calls); err != nil {
			return err
		}
	}
	if content, ok := value["content"].([]any); ok {
		for _, block := range content {
			if err := parser.item(object(block), calls); err != nil {
				return err
			}
		}
	}
	return parser.item(value, calls)
}

func (parser *ToolCallParser) item(value map[string]any, calls *[]domain.ToolCall) error {
	switch value["type"] {
	case "function_call":
		return parser.invocation(domain.ToolProtocolOpenAI, responseCallID(value), stringField(value, "name"), value["arguments"], true, calls)
	case "function_call_output":
		return parser.result(domain.ToolProtocolOpenAI, stringField(value, "call_id"), value["output"], false, calls)
	case "tool_use":
		return parser.invocation(domain.ToolProtocolAnthropic, stringField(value, "id"), stringField(value, "name"), value["input"], false, calls)
	case "tool_result":
		isError, _ := value["is_error"].(bool)
		return parser.result(domain.ToolProtocolAnthropic, stringField(value, "tool_use_id"), value["content"], isError, calls)
	}
	return nil
}

// Optional Gemini IDs use an internal fallback, without changing wire fields.
func geminiCallID(call map[string]any) string {
	if id := stringField(call, "id"); id != "" {
		return id
	}
	return fmt.Sprintf("implicit-%x", sha256.Sum256(toolJSON([]any{call["name"], call["args"]})))
}
func (parser *ToolCallParser) geminiEnvelope(value map[string]any, calls *[]domain.ToolCall) error {
	var contents []any
	if list, ok := value["contents"].([]any); ok {
		contents = append(contents, list...)
	}
	if list, ok := value["candidates"].([]any); ok {
		for _, candidate := range list {
			contents = append(contents, object(candidate)["content"])
		}
	}
	consumed := map[int]bool{}
	for _, content := range contents {
		parts, _ := object(content)["parts"].([]any)
		for _, entry := range parts {
			part := object(entry)
			if raw, exists := part["functionCall"]; exists {
				call := object(raw)
				if call == nil || call["partialArgs"] != nil || call["willContinue"] == true {
					return parser.invalid(domain.ToolProtocolGemini)
				}
				args := call["args"]
				if args == nil {
					args = map[string]any{}
				}
				if err := parser.invocation(domain.ToolProtocolGemini, geminiCallID(call), stringField(call, "name"), args, false, calls); err != nil {
					return err
				}
			}
			if raw, exists := part["functionResponse"]; exists {
				result := object(raw)
				name := stringField(result, "name")
				id := stringField(result, "id")
				if result == nil || name == "" || object(result["response"]) == nil {
					return parser.invalid(domain.ToolProtocolGemini)
				}
				if id != "" {
					for index, call := range *calls {
						if call.Protocol == domain.ToolProtocolGemini && call.Kind == domain.ToolCallInvocation && call.ID == id {
							if call.Name != name {
								return parser.invalid(domain.ToolProtocolGemini)
							}
							consumed[index] = true
						}
					}
				}
				if id == "" {
					for index, call := range *calls {
						if call.Protocol == domain.ToolProtocolGemini && call.Kind == domain.ToolCallInvocation && call.Name == name && !consumed[index] {
							id = call.ID
							consumed[index] = true
							break
						}
					}
				}
				if err := parser.result(domain.ToolProtocolGemini, id, result["response"], false, calls); err != nil {
					return err
				}
				(*calls)[len(*calls)-1].Name = name
			}
		}
	}
	return nil
}
