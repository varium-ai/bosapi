package infrastructure

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type RelayReply struct {
	ID                                             string
	Blocks                                         []RelayBlock
	Stop, StopSequence                             string
	Input, Output, CacheRead, CacheWrite, Thoughts int64
	HasUsage                                       bool
}

func relayInt(value any) int64 {
	if n, ok := value.(json.Number); ok {
		result, _ := n.Int64()
		return result
	}
	if n, ok := value.(float64); ok {
		return int64(n)
	}
	return 0
}
func (reply *RelayReply) ObserveUsage(value map[string]any, format string) {
	if format == "openai_chat" || format == "" {
		reply.observeChatUsage(value)
		return
	}
	if format == "anthropic_messages" {
		usage, ok := value["usage"].(map[string]any)
		if !ok {
			if message, ok := value["message"].(map[string]any); ok {
				usage, _ = message["usage"].(map[string]any)
			}
		}
		if usage == nil {
			return
		}
		reply.HasUsage = true
		if _, exists := usage["input_tokens"]; exists {
			reply.Input = relayInt(usage["input_tokens"])
		}
		if _, exists := usage["output_tokens"]; exists {
			reply.Output = relayInt(usage["output_tokens"])
		}
		if _, exists := usage["cache_read_input_tokens"]; exists {
			reply.CacheRead = relayInt(usage["cache_read_input_tokens"])
		}
		if _, exists := usage["cache_creation_input_tokens"]; exists {
			reply.CacheWrite = relayInt(usage["cache_creation_input_tokens"])
		}
	} else if usage, ok := value["usageMetadata"].(map[string]any); ok {
		reply.HasUsage = true
		reply.CacheRead = relayInt(usage["cachedContentTokenCount"])
		reply.Input = max(int64(0), relayInt(usage["promptTokenCount"])-reply.CacheRead)
		reply.Thoughts = relayInt(usage["thoughtsTokenCount"])
		reply.Output = relayInt(usage["candidatesTokenCount"]) + reply.Thoughts
	}
}
func (reply RelayReply) ChatUsage() map[string]any {
	input := reply.Input + reply.CacheRead + reply.CacheWrite
	usage := map[string]any{"prompt_tokens": input, "completion_tokens": reply.Output, "total_tokens": input + reply.Output}
	if reply.CacheRead > 0 {
		usage["prompt_tokens_details"] = map[string]any{"cached_tokens": reply.CacheRead}
	}
	if reply.Thoughts > 0 {
		usage["completion_tokens_details"] = map[string]any{"reasoning_tokens": reply.Thoughts}
	}
	return usage
}
func (reply RelayReply) MessagesUsage() map[string]any {
	return map[string]any{"input_tokens": reply.Input, "output_tokens": reply.Output, "cache_read_input_tokens": reply.CacheRead, "cache_creation_input_tokens": reply.CacheWrite}
}
func mapStop(reason, format string, tools bool) (string, error) {
	if format == "anthropic_messages" {
		switch reason {
		case "end_turn", "stop_sequence":
			return "stop", nil
		case "max_tokens":
			return "length", nil
		case "tool_use":
			return "tool_calls", nil
		case "refusal":
			return "content_filter", nil
		}
	}
	if format == "gemini" {
		switch reason {
		case "STOP":
			if tools {
				return "tool_calls", nil
			}
			return "stop", nil
		case "MAX_TOKENS":
			return "length", nil
		case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
			return "content_filter", nil
		}
	}
	return "", conversionError("response.finish_reason", "upstream finish reason cannot be represented")
}
func (reply RelayReply) MessagesStop() string {
	switch reply.Stop {
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "refusal"
	}
	return "end_turn"
}

func GeminiRelayParts(value map[string]any, id string) ([]RelayBlock, string, error) {
	if feedback, ok := value["promptFeedback"].(map[string]any); ok && feedback["blockReason"] != nil {
		return nil, "", conversionError("response.promptFeedback.blockReason", "upstream blocked the prompt")
	}
	candidates, ok := value["candidates"].([]any)
	if !ok || len(candidates) == 0 {
		return nil, "", nil
	}
	if len(candidates) != 1 {
		return nil, "", conversionError("response.candidates", "multiple candidates cannot be represented")
	}
	candidate, ok := candidates[0].(map[string]any)
	if !ok {
		return nil, "", conversionError("response.candidates", "invalid candidate")
	}
	if err := fieldsOnly(candidate, "response.candidates[0].", "content", "finishReason", "index", "safetyRatings", "tokenCount"); err != nil {
		return nil, "", err
	}
	content, _ := candidate["content"].(map[string]any)
	if content != nil {
		if err := fieldsOnly(content, "response.candidates[0].content.", "role", "parts"); err != nil {
			return nil, "", err
		}
	}
	parts, _ := content["parts"].([]any)
	blocks := []RelayBlock{}
	for index, entry := range parts {
		path := fmt.Sprintf("response.candidates[0].content.parts[%d].", index)
		part, ok := entry.(map[string]any)
		if !ok {
			return nil, "", conversionError(path, "invalid part")
		}
		if err := fieldsOnly(part, path, "text", "functionCall", "thought", "thoughtSignature"); err != nil {
			return nil, "", err
		}
		if value, exists := part["thought"]; exists {
			if _, ok := value.(bool); !ok {
				return nil, "", conversionError(path+"thought", "expected a boolean")
			}
		}
		if part["thought"] == true || part["thoughtSignature"] != nil {
			return nil, "", conversionError(path+"thoughtSignature", "Gemini thinking/signatures require the native Gemini protocol")
		}
		if text, ok := part["text"].(string); ok {
			if part["functionCall"] != nil {
				return nil, "", conversionError(path, "mixed text/function part")
			}
			blocks = append(blocks, RelayBlock{Type: "text", Text: text})
			continue
		}
		call, ok := part["functionCall"].(map[string]any)
		if !ok {
			return nil, "", conversionError(path, "only text and complete function calls are supported")
		}
		if err := fieldsOnly(call, path+"functionCall.", "id", "name", "args"); err != nil {
			return nil, "", err
		}
		name, err := requiredString(call["name"], path+"functionCall.name")
		if err != nil {
			return nil, "", err
		}
		callID, _ := call["id"].(string)
		if value, exists := call["id"]; exists {
			if _, ok := value.(string); !ok {
				return nil, "", conversionError(path+"functionCall.id", "expected a string")
			}
		}
		if callID == "" {
			callID = fmt.Sprintf("%s_call_%d", id, index)
		}
		args := map[string]any{}
		if value, exists := call["args"]; exists {
			var ok bool
			args, ok = value.(map[string]any)
			if !ok {
				return nil, "", conversionError(path+"functionCall.args", "partial/non-object function arguments cannot be preserved")
			}
		}
		blocks = append(blocks, RelayBlock{Type: "tool_use", ID: callID, Name: name, Input: args})
	}
	stop, _ := candidate["finishReason"].(string)
	return blocks, stop, nil
}
func ParseRelayReply(raw []byte, format, id string) (RelayReply, error) {
	if format == "openai_chat" || format == "" {
		return parseChatReply(raw, id)
	}
	reply := RelayReply{ID: id}
	root, err := DecodeProtocolJSON(raw)
	if err != nil {
		return reply, err
	}
	reply.ObserveUsage(root, format)
	reason := ""
	if format == "anthropic_messages" {
		if root["type"] != "message" {
			return reply, conversionError("response.type", "expected an Anthropic message")
		}
		if value, ok := root["id"].(string); ok && value != "" {
			reply.ID = value
		}
		reason, _ = root["stop_reason"].(string)
		reply.StopSequence, _ = root["stop_sequence"].(string)
		blocks, ok := root["content"].([]any)
		if !ok {
			return reply, conversionError("response.content", "expected content blocks")
		}
		for index, entry := range blocks {
			path := fmt.Sprintf("response.content[%d].", index)
			block, ok := entry.(map[string]any)
			if !ok {
				return reply, conversionError(path, "invalid content block")
			}
			if err := ValidateAnthropicRelayBlockMetadata(block, path); err != nil {
				return reply, err
			}
			switch block["type"] {
			case "thinking", "redacted_thinking":
				thinking, err := ParseAnthropicThinking(block, path)
				if err != nil {
					return reply, err
				}
				reply.Blocks = append(reply.Blocks, thinking)
			case "text":
				text, ok := block["text"].(string)
				if !ok {
					return reply, conversionError(path+"text", "expected text")
				}
				reply.Blocks = append(reply.Blocks, RelayBlock{Type: "text", Text: text})
			case "tool_use":
				id, err := requiredString(block["id"], path+"id")
				if err != nil {
					return reply, err
				}
				name, err := requiredString(block["name"], path+"name")
				if err != nil {
					return reply, err
				}
				input, ok := block["input"].(map[string]any)
				if !ok {
					return reply, conversionError(path+"input", "expected object arguments")
				}
				reply.Blocks = append(reply.Blocks, RelayBlock{Type: "tool_use", ID: id, Name: name, Input: input})
			default:
				return reply, conversionError(path+"type", "media and server tools cannot be preserved")
			}
		}
	} else {
		reply.Blocks, reason, err = GeminiRelayParts(root, id)
		if err != nil {
			return reply, err
		}
		if value, ok := root["responseId"].(string); ok && value != "" {
			reply.ID = value
		}
	}
	hasTools := false
	for _, block := range reply.Blocks {
		hasTools = hasTools || block.Type == "tool_use"
	}
	reply.Stop, err = mapStop(reason, format, hasTools)
	return reply, err
}
func (reply RelayReply) JSON(source, model string) ([]byte, error) {
	if source == "messages" {
		if !reply.HasUsage {
			return nil, conversionError("response.usage", "upstream did not provide required usage metrics")
		}
		content := []any{}
		for _, block := range reply.Blocks {
			switch block.Type {
			case "thinking":
				if block.Signature != "" {
					content = append(content, block.AnthropicThinking())
				} else {
					content = append(content, map[string]any{"type": "thinking", "thinking": block.Text, "signature": block.ThinkingSignature()})
				}
			case "redacted_thinking":
				content = append(content, block.AnthropicThinking())
			case "text":
				content = append(content, map[string]any{"type": "text", "text": block.Text})
			default:
				content = append(content, map[string]any{"type": "tool_use", "id": block.ID, "name": block.Name, "input": block.Input})
			}
		}
		return json.Marshal(map[string]any{"id": reply.ID, "type": "message", "role": "assistant", "model": model, "content": content, "stop_reason": reply.MessagesStop(), "stop_sequence": nil, "usage": reply.MessagesUsage()})
	}
	var text strings.Builder
	reasoning := map[string]string{}
	thinking := []any{}
	calls := []any{}
	for _, block := range reply.Blocks {
		switch block.Type {
		case "thinking":
			reasoning[block.ReasoningField] += block.Text
			if block.Signature != "" {
				thinking = append(thinking, block.ChatThinking(text.Len(), len(calls)))
			}
		case "redacted_thinking":
			thinking = append(thinking, block.ChatThinking(text.Len(), len(calls)))
		case "text":
			text.WriteString(block.Text)
		default:
			args, _ := json.Marshal(block.Input)
			calls = append(calls, map[string]any{"id": block.ID, "type": "function", "function": map[string]any{"name": block.Name, "arguments": string(args)}})
		}
	}
	message := map[string]any{"role": "assistant", "content": text.String()}
	if len(thinking) > 0 {
		message["anthropic_thinking"] = thinking
	}
	for field, value := range reasoning {
		message[field] = value
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	root := map[string]any{"id": reply.ID, "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": reply.Stop}}}
	if reply.HasUsage {
		root["usage"] = reply.ChatUsage()
	}
	return json.Marshal(root)
}
