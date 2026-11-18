package infrastructure

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// ParseResponsesForChat converts stateless Responses history into the validated Chat subset.
func ParseResponsesForChat(raw []byte) (RelayRequest, error) {
	root, err := DecodeProtocolJSON(raw)
	if err != nil {
		return RelayRequest{}, err
	}
	// Client metadata is advisory on this stateless bridge and is omitted upstream.
	if err := fieldsOnly(root, "", "model", "input", "instructions", "stream", "stream_options", "store", "include", "max_output_tokens", "temperature", "top_p", "tools", "tool_choice", "parallel_tool_calls", "reasoning", "service_tier", "prompt_cache_key", "safety_identifier", "text", "metadata", "client_metadata", "previous_response_id", "truncation", "modalities"); err != nil {
		return RelayRequest{}, err
	}
	if err := validateResponsesChatOptions(root); err != nil {
		return RelayRequest{}, err
	}
	if id := root["previous_response_id"]; id != nil && id != "" {
		return RelayRequest{}, conversionError("previous_response_id", "Chat conversion requires the full input history")
	}
	// Accept clients' storage preference without forwarding it: this bridge
	// does not store responses for subsequent lookup or continuation.
	if root["store"] != nil {
		if _, err := boolField(root, "store"); err != nil {
			return RelayRequest{}, err
		}
	}
	if value := root["include"]; value != nil {
		parts, ok := value.([]any)
		if !ok {
			return RelayRequest{}, conversionError("include", "expected an array")
		}
		for _, part := range parts {
			if part != "reasoning.encrypted_content" {
				return RelayRequest{}, conversionError("include", "requested output cannot be represented by Chat")
			}
		}
	}
	messages, err := responsesChatMessages(root)
	if err != nil {
		return RelayRequest{}, err
	}
	chat := map[string]any{"model": root["model"], "messages": messages}
	for _, field := range []string{"stream", "temperature", "top_p", "parallel_tool_calls"} {
		if root[field] != nil {
			chat[field] = root[field]
		}
	}
	if root["max_output_tokens"] != nil {
		chat["max_completion_tokens"] = root["max_output_tokens"]
	}
	tools, err := responsesChatTools(root)
	if err != nil {
		return RelayRequest{}, err
	}
	tools, namespaces, err := responsesChatNamespaceTools(tools)
	if err != nil {
		return RelayRequest{}, err
	}
	var strictTools map[string]bool
	if len(tools) > 0 {
		converted := make([]any, 0, len(tools))
		strictTools = make(map[string]bool)
		for _, declaration := range tools {
			tool, path := declaration.tool, declaration.path
			if tool["type"] != "function" {
				return RelayRequest{}, conversionError(path+"type", "only function tools can be converted to Chat")
			}
			if err := fieldsOnly(tool, path, "type", "name", "description", "parameters", "strict"); err != nil {
				return RelayRequest{}, err
			}
			name, err := requiredString(tool["name"], path+"name")
			if err != nil {
				return RelayRequest{}, err
			}
			function := map[string]any{"name": name}
			for _, key := range []string{"description", "parameters"} {
				if tool[key] != nil {
					function[key] = tool[key]
				}
			}
			if tool["strict"] != nil {
				strict, err := boolField(tool, "strict")
				if err != nil {
					return RelayRequest{}, conversionError(path+"strict", "expected a boolean")
				}
				strictTools[name] = strict
			}
			converted = append(converted, map[string]any{"type": "function", "function": function})
		}
		chat["tools"] = converted
	}
	choice, _ := root["tool_choice"].(map[string]any)
	allowedTools := choice["type"] == "allowed_tools"
	if value := root["tool_choice"]; value != nil && len(tools) > 0 && !allowedTools {
		chat["tool_choice"] = value
		if choice, ok := value.(map[string]any); ok {
			if responsesChatHostedTool(choice["type"]) {
				delete(chat, "tool_choice")
			} else {
				if err := fieldsOnly(choice, "tool_choice.", "type", "name", "function", "namespace"); err != nil {
					return RelayRequest{}, err
				}
				function := choice["function"]
				if function == nil {
					name, err := responsesChatFunctionName(choice, "tool_choice.")
					if err != nil {
						return RelayRequest{}, err
					}
					function = map[string]any{"name": name}
				} else if choice["name"] != nil {
					return RelayRequest{}, conversionError("tool_choice.name", "use one function selection form")
				}
				chat["tool_choice"] = map[string]any{"type": choice["type"], "function": function}
			}
		}
	}
	payload, err := json.Marshal(chat)
	if err != nil {
		return RelayRequest{}, err
	}
	q, err := parseRelayRequest(payload, "chat", "openai_chat", true)
	if err != nil {
		return q, err
	}
	q.ResponsesNamespaces = namespaces
	if allowedTools {
		if err := applyResponsesChatAllowedTools(&q, choice); err != nil {
			return RelayRequest{}, err
		}
	}
	mergeResponsesChatInstructions(&q)
	for i := range q.Tools {
		if strict, ok := strictTools[q.Tools[i].Name]; ok {
			q.Tools[i].Strict = &strict
		}
	}
	for _, key := range []string{"service_tier", "prompt_cache_key", "safety_identifier"} {
		if root[key] != nil {
			value, err := requiredString(root[key], key)
			if err != nil {
				return q, err
			}
			q.Controls[key] = value
		}
	}
	if value := root["reasoning"]; value != nil {
		reasoning, ok := value.(map[string]any)
		if !ok {
			return q, conversionError("reasoning", "expected an object")
		}
		if err := fieldsOnly(reasoning, "reasoning.", "effort", "summary"); err != nil {
			return q, err
		}
		if reasoning["effort"] != nil {
			effort, err := requiredString(reasoning["effort"], "reasoning.effort")
			if err != nil {
				return q, err
			}
			q.Controls["reasoning_effort"] = effort
		}
		if reasoning["summary"] != nil {
			if _, err := requiredString(reasoning["summary"], "reasoning.summary"); err != nil {
				return q, err
			}
		}
	}
	if value := root["text"]; value != nil {
		text, ok := value.(map[string]any)
		if !ok {
			return q, conversionError("text", "expected an object")
		}
		if err := fieldsOnly(text, "text.", "format", "verbosity"); err != nil {
			return q, err
		}
		if text["format"] != nil {
			format, ok := text["format"].(map[string]any)
			if !ok {
				return q, conversionError("text.format", "expected an object")
			}
			switch format["type"] {
			case "text", "json_object":
				if err := fieldsOnly(format, "text.format.", "type"); err != nil {
					return q, err
				}
				q.Controls["response_format"] = format
			case "json_schema":
				if err := fieldsOnly(format, "text.format.", "type", "name", "description", "schema", "strict"); err != nil {
					return q, err
				}
				delete(format, "type")
				q.Controls["response_format"] = map[string]any{"type": "json_schema", "json_schema": format}
			default:
				return q, conversionError("text.format.type", "unsupported format")
			}
		}
		if text["verbosity"] != nil {
			switch text["verbosity"] {
			case "low", "medium", "high":
				q.Controls["verbosity"] = text["verbosity"]
			default:
				return q, conversionError("text.verbosity", "unsupported verbosity")
			}
		}
	}
	return q, nil
}

func validateResponsesChatOptions(root map[string]any) error {
	if value := root["truncation"]; value != nil && value != "disabled" {
		return conversionError("truncation", "Chat conversion only supports disabled truncation")
	}
	if value := root["modalities"]; value != nil {
		if modalities, ok := value.([]any); !ok || len(modalities) != 1 || modalities[0] != "text" {
			return conversionError("modalities", "Chat conversion only supports text output")
		}
	}
	if value := root["stream_options"]; value != nil {
		options, ok := value.(map[string]any)
		if !ok || root["stream"] != true {
			return conversionError("stream_options", "expected an object on a streaming request")
		}
		if err := fieldsOnly(options, "stream_options.", "include_obfuscation"); err != nil {
			return err
		}
		if obfuscation := options["include_obfuscation"]; obfuscation != nil && obfuscation != false {
			return conversionError("stream_options.include_obfuscation", "Chat conversion cannot provide stream obfuscation")
		}
	}
	return nil
}

func applyResponsesChatAllowedTools(q *RelayRequest, choice map[string]any) error {
	if err := fieldsOnly(choice, "tool_choice.", "type", "mode", "tools"); err != nil {
		return err
	}
	mode := stringField(choice, "mode")
	if mode != "auto" && mode != "required" {
		return conversionError("tool_choice.mode", "expected auto or required")
	}
	tools, ok := choice["tools"].([]any)
	if !ok || len(tools) == 0 {
		return conversionError("tool_choice.tools", "expected a non-empty array of function selectors")
	}
	declared := make(map[string]bool, len(q.Tools))
	for _, tool := range q.Tools {
		declared[tool.Name] = true
	}
	allowed := make(map[string]bool, len(tools))
	for index, value := range tools {
		path := fmt.Sprintf("tool_choice.tools[%d].", index)
		tool, ok := value.(map[string]any)
		if !ok || tool["type"] != "function" {
			return conversionError(path+"type", "expected a function selector")
		}
		if err := fieldsOnly(tool, path, "type", "name", "namespace"); err != nil {
			return err
		}
		name, err := responsesChatFunctionName(tool, path)
		if err != nil {
			return err
		}
		owner, namespaced := q.ResponsesNamespaces[name]
		namespace := stringField(tool, "namespace")
		if !declared[name] || namespaced != (namespace != "") || namespaced && (owner.Namespace != namespace || owner.Name != stringField(tool, "name")) {
			return conversionError(path+"name", "selected function is not declared")
		}
		allowed[name] = true
	}
	selected := q.Tools[:0]
	for _, tool := range q.Tools {
		if allowed[tool.Name] {
			selected = append(selected, tool)
		}
	}
	q.Tools = selected
	if mode == "required" {
		q.Choice = "any"
	}
	return nil
}

func mergeResponsesChatInstructions(q *RelayRequest) {
	// Some Chat upstreams accept a single initial system message.
	leading := 0
	for leading < len(q.Messages) && q.Messages[leading].Role == "system" {
		leading++
	}
	if leading < 2 {
		return
	}
	parts := make([]string, 0, leading)
	for _, message := range q.Messages[:leading] {
		parts = append(parts, joinRelayText(message.Blocks))
	}
	q.Messages[leading-1].Blocks = []RelayBlock{{Type: "text", Text: strings.Join(parts, "\n\n")}}
	q.Messages = q.Messages[leading-1:]
}

type responsesChatTool struct {
	path string
	tool map[string]any
}

// These tools execute on a native Responses server. Chat has no equivalent.
func responsesChatHostedTool(kind any) bool {
	switch kind {
	case "web_search", "web_search_preview", "web_search_preview_2025_03_11", "image_generation", "file_search", "code_interpreter", "computer_use_preview", "computer":
		return true
	default:
		return false
	}
}

func responsesChatTools(root map[string]any) ([]responsesChatTool, error) {
	var tools []responsesChatTool
	byName := make(map[string]map[string]any)
	add := func(value any, path string, additional bool) error {
		list, ok := value.([]any)
		if !ok {
			return conversionError(path, "expected an array")
		}
		for i, entry := range list {
			field := fmt.Sprintf("%s[%d].", path, i)
			tool, ok := entry.(map[string]any)
			if !ok {
				return conversionError(field, "expected a tool declaration")
			}
			if responsesChatHostedTool(tool["type"]) {
				continue
			}
			name, _ := tool["name"].(string)
			if previous, exists := byName[name]; exists {
				if additional && reflect.DeepEqual(previous, tool) {
					continue
				}
				return conversionError(field+"name", "duplicate tool declaration")
			}
			byName[name] = tool
			tools = append(tools, responsesChatTool{path: field, tool: tool})
		}
		return nil
	}
	if root["tools"] != nil {
		if err := add(root["tools"], "tools", false); err != nil {
			return nil, err
		}
	}
	input, _ := root["input"].([]any)
	for i, entry := range input {
		item, _ := entry.(map[string]any)
		if item["type"] != "additional_tools" {
			continue
		}
		path := fmt.Sprintf("input[%d].", i)
		if err := fieldsOnly(item, path, "type", "tools"); err != nil {
			return nil, err
		}
		if err := add(item["tools"], path+"tools", true); err != nil {
			return nil, err
		}
	}
	return tools, nil
}

func responsesChatMessages(root map[string]any) ([]any, error) {
	messages := []any{}
	if value := root["instructions"]; value != nil {
		text, ok := value.(string)
		if !ok {
			return nil, conversionError("instructions", "expected a string")
		}
		if text != "" {
			messages = append(messages, map[string]any{"role": "system", "content": text})
		}
	}
	if text, ok := root["input"].(string); ok {
		return append(messages, map[string]any{"role": "user", "content": text}), nil
	}
	input, ok := root["input"].([]any)
	if !ok || len(input) == 0 {
		return nil, conversionError("input", "expected text or a non-empty history array")
	}
	var assistant map[string]any
	var reasoning strings.Builder
	flush := func() {
		if assistant != nil {
			if reasoning.Len() != 0 {
				assistant["reasoning_content"] = reasoning.String()
			}
			messages = append(messages, assistant)
			assistant = nil
			reasoning.Reset()
		}
	}
	for i, entry := range input {
		path := fmt.Sprintf("input[%d].", i)
		if text, ok := entry.(string); ok {
			flush()
			messages = append(messages, map[string]any{"role": "user", "content": text})
			continue
		}
		item, ok := entry.(map[string]any)
		if !ok {
			return nil, conversionError(path, "expected an object")
		}
		kind := item["type"]
		switch kind {
		case "web_search_call", "file_search_call", "image_generation_call", "code_interpreter_call", "computer_call", "computer_call_output":
			// Native hosted-tool bookkeeping carries no Chat function invocation.
			continue
		case "additional_tools":
			// Collected and validated as declarations, with no message added.
			continue
		case "input_text", "text", "input_image":
			flush()
			part, err := responsesChatPart(item, path, "user")
			if err != nil {
				return nil, err
			}
			messages = append(messages, map[string]any{"role": "user", "content": []any{part}})
		case nil, "message":
			if err := fieldsOnly(item, path, "type", "id", "status", "role", "content", "phase"); err != nil {
				return nil, err
			}
			role, _ := item["role"].(string)
			if role == "developer" {
				role = "system"
			}
			if role != "assistant" && role != "system" && role != "user" {
				return nil, conversionError(path+"role", "unsupported message role")
			}
			content, err := responsesChatContent(item["content"], path+"content", role)
			if err != nil {
				return nil, err
			}
			if role == "assistant" {
				if assistant != nil && (assistant["content"] != nil || assistant["tool_calls"] != nil) {
					flush()
				}
				if assistant == nil {
					assistant = map[string]any{"role": role}
				}
				assistant["content"] = content
			} else {
				flush()
				messages = append(messages, map[string]any{"role": role, "content": content})
			}
		case "reasoning":
			block, err := CodexReasoningBlock(item, path)
			if err != nil {
				return nil, err
			}
			// Opaque reasoning cannot be replayed. Keep any plaintext summary.
			if block.Text != "" {
				if assistant != nil && (assistant["content"] != nil || assistant["tool_calls"] != nil) {
					flush()
				}
				if assistant == nil {
					assistant = map[string]any{"role": "assistant"}
				}
				reasoning.WriteString(block.Text)
			}
		case "function_call":
			if err := fieldsOnly(item, path, "type", "id", "status", "call_id", "name", "arguments", "namespace"); err != nil {
				return nil, err
			}
			name, err := responsesChatFunctionName(item, path)
			if err != nil {
				return nil, err
			}
			if assistant == nil {
				assistant = map[string]any{"role": "assistant"}
			}
			calls, _ := assistant["tool_calls"].([]any)
			assistant["tool_calls"] = append(calls, map[string]any{"id": item["call_id"], "type": "function", "function": map[string]any{"name": name, "arguments": item["arguments"]}})
		case "function_call_output":
			flush()
			if err := fieldsOnly(item, path, "type", "id", "status", "call_id", "output"); err != nil {
				return nil, err
			}
			content, err := responsesChatToolOutput(item["output"], path+"output")
			if err != nil {
				return nil, err
			}
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": item["call_id"], "content": content})
		default:
			return nil, conversionError(path+"type", "input item cannot be represented by Chat")
		}
	}
	flush()
	return messages, nil
}

func responsesChatToolOutput(value any, path string) (any, error) {
	if _, structured := value.(map[string]any); structured {
		raw, err := json.Marshal(value)
		return string(raw), err
	}
	return responsesChatContent(value, path, "tool")
}

func responsesChatContent(value any, path, role string) (any, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	parts, ok := value.([]any)
	if !ok {
		return nil, conversionError(path, "expected a string or content parts")
	}
	converted := make([]any, 0, len(parts))
	for i, entry := range parts {
		partPath := fmt.Sprintf("%s[%d].", path, i)
		part, ok := entry.(map[string]any)
		if !ok {
			return nil, conversionError(partPath, "expected an object")
		}
		chatPart, err := responsesChatPart(part, partPath, role)
		if err != nil {
			return nil, err
		}
		converted = append(converted, chatPart)
	}
	return converted, nil
}

func responsesChatPart(part map[string]any, partPath, role string) (map[string]any, error) {
	switch part["type"] {
	case "input_text", "output_text", "text":
		if err := fieldsOnly(part, partPath, "type", "text", "annotations", "logprobs"); err != nil {
			return nil, err
		}
		for _, field := range []string{"annotations", "logprobs"} {
			if value := part[field]; value != nil {
				historicalAnnotations := field == "annotations" && role == "assistant" && part["type"] == "output_text"
				if list, ok := value.([]any); !ok || len(list) != 0 && !historicalAnnotations {
					return nil, conversionError(partPath+field, "content metadata cannot be represented by Chat")
				}
			}
		}
		text, ok := part["text"].(string)
		if !ok {
			return nil, conversionError(partPath+"text", "expected a string")
		}
		return map[string]any{"type": "text", "text": text}, nil
	case "input_image", "image_url":
		if role != "user" && role != "tool" {
			return nil, conversionError(partPath+"type", "images require a user message or tool result")
		}
		if err := fieldsOnly(part, partPath, "type", "image_url", "detail"); err != nil {
			return nil, err
		}
		image := map[string]any{"url": part["image_url"]}
		if nested, ok := part["image_url"].(map[string]any); ok {
			if err := fieldsOnly(nested, partPath+"image_url.", "url", "detail"); err != nil {
				return nil, err
			}
			image = nested
		}
		if part["detail"] != nil {
			if image["detail"] != nil && !reflect.DeepEqual(image["detail"], part["detail"]) {
				return nil, conversionError(partPath+"detail", "conflicting image detail")
			}
			image["detail"] = part["detail"]
		}
		return map[string]any{"type": "image_url", "image_url": image}, nil
	default:
		return nil, conversionError(partPath+"type", "content part cannot be represented by Chat")
	}
}
