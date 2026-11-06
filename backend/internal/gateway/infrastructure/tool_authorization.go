package infrastructure

import "bosapi/backend/internal/gateway/domain"

// ToolAuthorization contains only exact names declared by the caller, never
// names found in conversation history, model output, descriptions or arguments.
type ToolAuthorization map[domain.ToolProtocol]map[string]bool

func (authorization ToolAuthorization) Allows(call domain.ToolCall) bool {
	return authorization[call.Protocol][call.Name]
}

// ParseToolAuthorization treats missing/invalid declarations as an empty list.
// Provider-hosted tools are not client-executable function declarations.
func ParseToolAuthorization(payload []byte) ToolAuthorization {
	if len(payload) > maxToolPayload {
		return nil
	}
	value, valid := deliveryObject(payload)
	if !valid {
		return nil
	}
	if value["type"] == "response.create" {
		if response := object(value["response"]); response != nil {
			value = response
		}
	}
	authorization := ToolAuthorization{}
	add := func(protocol domain.ToolProtocol, name string) {
		if name == "" {
			return
		}
		if authorization[protocol] == nil {
			authorization[protocol] = map[string]bool{}
		}
		authorization[protocol][name] = true
	}
	if value["tool_choice"] == "none" || object(value["tool_choice"])["type"] == "none" {
		return authorization
	}
	geminiConfig := object(object(value["toolConfig"])["functionCallingConfig"])
	if geminiConfig["mode"] == "NONE" {
		return authorization
	}
	if tools, ok := value["tools"].([]any); ok && len(tools) <= maxToolCalls {
		for _, entry := range tools {
			tool := object(entry)
			if declarations, ok := tool["functionDeclarations"].([]any); ok && len(declarations) <= maxToolCalls {
				allowed, limited := geminiConfig["allowedFunctionNames"].([]any)
				for _, declaration := range declarations {
					name := stringField(object(declaration), "name")
					permitted := !limited || len(allowed) == 0
					for _, candidate := range allowed {
						if candidate == name {
							permitted = true
						}
					}
					if permitted {
						add(domain.ToolProtocolGemini, name)
					}
				}
			}
			switch tool["type"] {
			case "function":
				name := stringField(tool, "name")
				if function := object(tool["function"]); function != nil {
					name = stringField(function, "name")
				}
				add(domain.ToolProtocolOpenAI, name)
			case nil:
				// Anthropic custom tools use name/input_schema without a type.
				if object(tool["input_schema"]) != nil {
					add(domain.ToolProtocolAnthropic, stringField(tool, "name"))
				}
			case "custom":
				if object(tool["input_schema"]) != nil {
					add(domain.ToolProtocolAnthropic, stringField(tool, "name"))
				}
			}
		}
	}
	return authorization
}
