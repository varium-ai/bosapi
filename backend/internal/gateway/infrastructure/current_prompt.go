package infrastructure

import "strconv"

// mapCurrentPrompt shares the exact same selection between detection and
// redaction. Protocol history is kept intact, and evidence paths retain the
// original message/block indices. A tool continuation has no new user input.
func mapCurrentPrompt(value any, visit func(any, string) (any, bool)) (any, bool) {
	root, ok := value.(map[string]any)
	if !ok {
		return visit(value, "body")
	}
	if contents, exists := root["contents"]; exists {
		items, _ := contents.([]any)
		if len(items) == 0 {
			return value, false
		}
		index := len(items) - 1
		message, _ := items[index].(map[string]any)
		role, _ := message["role"].(string)
		if role != "" && role != "user" {
			return value, false
		}
		parts, changed := mapCurrentText(message["parts"], "contents["+strconv.Itoa(index)+"].parts", visit)
		if message != nil {
			message["parts"] = parts
		}
		return value, changed
	}
	if messages, exists := root["messages"]; exists {
		items, _ := messages.([]any)
		return value, mapCurrentMessage(items, "messages", visit)
	}
	if input, exists := root["input"]; exists {
		switch input := input.(type) {
		case string:
			updated, changed := visit(input, "input")
			root["input"] = updated
			return value, changed
		case []any:
			// Responses message/call items form conversation history. Bare
			// input_text blocks and embedding strings are direct input instead.
			for _, item := range input {
				if object, ok := item.(map[string]any); ok {
					kind, _ := object["type"].(string)
					_, hasRole := object["role"]
					_, hasContent := object["content"]
					if hasRole || hasContent || kind == "message" || kind == "function_call" || kind == "function_call_output" || kind == "reasoning" {
						return value, mapCurrentMessage(input, "input", visit)
					}
				}
			}
			updated, changed := mapCurrentText(input, "input", visit)
			root["input"] = updated
			return value, changed
		default:
			return value, false
		}
	}
	if prompt, exists := root["prompt"]; exists {
		updated, changed := visit(prompt, "prompt")
		root["prompt"] = updated
		return value, changed
	}
	// A protocol envelope with no user input must not fall back to inspecting
	// system instructions, tools or model/transport metadata.
	for _, key := range []string{"model", "system", "instructions", "tools", "functions", "response_format", "tool_choice"} {
		if _, exists := root[key]; exists {
			return value, false
		}
	}
	if _, schema := root["text"].(map[string]any); schema {
		return value, false
	}
	// Preserve standalone JSON/text inspection for non-conversation callers.
	return visit(value, "")
}

func mapCurrentMessage(items []any, path string, visit func(any, string) (any, bool)) bool {
	if len(items) == 0 {
		return false
	}
	index := len(items) - 1
	// Trailing policy instructions do not turn a new user message into
	// history. Assistant/tool continuations below do stop the selection.
	for index >= 0 {
		message, _ := items[index].(map[string]any)
		role, _ := message["role"].(string)
		if role != "system" && role != "developer" {
			break
		}
		index--
	}
	if index < 0 {
		return false
	}
	message, ok := items[index].(map[string]any)
	if !ok {
		return false
	}
	role, _ := message["role"].(string)
	kind, _ := message["type"].(string)
	// Missing roles are accepted for legacy content-only messages. Explicit
	// system/developer/assistant/tool roles and call items are never new input.
	if role != "" && role != "user" || kind != "" && kind != "message" {
		return false
	}
	content, exists := message["content"]
	if !exists {
		return false
	}
	updated, changed := mapCurrentText(content, path+"["+strconv.Itoa(index)+"].content", visit)
	message["content"] = updated
	return changed
}

func mapCurrentText(value any, path string, visit func(any, string) (any, bool)) (any, bool) {
	switch value := value.(type) {
	case string:
		return visit(value, path)
	case []any:
		changed := false
		for index, item := range value {
			updated, itemChanged := mapCurrentText(item, path+"["+strconv.Itoa(index)+"]", visit)
			value[index], changed = updated, changed || itemChanged
		}
		return value, changed
	case map[string]any:
		kind, _ := value["type"].(string)
		if kind != "" && kind != "text" && kind != "input_text" {
			return value, false // Includes tool_result, tool_use and media blocks.
		}
		if text, ok := value["text"].(string); ok {
			updated, changed := visit(text, path+".text")
			value["text"] = updated
			return value, changed
		}
		return value, false
	default:
		return value, false
	}
}
