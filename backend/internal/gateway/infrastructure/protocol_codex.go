package infrastructure

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CodexUpstream renders the validated common Chat subset as Responses input.
// Unsupported controls are rejected rather than silently changing semantics.
func (q RelayRequest) CodexUpstream() ([]byte, error) {
	keys := make([]string, 0, len(q.Controls))
	for key := range q.Controls {
		if key != "max_tokens" {
			keys = append(keys, key)
		}
	}
	if len(keys) > 0 {
		sort.Strings(keys)
		return nil, conversionError(keys[0], "Codex OAuth support for this control is not established")
	}
	input := []any{}
	instructions := ""
	if len(q.System) > 0 {
		role := q.SystemRole
		if role == "" {
			role = "developer"
		}
		if role == "system" {
			instructions = strings.Join(q.System, "\n\n")
		} else {
			input = append(input, map[string]any{"type": "message", "role": role, "content": strings.Join(q.System, "\n\n")})
		}
	}
	for i, message := range q.Messages {
		parts := []any{}
		flush := func() {
			if len(parts) > 0 {
				input = append(input, map[string]any{"type": "message", "role": message.Role, "content": parts})
				parts = []any{}
			}
		}
		for _, block := range message.Blocks {
			switch block.Type {
			case "text":
				kind := "input_text"
				if message.Role == "assistant" {
					kind = "output_text"
				}
				parts = append(parts, map[string]any{"type": kind, "text": block.Text})
			case "image":
				if message.Role != "user" {
					return nil, conversionError(fmt.Sprintf("messages[%d].content", i), "images require user role")
				}
				parts = append(parts, map[string]any{"type": "input_image", "image_url": relayImageURL(block)})
			case "tool_use":
				flush()
				args, err := json.Marshal(block.Input)
				if err != nil {
					return nil, err
				}
				input = append(input, map[string]any{"type": "function_call", "call_id": block.ID, "name": block.Name, "arguments": string(args)})
			case "tool_result":
				flush()
				input = append(input, map[string]any{"type": "function_call_output", "call_id": block.ID, "output": block.Text})
			case "codex_reasoning":
				flush()
				input = append(input, block.Input)
			default:
				return nil, conversionError(fmt.Sprintf("messages[%d].content", i), "content cannot be represented by Codex")
			}
		}
		flush()
	}
	root := map[string]any{"model": q.Model, "input": input, "instructions": instructions, "stream": true, "store": false}
	if q.PromptCacheKey != "" {
		root["prompt_cache_key"] = q.PromptCacheKey
	}
	// Preserve encrypted reasoning for stateless tool/history continuation.
	root["include"] = []string{"reasoning.encrypted_content"}
	if q.CodexTextFormat != nil {
		root["text"] = map[string]any{"format": q.CodexTextFormat}
	}
	if limit, exists := q.Controls["max_tokens"]; exists {
		root["max_output_tokens"] = limit
	}
	if q.ReasoningEffort != "" {
		root["reasoning"] = map[string]any{"effort": q.ReasoningEffort}
	}
	if len(q.Tools) > 0 {
		tools := []any{}
		for _, tool := range q.Tools {
			if !relayFunctionName(tool.Name, "openai_responses") {
				return nil, conversionError("tools.name", "invalid function name")
			}
			tools = append(tools, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": tool.Schema})
		}
		root["tools"] = tools
	}
	if len(q.Tools) > 0 || q.Choice != "auto" {
		choice := any(q.Choice)
		if q.Choice == "any" {
			choice = "required"
		}
		if q.Choice == "tool" {
			choice = map[string]any{"type": "function", "name": q.ChoiceName}
		}
		root["tool_choice"] = choice
		if q.DisableParallel {
			root["parallel_tool_calls"] = false
		}
	}
	return json.Marshal(root)
}
