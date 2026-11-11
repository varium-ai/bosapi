package infrastructure

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (q RelayRequest) validateChat() error {
	if _, exists := q.Controls["top_k"]; exists {
		return conversionError("top_k", "openai chat has no equivalent sampling control")
	}
	pending := map[string]bool{}
	for index, message := range q.Messages {
		if len(pending) > 0 && message.Role != "user" {
			return conversionError(fmt.Sprintf("messages[%d].role", index), "pending tool invocations require a user result turn")
		}
		for _, block := range message.Blocks {
			path := fmt.Sprintf("messages[%d].content", index)
			if block.Type == "tool_result" {
				if !pending[block.ID] {
					return conversionError(path, "tool results must immediately follow their assistant invocation")
				}
				delete(pending, block.ID)
				continue
			}
			if len(pending) > 0 && block.Type != "tool_use" {
				return conversionError(path, "all pending tool results must precede conversation content")
			}
			if block.Type == "tool_use" {
				pending[block.ID] = true
			}
		}
	}
	if len(pending) > 0 {
		return conversionError("messages", "historical tool invocations require matching results")
	}
	if len(q.Messages) == 0 || q.Messages[len(q.Messages)-1].Role == "assistant" {
		return conversionError("messages", "openai chat cannot preserve assistant prefill")
	}
	return nil
}

func (q RelayRequest) chatUpstream() ([]byte, string, error) {
	if err := q.validateChat(); err != nil {
		return nil, "", err
	}
	messages := []any{}
	if len(q.System) > 0 {
		messages = append(messages, map[string]any{"role": "system", "content": strings.Join(q.System, "\n\n")})
	}
	for _, message := range q.Messages {
		converted, err := chatMessages(message)
		if err != nil {
			return nil, "", err
		}
		messages = append(messages, converted...)
	}
	root := map[string]any{"model": q.Model, "messages": messages, "stream": q.Stream}
	for key, value := range q.Controls {
		root[key] = value
	}
	if q.OutputSchema != nil {
		root["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name": "response", "strict": true, "schema": q.OutputSchema,
			},
		}
	}
	if q.Stream {
		// Messages requires terminal usage even when the client did not request it.
		root["stream_options"] = map[string]any{"include_usage": true}
	}
	tools := []any{}
	for _, tool := range q.Tools {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Schema}})
	}
	if len(tools) > 0 {
		root["tools"] = tools
	}
	if len(tools) > 0 || q.Choice != "auto" {
		choice := any(q.Choice)
		switch q.Choice {
		case "any":
			choice = "required"
		case "tool":
			choice = map[string]any{"type": "function", "function": map[string]any{"name": q.ChoiceName}}
		}
		root["tool_choice"] = choice
		if q.DisableParallel {
			root["parallel_tool_calls"] = false
		}
	}
	payload, err := json.Marshal(root)
	return payload, "chat/completions", err
}

func chatMessages(message RelayMessage) ([]any, error) {
	result := []any{}
	builder := &chatHistoryBuilder{role: message.Role}
	flush := func() {
		if value := builder.flush(); value != nil {
			result = append(result, value)
		}
	}
	for _, block := range message.Blocks {
		if block.Type == "tool_result" {
			flush()
			content := block.Text
			if block.IsError {
				content = "Tool error:\n" + content
			}
			result = append(result, map[string]any{"role": "tool", "tool_call_id": block.ID, "content": content})
			continue
		}
		if err := builder.append(block); err != nil {
			return nil, err
		}
	}
	flush()
	return result, nil
}

func (reply *RelayReply) observeChatUsage(value map[string]any) {
	usage, ok := value["usage"].(map[string]any)
	if !ok || ValidateChatRelayUsage(value) != nil {
		return
	}
	reply.HasUsage = true
	details, _ := usage["prompt_tokens_details"].(map[string]any)
	reply.CacheRead = relayInt(details["cached_tokens"])
	reply.Input = relayInt(usage["prompt_tokens"]) - reply.CacheRead
	reply.Output = relayInt(usage["completion_tokens"])
}

// ChatRelayStop rejects unknown reasons so an incomplete turn never becomes a success.
func ChatRelayStop(reason string, tools bool) (string, error) {
	switch reason {
	case "stop", "length", "content_filter":
		if tools {
			return "", conversionError("response.finish_reason", "tool invocations require tool_calls finish reason")
		}
		return reason, nil
	case "tool_calls":
		if tools {
			return reason, nil
		}
	}
	return "", conversionError("response.finish_reason", "unsupported or incomplete Chat finish reason")
}

// ChatRelayMessage validates executable response fields before protocol rendering.
// Empty SDK metadata is harmless; executable legacy calls still require native Chat.
func ChatRelayMessage(message map[string]any) error {
	if err := fieldsOnly(message, "response.message.", "role", "content", "tool_calls", "refusal", "audio", "annotations", "function_call", "reasoning_content", "reasoning"); err != nil {
		return err
	}
	if role, exists := message["role"]; exists && role != "assistant" {
		return conversionError("response.message.role", "expected assistant role")
	}
	if message["function_call"] != nil {
		return conversionError("response.message.function_call", "legacy function calls require the native upstream protocol")
	}
	if _, err := ChatRelayReasoning(message); err != nil {
		return err
	}
	for _, name := range []string{"refusal", "audio", "annotations"} {
		value := message[name]
		if value == nil || value == "" {
			continue
		}
		if list, ok := value.([]any); ok && len(list) == 0 {
			continue
		}
		return conversionError("response.message."+name, "this capability requires the native upstream protocol")
	}
	return nil
}

// ValidateChatRelayUsage prevents malformed upstream counts from becoming billing
// totals. Missing usage is allowed until the final Messages response is rendered.
func ValidateChatRelayUsage(root map[string]any) error {
	value := root["usage"]
	if value == nil {
		return nil
	}
	usage, ok := value.(map[string]any)
	if !ok {
		return conversionError("response.usage", "expected usage metrics")
	}
	for _, name := range []string{"prompt_tokens", "completion_tokens"} {
		if err := validateChatTokenCount(usage[name], "response.usage."+name); err != nil {
			return err
		}
	}
	if relayInt(usage["prompt_tokens"]) > (1<<63-1)-relayInt(usage["completion_tokens"]) {
		return conversionError("response.usage", "total token count exceeds the supported range")
	}
	if value := usage["prompt_tokens_details"]; value != nil {
		details, ok := value.(map[string]any)
		if !ok {
			return conversionError("response.usage.prompt_tokens_details", "expected token details")
		}
		if value, exists := details["cached_tokens"]; exists {
			if err := validateChatTokenCount(value, "response.usage.prompt_tokens_details.cached_tokens"); err != nil {
				return err
			}
			if relayInt(value) > relayInt(usage["prompt_tokens"]) {
				return conversionError("response.usage.prompt_tokens_details.cached_tokens", "cached tokens exceed prompt tokens")
			}
		}
	}
	return nil
}

func validateChatTokenCount(value any, path string) error {
	number, ok := value.(json.Number)
	if !ok {
		return conversionError(path, "expected a non-negative integer token count")
	}
	count, err := number.Int64()
	if err != nil || count < 0 {
		return conversionError(path, "expected a non-negative integer token count")
	}
	return nil
}

func parseChatReply(raw []byte, id string) (RelayReply, error) {
	reply := RelayReply{ID: id}
	root, err := DecodeProtocolJSON(raw)
	if err != nil {
		return reply, err
	}
	if root["error"] != nil {
		return reply, conversionError("response.error", "upstream returned an error")
	}
	if err := ValidateChatRelayUsage(root); err != nil {
		return reply, err
	}
	choices, ok := root["choices"].([]any)
	if !ok || len(choices) != 1 {
		return reply, conversionError("response.choices", "expected one Chat choice")
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return reply, conversionError("response.choices", "invalid Chat choice")
	}
	message, ok := choice["message"].(map[string]any)
	if !ok {
		return reply, conversionError("response.message", "expected an assistant message")
	}
	if err := ChatRelayMessage(message); err != nil {
		return reply, err
	}
	blocks, err := textBlocks(message["content"], "response.message.content", false, false)
	if err != nil {
		return reply, err
	}
	reply.Blocks, err = ChatRelayReasoning(message)
	if err != nil {
		return reply, err
	}
	reply.Blocks = append(reply.Blocks, blocks...)
	calls, err := ChatRelayToolCalls(message["tool_calls"])
	if err != nil {
		return reply, err
	}
	reply.Blocks = append(reply.Blocks, calls...)
	if value, ok := root["id"].(string); ok && value != "" {
		reply.ID = value
	}
	reply.ObserveUsage(root, "openai_chat")
	reason, _ := choice["finish_reason"].(string)
	reply.Stop, err = ChatRelayStop(reason, len(calls) > 0)
	return reply, err
}

// ChatRelayToolCalls shares invocation validation between JSON and assembled SSE
// calls so duplicate IDs or malformed arguments cannot bypass delivery checks.
func ChatRelayToolCalls(value any) ([]RelayBlock, error) {
	if value == nil {
		return nil, nil
	}
	calls, ok := value.([]any)
	if !ok || len(calls) > 128 {
		return nil, conversionError("response.tool_calls", "expected at most 128 function calls")
	}
	blocks := make([]RelayBlock, 0, len(calls))
	seen := map[string]bool{}
	for _, value := range calls {
		block, err := parseChatCall(value)
		if err != nil {
			return nil, err
		}
		if seen[block.ID] {
			return nil, conversionError("response.tool_calls.id", "duplicate invocation ID")
		}
		seen[block.ID] = true
		blocks = append(blocks, block)
	}
	return blocks, nil
}

func parseChatCall(value any) (RelayBlock, error) {
	block := RelayBlock{Type: "tool_use"}
	call, ok := value.(map[string]any)
	if !ok || call["type"] != "function" {
		return block, conversionError("response.tool_calls", "expected a function call")
	}
	if err := fieldsOnly(call, "response.tool_calls.", "id", "type", "function"); err != nil {
		return block, err
	}
	function, ok := call["function"].(map[string]any)
	if !ok {
		return block, conversionError("response.tool_calls.function", "expected a function")
	}
	if err := fieldsOnly(function, "response.tool_calls.function.", "name", "arguments"); err != nil {
		return block, err
	}
	var err error
	block.ID, err = requiredString(call["id"], "response.tool_calls.id")
	if err != nil {
		return block, err
	}
	block.Name, _ = function["name"].(string)
	if !relayFunctionName(block.Name, "openai_chat") {
		return block, conversionError("response.tool_calls.function.name", "invalid function name")
	}
	args, ok := function["arguments"].(string)
	if !ok {
		return block, conversionError("response.tool_calls.function.arguments", "expected JSON object arguments")
	}
	block.Input, err = DecodeProtocolJSON([]byte(args))
	if err != nil {
		return block, conversionError("response.tool_calls.function.arguments", "expected JSON object arguments")
	}
	return block, nil
}
