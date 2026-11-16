package infrastructure

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// parseGeminiRelayRequest converts the bounded native Gemini request subset
// into the shared relay representation used by OpenAI Chat.
func parseGeminiRelayRequest(raw []byte, target string) (RelayRequest, error) {
	q := RelayRequest{Controls: map[string]any{}, Choice: "auto"}
	if target != "openai_chat" && target != "" {
		return q, conversionError("endpoint", "Gemini requests can only be converted to an OpenAI Chat upstream")
	}
	root, err := DecodeProtocolJSON(raw)
	if err != nil {
		return q, err
	}
	if err := fieldsOnly(root, "", "contents", "systemInstruction", "generationConfig", "tools", "toolConfig"); err != nil {
		return q, err
	}

	if value := root["systemInstruction"]; value != nil {
		instruction, ok := value.(map[string]any)
		if !ok {
			return q, conversionError("systemInstruction", "expected an object")
		}
		if err := fieldsOnly(instruction, "systemInstruction.", "parts"); err != nil {
			return q, err
		}
		parts, ok := instruction["parts"].([]any)
		if !ok || len(parts) == 0 {
			return q, conversionError("systemInstruction.parts", "expected a non-empty array")
		}
		for index, entry := range parts {
			path := fmt.Sprintf("systemInstruction.parts[%d].", index)
			part, ok := entry.(map[string]any)
			if !ok {
				return q, conversionError(path, "expected a text part")
			}
			if err := fieldsOnly(part, path, "text"); err != nil {
				return q, err
			}
			text, ok := part["text"].(string)
			if !ok {
				return q, conversionError(path+"text", "expected text")
			}
			q.System = append(q.System, text)
		}
	}

	if value := root["generationConfig"]; value != nil {
		config, ok := value.(map[string]any)
		if !ok {
			return q, conversionError("generationConfig", "expected an object")
		}
		if err := fieldsOnly(config, "generationConfig.", "maxOutputTokens", "temperature", "topP", "stopSequences", "candidateCount"); err != nil {
			return q, err
		}
		for _, control := range []struct{ source, target string }{
			{"maxOutputTokens", "max_tokens"},
			{"temperature", "temperature"},
			{"topP", "top_p"},
		} {
			sourceName, targetName := control.source, control.target
			if value, exists := config[sourceName]; exists {
				number, ok := value.(json.Number)
				if !ok {
					return q, conversionError("generationConfig."+sourceName, "expected a number")
				}
				n, err := number.Float64()
				if err != nil {
					return q, conversionError("generationConfig."+sourceName, "invalid number")
				}
				if targetName == "max_tokens" {
					if integer, err := number.Int64(); err != nil || integer <= 0 {
						return q, conversionError("generationConfig."+sourceName, "expected a positive integer")
					}
				} else if targetName == "temperature" && (n < 0 || n > 2) || targetName == "top_p" && (n < 0 || n > 1) {
					return q, conversionError("generationConfig."+sourceName, "outside the OpenAI Chat range")
				}
				q.Controls[targetName] = value
			}
		}
		if value, exists := config["stopSequences"]; exists {
			stops, ok := value.([]any)
			if !ok {
				return q, conversionError("generationConfig.stopSequences", "expected an array of strings")
			}
			for _, stop := range stops {
				if _, ok := stop.(string); !ok {
					return q, conversionError("generationConfig.stopSequences", "expected an array of strings")
				}
			}
			q.Controls["stop"] = stops
		}
		if value, exists := config["candidateCount"]; exists && value != json.Number("1") {
			return q, conversionError("generationConfig.candidateCount", "only one candidate is supported")
		}
	}

	if err := parseGeminiFunctionDeclarations(root["tools"], &q); err != nil {
		return q, err
	}
	allowedNames, err := parseGeminiToolConfig(root["toolConfig"], &q)
	if err != nil {
		return q, err
	}
	if allowedNames != nil {
		allowed := map[string]bool{}
		for _, name := range allowedNames {
			allowed[name] = true
		}
		declared := map[string]bool{}
		for _, tool := range q.Tools {
			declared[tool.Name] = true
		}
		for name := range allowed {
			if !declared[name] {
				return q, conversionError("toolConfig.functionCallingConfig.allowedFunctionNames", "contains an undeclared function")
			}
		}
		if q.Choice == "none" {
			allowed = nil
		} else {
			filtered := q.Tools[:0]
			for _, tool := range q.Tools {
				if allowed[tool.Name] {
					filtered = append(filtered, tool)
				}
			}
			q.Tools = filtered
		}
	}
	if (q.Choice == "any" || q.Choice == "tool") && len(q.Tools) == 0 {
		return q, conversionError("toolConfig.functionCallingConfig.mode", "function selection requires declared tools")
	}
	if q.Choice == "tool" {
		found := false
		for _, tool := range q.Tools {
			if tool.Name == q.ChoiceName {
				found = true
				break
			}
		}
		if !found {
			return q, conversionError("toolConfig.functionCallingConfig.allowedFunctionNames", "selected function is not declared")
		}
	}

	contents, ok := root["contents"].([]any)
	if !ok || len(contents) == 0 {
		return q, conversionError("contents", "expected a non-empty array")
	}
	calls, answered := map[string]string{}, map[string]bool{}
	outstanding := map[string][]string{}
	callIndex := 0
	for messageIndex, entry := range contents {
		path := fmt.Sprintf("contents[%d].", messageIndex)
		message, ok := entry.(map[string]any)
		if !ok {
			return q, conversionError(path, "expected an object")
		}
		if err := fieldsOnly(message, path, "role", "parts"); err != nil {
			return q, err
		}
		role, err := requiredString(message["role"], path+"role")
		if err != nil {
			return q, err
		}
		if role != "user" && role != "model" {
			return q, conversionError(path+"role", "expected user or model")
		}
		parts, ok := message["parts"].([]any)
		if !ok || len(parts) == 0 {
			return q, conversionError(path+"parts", "expected a non-empty array")
		}
		turn := RelayMessage{Role: "user"}
		if role == "model" {
			turn.Role = "assistant"
		}
		for partIndex, partValue := range parts {
			partPath := fmt.Sprintf("%sparts[%d].", path, partIndex)
			part, ok := partValue.(map[string]any)
			if !ok {
				return q, conversionError(partPath, "expected a part object")
			}
			switch {
			case part["text"] != nil:
				if err := fieldsOnly(part, partPath, "text"); err != nil {
					return q, err
				}
				text, ok := part["text"].(string)
				if !ok {
					return q, conversionError(partPath+"text", "expected text")
				}
				turn.Blocks = append(turn.Blocks, RelayBlock{Type: "text", Text: text})
			case part["inlineData"] != nil:
				if role != "user" {
					return q, conversionError(partPath+"inlineData", "images require a user turn")
				}
				image, err := parseGeminiInlineImage(part["inlineData"], partPath+"inlineData")
				if err != nil {
					return q, err
				}
				if err := fieldsOnly(part, partPath, "inlineData"); err != nil {
					return q, err
				}
				turn.Blocks = append(turn.Blocks, image)
			case part["functionCall"] != nil:
				if role != "model" {
					return q, conversionError(partPath+"functionCall", "function calls require a model turn")
				}
				block, err := parseGeminiFunctionCall(part["functionCall"], partPath+"functionCall", callIndex)
				if err != nil {
					return q, err
				}
				callIndex++
				if calls[block.ID] != "" {
					return q, conversionError(partPath+"functionCall.id", "duplicate invocation ID")
				}
				calls[block.ID] = block.Name
				outstanding[block.Name] = append(outstanding[block.Name], block.ID)
				turn.Blocks = append(turn.Blocks, block)
				if err := fieldsOnly(part, partPath, "functionCall"); err != nil {
					return q, err
				}
			case part["functionResponse"] != nil:
				if role != "user" {
					return q, conversionError(partPath+"functionResponse", "function responses require a user turn")
				}
				block, err := parseGeminiFunctionResponse(part["functionResponse"], partPath+"functionResponse", calls, answered, outstanding)
				if err != nil {
					return q, err
				}
				answered[block.ID] = true
				turn.Blocks = append(turn.Blocks, block)
				if err := fieldsOnly(part, partPath, "functionResponse"); err != nil {
					return q, err
				}
			default:
				return q, conversionError(partPath, "only text, inlineData, functionCall, and functionResponse parts are supported")
			}
		}
		q.Messages = append(q.Messages, turn)
	}
	for id := range calls {
		if !answered[id] {
			return q, conversionError("contents", "historical function calls require matching function responses")
		}
	}
	if err := q.validateChat(); err != nil {
		return q, err
	}
	return q, nil
}

func parseGeminiInlineImage(value any, path string) (RelayBlock, error) {
	image, ok := value.(map[string]any)
	if !ok {
		return RelayBlock{}, conversionError(path, "expected an inline image object")
	}
	if err := fieldsOnly(image, path+".", "mimeType", "data"); err != nil {
		return RelayBlock{}, err
	}
	mediaType, err := requiredString(image["mimeType"], path+".mimeType")
	if err != nil || !strings.HasPrefix(mediaType, "image/") {
		return RelayBlock{}, conversionError(path+".mimeType", "expected an image media type")
	}
	data, err := requiredString(image["data"], path+".data")
	if err != nil {
		return RelayBlock{}, conversionError(path+".data", "expected base64 image data")
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return RelayBlock{}, conversionError(path+".data", "expected base64 image data")
	}
	return RelayBlock{Type: "image", MediaType: mediaType, ImageData: data}, nil
}

func parseGeminiFunctionCall(value any, path string, index int) (RelayBlock, error) {
	call, ok := value.(map[string]any)
	if !ok {
		return RelayBlock{}, conversionError(path, "expected a function call object")
	}
	if err := fieldsOnly(call, path+".", "id", "name", "args"); err != nil {
		return RelayBlock{}, err
	}
	name, err := requiredString(call["name"], path+".name")
	if err != nil {
		return RelayBlock{}, err
	}
	id := fmt.Sprintf("gemini_call_%d", index)
	if value, exists := call["id"]; exists {
		id, err = requiredString(value, path+".id")
		if err != nil {
			return RelayBlock{}, err
		}
	}
	args := map[string]any{}
	if value, exists := call["args"]; exists {
		var ok bool
		args, ok = value.(map[string]any)
		if !ok {
			return RelayBlock{}, conversionError(path+".args", "expected an object")
		}
	}
	return RelayBlock{Type: "tool_use", ID: id, Name: name, Input: args}, nil
}

func parseGeminiFunctionResponse(value any, path string, calls map[string]string, answered map[string]bool, outstanding map[string][]string) (RelayBlock, error) {
	response, ok := value.(map[string]any)
	if !ok {
		return RelayBlock{}, conversionError(path, "expected a function response object")
	}
	if err := fieldsOnly(response, path+".", "id", "name", "response"); err != nil {
		return RelayBlock{}, err
	}
	name, err := requiredString(response["name"], path+".name")
	if err != nil {
		return RelayBlock{}, err
	}
	var id string
	if value, exists := response["id"]; exists {
		id, err = requiredString(value, path+".id")
		if err != nil {
			return RelayBlock{}, err
		}
	} else {
		for _, candidate := range outstanding[name] {
			if !answered[candidate] {
				id = candidate
				break
			}
		}
	}
	if id == "" || calls[id] != name || answered[id] {
		return RelayBlock{}, conversionError(path+".id", "missing or unmatched function call")
	}
	result, ok := response["response"].(map[string]any)
	if !ok {
		return RelayBlock{}, conversionError(path+".response", "expected an object")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return RelayBlock{}, conversionError(path+".response", "response could not be represented")
	}
	return RelayBlock{Type: "tool_result", ID: id, Name: name, Text: string(raw), IsError: result["error"] != nil}, nil
}

func parseGeminiFunctionDeclarations(value any, q *RelayRequest) error {
	if value == nil {
		return nil
	}
	tools, ok := value.([]any)
	if !ok {
		return conversionError("tools", "expected an array")
	}
	seen := map[string]bool{}
	count := 0
	for index, entry := range tools {
		path := fmt.Sprintf("tools[%d].", index)
		tool, ok := entry.(map[string]any)
		if !ok {
			return conversionError(path, "expected a tool object")
		}
		if err := fieldsOnly(tool, path, "functionDeclarations"); err != nil {
			return err
		}
		declarations, ok := tool["functionDeclarations"].([]any)
		if !ok {
			return conversionError(path+"functionDeclarations", "expected an array")
		}
		for declarationIndex, declarationValue := range declarations {
			count++
			if count > maxToolCalls {
				return conversionError("tools", "too many function declarations")
			}
			declarationPath := fmt.Sprintf("%sfunctionDeclarations[%d].", path, declarationIndex)
			declaration, ok := declarationValue.(map[string]any)
			if !ok {
				return conversionError(declarationPath, "expected a function declaration")
			}
			if err := fieldsOnly(declaration, declarationPath, "name", "description", "parameters", "parametersJsonSchema"); err != nil {
				return err
			}
			if declaration["parameters"] != nil && declaration["parametersJsonSchema"] != nil {
				return conversionError(declarationPath, "parameters and parametersJsonSchema cannot both be set")
			}
			name, err := requiredString(declaration["name"], declarationPath+"name")
			if err != nil {
				return err
			}
			if seen[name] {
				return conversionError(declarationPath+"name", "duplicate function declaration")
			}
			seen[name] = true
			description := ""
			if value, exists := declaration["description"]; exists {
				var ok bool
				description, ok = value.(string)
				if !ok {
					return conversionError(declarationPath+"description", "expected a string")
				}
			}
			schema := map[string]any{"type": "object", "properties": map[string]any{}}
			if value := declaration["parameters"]; value != nil {
				var ok bool
				schema, ok = value.(map[string]any)
				if !ok {
					return conversionError(declarationPath+"parameters", "expected an object schema")
				}
			} else if value := declaration["parametersJsonSchema"]; value != nil {
				var ok bool
				schema, ok = value.(map[string]any)
				if !ok {
					return conversionError(declarationPath+"parametersJsonSchema", "expected an object schema")
				}
			}
			q.Tools = append(q.Tools, RelayTool{Name: name, Description: description, Schema: schema})
		}
	}
	return nil
}

func parseGeminiToolConfig(value any, q *RelayRequest) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	config, ok := value.(map[string]any)
	if !ok {
		return nil, conversionError("toolConfig", "expected an object")
	}
	if err := fieldsOnly(config, "toolConfig.", "functionCallingConfig"); err != nil {
		return nil, err
	}
	calling, ok := config["functionCallingConfig"].(map[string]any)
	if !ok {
		return nil, conversionError("toolConfig.functionCallingConfig", "expected an object")
	}
	if err := fieldsOnly(calling, "toolConfig.functionCallingConfig.", "mode", "allowedFunctionNames"); err != nil {
		return nil, err
	}
	mode := ""
	if value, exists := calling["mode"]; exists {
		var ok bool
		mode, ok = value.(string)
		if !ok {
			return nil, conversionError("toolConfig.functionCallingConfig.mode", "expected AUTO, ANY, or NONE")
		}
	}
	switch mode {
	case "", "AUTO":
		q.Choice = "auto"
	case "NONE":
		q.Choice = "none"
	case "ANY":
		q.Choice = "any"
	default:
		return nil, conversionError("toolConfig.functionCallingConfig.mode", "expected AUTO, ANY, or NONE")
	}
	if value, exists := calling["allowedFunctionNames"]; exists {
		names, ok := value.([]any)
		if !ok || len(names) == 0 {
			return nil, conversionError("toolConfig.functionCallingConfig.allowedFunctionNames", "expected a non-empty array of function names")
		}
		result, seen := make([]string, 0, len(names)), map[string]bool{}
		for _, entry := range names {
			name, ok := entry.(string)
			if !ok || name == "" || seen[name] {
				return nil, conversionError("toolConfig.functionCallingConfig.allowedFunctionNames", "expected unique function names")
			}
			seen[name] = true
			result = append(result, name)
		}
		if q.Choice == "any" && len(result) == 1 {
			q.Choice, q.ChoiceName = "tool", result[0]
		}
		return result, nil
	}
	return nil, nil
}
