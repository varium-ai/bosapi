package infrastructure

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ConversionError reports the exact capability that cannot cross the boundary.
// Never include caller content or tool arguments in its message.
type ConversionError struct{ Field, Reason string }

func (e *ConversionError) Error() string         { return e.Field + ": " + e.Reason }
func conversionError(field, reason string) error { return &ConversionError{field, reason} }
func fieldsOnly(value map[string]any, path string, allowed ...string) error {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		found := false
		for _, name := range allowed {
			if key == name {
				found = true
				break
			}
		}
		if !found {
			return conversionError(path+key, "unsupported in this protocol conversion")
		}
	}
	return nil
}
func DecodeProtocolJSON(raw []byte) (map[string]any, error) {
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if !json.Valid(raw) || decoder.Decode(&value) != nil || value == nil {
		return nil, conversionError("body", "expected a JSON object")
	}
	return value, nil
}

type RelayBlock struct {
	Type, Text, ID, Name string
	ReasoningField       string
	Signature, Data      string
	Input                map[string]any
	IsError              bool
	MediaType, ImageData string
	URL                  string
}
type RelayMessage struct {
	Role   string
	Blocks []RelayBlock
}
type RelayTool struct {
	Name, Description string
	Schema            map[string]any
}
type RelayRequest struct {
	Model                string
	Stream, IncludeUsage bool
	System               []string
	Messages             []RelayMessage
	Tools                []RelayTool
	Controls             map[string]any
	OutputSchema         map[string]any
	Choice, ChoiceName   string
	DisableParallel      bool
}

func textBlocks(value any, path string, allowImage, allowImageDetail bool) ([]RelayBlock, error) {
	if value == nil {
		return nil, nil
	}
	if text, ok := value.(string); ok {
		return []RelayBlock{{Type: "text", Text: text}}, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, conversionError(path, "only text content is supported")
	}
	result := []RelayBlock{}
	for index, entry := range list {
		block, ok := entry.(map[string]any)
		field := fmt.Sprintf("%s[%d].", path, index)
		if !ok {
			return nil, conversionError(field, "expected a text block")
		}
		if allowImage && block["type"] == "image_url" {
			image, err := relayImageFromURLPart(block, field, allowImageDetail)
			if err != nil {
				return nil, err
			}
			result = append(result, image)
			continue
		}
		if err := fieldsOnly(block, field, "type", "text"); err != nil {
			return nil, err
		}
		text, ok := block["text"].(string)
		if !ok || block["type"] != "text" {
			return nil, conversionError(field+"type", "only text blocks are supported")
		}
		result = append(result, RelayBlock{Type: "text", Text: text})
	}
	return result, nil
}
func relayImageFromURLPart(block map[string]any, path string, allowDetail bool) (RelayBlock, error) {
	if err := fieldsOnly(block, path, "type", "image_url"); err != nil {
		return RelayBlock{}, err
	}
	source, ok := block["image_url"].(map[string]any)
	if !ok {
		return RelayBlock{}, conversionError(path+"image_url", "expected an object")
	}
	allowedFields := []string{"url"}
	if allowDetail {
		allowedFields = append(allowedFields, "detail")
	}
	if err := fieldsOnly(source, path+"image_url.", allowedFields...); err != nil {
		return RelayBlock{}, err
	}
	if detail, exists := source["detail"]; exists && allowDetail {
		switch detail {
		case "auto", "low", "high":
		default:
			return RelayBlock{}, conversionError(path+"image_url.detail", "expected auto, low, or high")
		}
	}
	return relayImageFromURL(source["url"], path+"image_url")
}
func relayImageFromURL(value any, path string) (RelayBlock, error) {
	url, ok := value.(string)
	if !ok || url == "" {
		return RelayBlock{}, conversionError(path, "expected an image URL string")
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return RelayBlock{Type: "image", URL: url}, nil
	}
	if strings.HasPrefix(url, "data:") {
		mediaType, data, found := strings.Cut(strings.TrimPrefix(url, "data:"), ";base64,")
		if !found || mediaType == "" || !strings.HasPrefix(mediaType, "image/") {
			return RelayBlock{}, conversionError(path, "expected an image data URL")
		}
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			return RelayBlock{}, conversionError(path, "expected base64 image data")
		}
		return RelayBlock{Type: "image", MediaType: mediaType, ImageData: data}, nil
	}
	return RelayBlock{}, conversionError(path, "expected a data or http image URL")
}
func relayImageSource(source map[string]any, path string) (RelayBlock, error) {
	switch source["type"] {
	case "base64":
		if err := fieldsOnly(source, path+".", "type", "media_type", "data"); err != nil {
			return RelayBlock{}, err
		}
		mediaType, err := requiredString(source["media_type"], path+".media_type")
		if err != nil {
			return RelayBlock{}, err
		}
		if !strings.HasPrefix(mediaType, "image/") {
			return RelayBlock{}, conversionError(path+".media_type", "expected an image media type")
		}
		data, err := requiredString(source["data"], path+".data")
		if err != nil {
			return RelayBlock{}, err
		}
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			return RelayBlock{}, conversionError(path+".data", "expected base64 image data")
		}
		return RelayBlock{Type: "image", MediaType: mediaType, ImageData: data}, nil
	case "url":
		if err := fieldsOnly(source, path+".", "type", "url"); err != nil {
			return RelayBlock{}, err
		}
		url, err := requiredString(source["url"], path+".url")
		if err != nil {
			return RelayBlock{}, err
		}
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return RelayBlock{}, conversionError(path+".url", "expected an http image URL")
		}
		return RelayBlock{Type: "image", URL: url}, nil
	}
	return RelayBlock{}, conversionError(path+".type", "only base64 and url image sources are supported")
}
func relayImageURL(block RelayBlock) string {
	if block.URL != "" {
		return block.URL
	}
	return "data:" + block.MediaType + ";base64," + block.ImageData
}
func requiredString(value any, path string) (string, error) {
	s, ok := value.(string)
	if !ok || s == "" {
		return "", conversionError(path, "expected a non-empty string")
	}
	return s, nil
}
func boolField(value map[string]any, key string) (bool, error) {
	v, exists := value[key]
	if !exists {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, conversionError(key, "expected a boolean")
	}
	return b, nil
}

// ParseRelayRequest implements a deliberately bounded common subset. Unknown
// fields are rejected at every protocol envelope; JSON schemas stay opaque.
func ParseRelayRequest(raw []byte, source, target string) (RelayRequest, error) {
	q := RelayRequest{Controls: map[string]any{}, Choice: "auto"}
	root, err := DecodeProtocolJSON(raw)
	if err != nil {
		return q, err
	}
	if source == "messages" && (target == "openai_chat" || target == "") {
		if err := q.normalizeChatHints(root); err != nil {
			return q, err
		}
	}
	if source == "chat" && target == "anthropic_messages" {
		if err := q.normalizeAnthropicThinking(root); err != nil {
			return q, err
		}
	}
	allowed := []string{"model", "messages", "stream", "max_tokens", "temperature", "top_p", "tools", "tool_choice"}
	if source == "chat" {
		allowed = append(allowed, "stop", "n", "stream_options", "parallel_tool_calls")
	} else {
		allowed = append(allowed, "system", "stop_sequences", "top_k")
	}
	if err = fieldsOnly(root, "", allowed...); err != nil {
		return q, err
	}
	q.Model, err = requiredString(root["model"], "model")
	if err != nil {
		return q, err
	}
	q.Stream, err = boolField(root, "stream")
	if err != nil {
		return q, err
	}
	for _, name := range []string{"max_tokens", "temperature", "top_p", "top_k"} {
		if value, exists := root[name]; exists {
			number, ok := value.(json.Number)
			if !ok {
				return q, conversionError(name, "expected a number")
			}
			n, err := number.Float64()
			if err != nil {
				return q, conversionError(name, "invalid number")
			}
			if name == "max_tokens" || name == "top_k" {
				if _, err := number.Int64(); err != nil || n <= 0 {
					return q, conversionError(name, "expected a positive integer")
				}
			}
			if name == "temperature" && (n < 0 || n > 2 || target == "anthropic_messages" && n > 1) || name == "top_p" && (n < 0 || n > 1) {
				return q, conversionError(name, "outside the target protocol range")
			}
			q.Controls[name] = value
		}
	}
	if n, exists := root["n"]; exists && n != json.Number("1") {
		return q, conversionError("n", "only one candidate is supported")
	}
	stopKey := "stop"
	if source == "messages" {
		stopKey = "stop_sequences"
	}
	if value, exists := root[stopKey]; exists {
		if text, ok := value.(string); ok && source == "chat" {
			value = []any{text}
		}
		stops, ok := value.([]any)
		if !ok {
			return q, conversionError(stopKey, "expected an array of strings")
		}
		for _, entry := range stops {
			if _, ok := entry.(string); !ok {
				return q, conversionError(stopKey, "expected an array of strings")
			}
		}
		q.Controls["stop"] = stops
	}
	if value, exists := root["stream_options"]; exists {
		options, ok := value.(map[string]any)
		if !ok {
			return q, conversionError("stream_options", "expected an object")
		}
		if err = fieldsOnly(options, "stream_options.", "include_usage"); err != nil {
			return q, err
		}
		q.IncludeUsage, err = boolField(options, "include_usage")
		if err != nil {
			return q, err
		}
		if !q.Stream {
			return q, conversionError("stream_options", "requires stream=true")
		}
	}
	if value, exists := root["parallel_tool_calls"]; exists {
		b, ok := value.(bool)
		if !ok {
			return q, conversionError("parallel_tool_calls", "expected a boolean")
		}
		if !b && target == "gemini" {
			return q, conversionError("parallel_tool_calls", "Gemini cannot guarantee serial tool calls")
		}
		q.DisableParallel = !b
	}
	if source == "messages" {
		blocks, err := textBlocks(root["system"], "system", false, false)
		if err != nil {
			return q, err
		}
		for _, block := range blocks {
			q.System = append(q.System, block.Text)
		}
	}
	list, ok := root["messages"].([]any)
	if !ok || len(list) == 0 {
		return q, conversionError("messages", "expected a non-empty array")
	}
	calls := map[string]string{}
	answered := map[string]bool{}
	systemRole := ""
	for index, entry := range list {
		path := fmt.Sprintf("messages[%d].", index)
		message, ok := entry.(map[string]any)
		if !ok {
			return q, conversionError(path, "expected an object")
		}
		messageFields := []string{"role", "content", "tool_calls", "tool_call_id"}
		if source == "chat" {
			messageFields = append(messageFields, "refusal", "audio", "annotations")
			if target == "anthropic_messages" {
				messageFields = append(messageFields, "reasoning_content", "anthropic_thinking")
			}
		}
		if err = fieldsOnly(message, path, messageFields...); err != nil {
			return q, err
		}
		// SDK message snapshots include nullable optional fields. Empty/null
		// metadata carries no feature; actual refusals/audio/annotations do.
		if source == "chat" {
			for _, name := range []string{"refusal", "audio", "annotations"} {
				if value, exists := message[name]; exists && value != nil {
					if list, ok := value.([]any); name == "annotations" && ok && len(list) == 0 {
						continue
					}
					return q, conversionError(path+name, "this message capability cannot be preserved")
				}
			}
		}
		role, err := requiredString(message["role"], path+"role")
		if err != nil {
			return q, err
		}
		chatInstruction := source == "messages" && (target == "openai_chat" || target == "") && (role == "system" || role == "developer")
		if source == "messages" && role != "user" && role != "assistant" && !chatInstruction {
			return q, conversionError(path+"role", "unsupported Messages role")
		}
		if role == "system" || role == "developer" {
			if source == "chat" && target == "anthropic_messages" {
				instruction := RelayMessage{Role: role}
				if err := restoreAnthropicThinking(&instruction, message, path); err != nil {
					return q, err
				}
			}
			if !chatInstruction && (len(q.Messages) > 0 || systemRole != "" && systemRole != role) {
				return q, conversionError(path+"role", "only a prefix of one instruction role can be preserved")
			}
			if _, exists := message["tool_calls"]; exists {
				return q, conversionError(path+"tool_calls", "not valid on instruction messages")
			}
			if _, exists := message["tool_call_id"]; exists {
				return q, conversionError(path+"tool_call_id", "not valid on instruction messages")
			}
			systemRole = role
			blocks, err := textBlocks(message["content"], path+"content", false, false)
			if err != nil {
				return q, err
			}
			if chatInstruction {
				// Current Claude Code can append an instruction turn after user
				// content. Chat can preserve its role and exact position directly.
				q.Messages = append(q.Messages, RelayMessage{Role: role, Blocks: blocks})
				continue
			}
			for _, block := range blocks {
				q.System = append(q.System, block.Text)
			}
			continue
		}
		turn := RelayMessage{Role: role}
		if source == "chat" {
			if role != "user" && role != "assistant" && role != "tool" {
				return q, conversionError(path+"role", "unsupported Chat role")
			}
			blocks, err := textBlocks(message["content"], path+"content", role == "user", target == "anthropic_messages")
			if err != nil {
				return q, err
			}
			turn.Blocks = blocks
			if role == "tool" {
				if message["content"] == nil {
					return q, conversionError(path+"content", "tool result content is required")
				}
				id, err := requiredString(message["tool_call_id"], path+"tool_call_id")
				if err != nil {
					return q, err
				}
				if calls[id] == "" || answered[id] {
					return q, conversionError(path+"tool_call_id", "missing or duplicate historical invocation")
				}
				answered[id] = true
				text := joinRelayText(blocks)
				turn.Role = "user"
				turn.Blocks = []RelayBlock{{Type: "tool_result", ID: id, Name: calls[id], Text: text}}
			} else if _, exists := message["tool_call_id"]; exists {
				return q, conversionError(path+"tool_call_id", "only valid on tool results")
			}
			if value, exists := message["tool_calls"]; exists && value != nil {
				if role != "assistant" {
					return q, conversionError(path+"tool_calls", "only valid on assistant messages")
				}
				tools, ok := value.([]any)
				if !ok {
					return q, conversionError(path+"tool_calls", "expected an array")
				}
				for offset, entry := range tools {
					field := fmt.Sprintf("%stool_calls[%d].", path, offset)
					tool, ok := entry.(map[string]any)
					if !ok {
						return q, conversionError(field, "expected an object")
					}
					if err = fieldsOnly(tool, field, "id", "type", "function"); err != nil {
						return q, err
					}
					if tool["type"] != "function" {
						return q, conversionError(field+"type", "only function tools are supported")
					}
					function, ok := tool["function"].(map[string]any)
					if !ok {
						return q, conversionError(field+"function", "expected an object")
					}
					if err = fieldsOnly(function, field+"function.", "name", "arguments"); err != nil {
						return q, err
					}
					id, err := requiredString(tool["id"], field+"id")
					if err != nil {
						return q, err
					}
					name, err := requiredString(function["name"], field+"function.name")
					if err != nil {
						return q, err
					}
					args, ok := function["arguments"].(string)
					if !ok {
						return q, conversionError(field+"function.arguments", "expected a JSON object string")
					}
					input, err := DecodeProtocolJSON([]byte(args))
					if err != nil {
						return q, conversionError(field+"function.arguments", "expected a JSON object string")
					}
					if calls[id] != "" {
						return q, conversionError(field+"id", "duplicate invocation ID")
					}
					calls[id] = name
					turn.Blocks = append(turn.Blocks, RelayBlock{Type: "tool_use", ID: id, Name: name, Input: input})
				}
			}
		} else {
			if _, exists := message["tool_calls"]; exists {
				return q, conversionError(path+"tool_calls", "not a Messages field")
			}
			if _, exists := message["tool_call_id"]; exists {
				return q, conversionError(path+"tool_call_id", "not a Messages field")
			}
			if text, ok := message["content"].(string); ok {
				turn.Blocks = []RelayBlock{{Type: "text", Text: text}}
			} else {
				blocks, ok := message["content"].([]any)
				if !ok {
					return q, conversionError(path+"content", "expected text or blocks")
				}
				for offset, entry := range blocks {
					field := fmt.Sprintf("%scontent[%d].", path, offset)
					block, ok := entry.(map[string]any)
					if !ok {
						return q, conversionError(field, "expected an object")
					}
					switch block["type"] {
					case "thinking":
						if role != "assistant" || target != "openai_chat" && target != "" {
							return q, conversionError(field+"type", "chat reasoning history requires an assistant turn and a chat upstream")
						}
						thinking, err := parseChatThinking(block, field)
						if err != nil {
							return q, err
						}
						turn.Blocks = append(turn.Blocks, thinking)
					case "text":
						if err = fieldsOnly(block, field, "type", "text"); err != nil {
							return q, err
						}
						text, ok := block["text"].(string)
						if !ok {
							return q, conversionError(field+"text", "expected text")
						}
						turn.Blocks = append(turn.Blocks, RelayBlock{Type: "text", Text: text})
					case "image":
						if role != "user" {
							return q, conversionError(field+"type", "image blocks require a user turn")
						}
						if err = fieldsOnly(block, field, "type", "source"); err != nil {
							return q, err
						}
						source, ok := block["source"].(map[string]any)
						if !ok {
							return q, conversionError(field+"source", "expected an object")
						}
						image, err := relayImageSource(source, field+"source")
						if err != nil {
							return q, err
						}
						turn.Blocks = append(turn.Blocks, image)
					case "tool_use":
						if role != "assistant" {
							return q, conversionError(field+"type", "tool_use requires assistant role")
						}
						if err = fieldsOnly(block, field, "type", "id", "name", "input"); err != nil {
							return q, err
						}
						id, err := requiredString(block["id"], field+"id")
						if err != nil {
							return q, err
						}
						name, err := requiredString(block["name"], field+"name")
						if err != nil {
							return q, err
						}
						input, ok := block["input"].(map[string]any)
						if !ok || calls[id] != "" {
							return q, conversionError(field+"input", "expected object arguments and unique invocation ID")
						}
						calls[id] = name
						turn.Blocks = append(turn.Blocks, RelayBlock{Type: "tool_use", ID: id, Name: name, Input: input})
					case "tool_result":
						if role != "user" {
							return q, conversionError(field+"type", "tool_result requires user role")
						}
						if err = fieldsOnly(block, field, "type", "tool_use_id", "content", "is_error"); err != nil {
							return q, err
						}
						id, err := requiredString(block["tool_use_id"], field+"tool_use_id")
						if err != nil {
							return q, err
						}
						if calls[id] == "" || answered[id] {
							return q, conversionError(field+"tool_use_id", "missing or duplicate historical invocation")
						}
						answered[id] = true
						text, err := textBlocks(block["content"], field+"content", false, false)
						if err != nil {
							return q, err
						}
						isError, err := boolField(block, "is_error")
						if err != nil {
							return q, err
						}
						turn.Blocks = append(turn.Blocks, RelayBlock{Type: "tool_result", ID: id, Name: calls[id], Text: joinRelayText(text), IsError: isError})
					default:
						return q, conversionError(field+"type", "only text, tool_use and tool_result blocks are supported")
					}
				}
			}
		}
		if source == "chat" && target == "anthropic_messages" {
			if err := restoreAnthropicThinking(&turn, message, path); err != nil {
				return q, err
			}
		}
		if len(turn.Blocks) == 0 {
			return q, conversionError(path+"content", "empty messages cannot be preserved")
		}
		q.Messages = append(q.Messages, turn)
	}
	if len(q.Messages) == 0 {
		return q, conversionError("messages", "missing conversation")
	}
	for id := range calls {
		if !answered[id] {
			return q, conversionError("messages", "historical tool invocations require matching results")
		}
	}
	if target == "gemini" && q.Messages[len(q.Messages)-1].Role == "assistant" {
		return q, conversionError("messages", "Gemini cannot preserve assistant prefill")
	}
	if value, exists := root["tools"]; exists {
		tools, ok := value.([]any)
		if !ok {
			return q, conversionError("tools", "expected an array")
		}
		seen := map[string]bool{}
		for index, entry := range tools {
			field := fmt.Sprintf("tools[%d].", index)
			tool, ok := entry.(map[string]any)
			if !ok {
				return q, conversionError(field, "expected an object")
			}
			schemaKey := "input_schema"
			if source == "chat" {
				if err = fieldsOnly(tool, field, "type", "function"); err != nil {
					return q, err
				}
				if tool["type"] != "function" {
					return q, conversionError(field+"type", "only function tools are supported")
				}
				tool, ok = tool["function"].(map[string]any)
				if !ok {
					return q, conversionError(field+"function", "expected an object")
				}
				field += "function."
				schemaKey = "parameters"
			}
			toolFields := []string{"name", "description", schemaKey}
			if source == "chat" {
				toolFields = append(toolFields, "strict")
			} else {
				toolFields = append(toolFields, "type")
				if value, exists := tool["type"]; exists && value != "custom" {
					return q, conversionError(field+"type", "only custom function tools are supported")
				}
			}
			if err = fieldsOnly(tool, field, toolFields...); err != nil {
				return q, err
			}
			if source == "messages" && tool[schemaKey] == nil {
				return q, conversionError(field+schemaKey, "input schema is required")
			}
			if value, exists := tool["strict"]; exists && (source != "chat" || value != false) {
				return q, conversionError(field+"strict", "strict schema enforcement cannot be guaranteed")
			}
			name, err := requiredString(tool["name"], field+"name")
			if err != nil {
				return q, err
			}
			if seen[name] {
				return q, conversionError(field+"name", "duplicate tool declaration")
			}
			seen[name] = true
			description := ""
			if value, exists := tool["description"]; exists {
				var ok bool
				description, ok = value.(string)
				if !ok {
					return q, conversionError(field+"description", "expected a string")
				}
			}
			schema := map[string]any{"type": "object", "properties": map[string]any{}}
			if value, exists := tool[schemaKey]; exists {
				var ok bool
				schema, ok = value.(map[string]any)
				if !ok {
					return q, conversionError(field+schemaKey, "expected an object schema")
				}
			}
			q.Tools = append(q.Tools, RelayTool{Name: name, Description: description, Schema: schema})
		}
	}
	if value, exists := root["tool_choice"]; exists {
		if choice, ok := value.(string); ok && source == "chat" {
			switch choice {
			case "auto", "none":
				q.Choice = choice
			case "required":
				q.Choice = "any"
			default:
				return q, conversionError("tool_choice", "unsupported choice")
			}
		} else {
			choice, ok := value.(map[string]any)
			if !ok {
				return q, conversionError("tool_choice", "expected a supported choice")
			}
			allowed := []string{"type", "name", "disable_parallel_tool_use"}
			if source == "chat" {
				allowed = []string{"type", "function"}
			}
			if err = fieldsOnly(choice, "tool_choice.", allowed...); err != nil {
				return q, err
			}
			kind, _ := choice["type"].(string)
			if source == "chat" {
				if kind != "function" {
					return q, conversionError("tool_choice.type", "only named function selection is supported")
				}
				function, ok := choice["function"].(map[string]any)
				if !ok {
					return q, conversionError("tool_choice.function", "expected an object")
				}
				if err = fieldsOnly(function, "tool_choice.function.", "name"); err != nil {
					return q, err
				}
				q.Choice = "tool"
				q.ChoiceName, err = requiredString(function["name"], "tool_choice.function.name")
			} else {
				switch kind {
				case "auto", "none", "any":
					q.Choice = kind
				case "tool":
					q.Choice = kind
					q.ChoiceName, err = requiredString(choice["name"], "tool_choice.name")
				default:
					return q, conversionError("tool_choice.type", "unsupported choice")
				}
				if _, exists := choice["name"]; exists && kind != "tool" {
					return q, conversionError("tool_choice.name", "requires type=tool")
				}
				b, e := boolField(choice, "disable_parallel_tool_use")
				if e != nil {
					return q, e
				}
				if b && target == "gemini" {
					return q, conversionError("tool_choice.disable_parallel_tool_use", "Gemini cannot guarantee serial tool calls")
				}
				q.DisableParallel = b
			}
			if err != nil {
				return q, err
			}
		}
	}
	if (q.Choice == "any" || q.Choice == "tool") && len(q.Tools) == 0 {
		return q, conversionError("tool_choice", "requires tool declarations")
	}
	if q.Choice == "tool" {
		found := false
		for _, tool := range q.Tools {
			if tool.Name == q.ChoiceName {
				found = true
			}
		}
		if !found {
			return q, conversionError("tool_choice", "selected tool is not declared")
		}
	}
	if target == "openai_chat" || target == "" {
		return q, q.validateChat()
	}
	return q, nil
}
func joinRelayText(blocks []RelayBlock) string {
	var text strings.Builder
	for _, block := range blocks {
		text.WriteString(block.Text)
	}
	return text.String()
}

func (q RelayRequest) Upstream(target string) ([]byte, string, error) {
	for index, tool := range q.Tools {
		if !relayFunctionName(tool.Name, target) {
			return nil, "", conversionError(fmt.Sprintf("tools[%d].name", index), "function name is invalid for the target protocol")
		}
	}
	for index, message := range q.Messages {
		for _, block := range message.Blocks {
			if block.Name != "" && !relayFunctionName(block.Name, target) {
				return nil, "", conversionError(fmt.Sprintf("messages[%d]", index), "historical function name is invalid for the target protocol")
			}
		}
	}
	if target == "openai_chat" || target == "" {
		return q.chatUpstream()
	}
	if q.OutputSchema != nil {
		return nil, "", conversionError("output_config.format", "structured output conversion requires an OpenAI Chat upstream")
	}
	root := map[string]any{}
	messages := []any{}
	for index, message := range q.Messages {
		parts := []any{}
		role := message.Role
		for _, block := range message.Blocks {
			if target == "anthropic_messages" {
				switch block.Type {
				case "thinking", "redacted_thinking":
					parts = append(parts, block.AnthropicThinking())
				case "text":
					parts = append(parts, map[string]any{"type": "text", "text": block.Text})
				case "tool_use":
					parts = append(parts, map[string]any{"type": "tool_use", "id": block.ID, "name": block.Name, "input": block.Input})
				case "tool_result":
					parts = append(parts, map[string]any{"type": "tool_result", "tool_use_id": block.ID, "content": block.Text, "is_error": block.IsError})
				case "image":
					source := map[string]any{"type": "base64", "media_type": block.MediaType, "data": block.ImageData}
					if block.URL != "" {
						source = map[string]any{"type": "url", "url": block.URL}
					}
					parts = append(parts, map[string]any{"type": "image", "source": source})
				}
			} else {
				switch block.Type {
				case "text":
					parts = append(parts, map[string]any{"text": block.Text})
				case "tool_use":
					parts = append(parts, map[string]any{"functionCall": map[string]any{"id": block.ID, "name": block.Name, "args": block.Input}})
				case "tool_result":
					kind := "output"
					if block.IsError {
						kind = "error"
					}
					parts = append(parts, map[string]any{"functionResponse": map[string]any{"id": block.ID, "name": block.Name, "response": map[string]any{kind: block.Text}}})
				case "image":
					if block.URL != "" {
						return nil, "", conversionError(fmt.Sprintf("messages[%d]", index), "Gemini conversion requires inline base64 images")
					}
					parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": block.MediaType, "data": block.ImageData}})
				}
			}
		}
		if target == "anthropic_messages" {
			messages = append(messages, map[string]any{"role": role, "content": parts})
		} else {
			if role == "assistant" {
				role = "model"
			}
			if len(messages) > 0 && messages[len(messages)-1].(map[string]any)["role"] == role {
				previous := messages[len(messages)-1].(map[string]any)
				previous["parts"] = append(previous["parts"].([]any), parts...)
			} else {
				messages = append(messages, map[string]any{"role": role, "parts": parts})
			}
		}
	}
	system := []any{}
	for _, text := range q.System {
		if target == "anthropic_messages" {
			system = append(system, map[string]any{"type": "text", "text": text})
		} else {
			system = append(system, map[string]any{"text": text})
		}
	}
	tools := []any{}
	for _, tool := range q.Tools {
		if target == "anthropic_messages" {
			tools = append(tools, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": tool.Schema})
		} else {
			tools = append(tools, map[string]any{"name": tool.Name, "description": tool.Description, "parametersJsonSchema": tool.Schema})
		}
	}
	path := "messages"
	if target == "anthropic_messages" {
		root["model"], root["messages"], root["stream"] = q.Model, messages, q.Stream
		root["max_tokens"] = 4096
		if len(system) > 0 {
			root["system"] = system
		}
		if len(tools) > 0 {
			root["tools"] = tools
		}
		for key, value := range q.Controls {
			if key == "stop" {
				key = "stop_sequences"
			}
			root[key] = value
		}
		if len(tools) > 0 || q.Choice != "auto" {
			choice := map[string]any{"type": q.Choice}
			if q.Choice == "tool" {
				choice["name"] = q.ChoiceName
			}
			if q.DisableParallel {
				choice["disable_parallel_tool_use"] = true
			}
			root["tool_choice"] = choice
		}
	} else {
		root["contents"] = messages
		if len(system) > 0 {
			root["systemInstruction"] = map[string]any{"parts": system}
		}
		if len(tools) > 0 {
			root["tools"] = []any{map[string]any{"functionDeclarations": tools}}
		}
		config := map[string]any{}
		for key, value := range q.Controls {
			switch key {
			case "max_tokens":
				key = "maxOutputTokens"
			case "top_p":
				key = "topP"
			case "top_k":
				key = "topK"
			case "stop":
				key = "stopSequences"
			}
			config[key] = value
		}
		if len(config) > 0 {
			root["generationConfig"] = config
		}
		if len(tools) > 0 || q.Choice != "auto" {
			mode := "AUTO"
			switch q.Choice {
			case "none":
				mode = "NONE"
			case "any", "tool":
				mode = "ANY"
			}
			choice := map[string]any{"mode": mode}
			if q.Choice == "tool" {
				choice["allowedFunctionNames"] = []string{q.ChoiceName}
			}
			root["toolConfig"] = map[string]any{"functionCallingConfig": choice}
		}
		action := "generateContent"
		if q.Stream {
			action = "streamGenerateContent"
		}
		path = "models/" + q.Model + ":" + action
	}
	payload, err := json.Marshal(root)
	return payload, path, err
}

func relayFunctionName(name, target string) bool {
	limit := 64
	if target == "gemini" {
		limit = 128
	}
	if len(name) == 0 || len(name) > limit {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
