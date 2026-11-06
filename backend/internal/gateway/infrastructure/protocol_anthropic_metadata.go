package infrastructure

// ValidateAnthropicRelayBlockMetadata validates fields shared by JSON replies
// and SSE block starts. Required content values are checked by each adapter.
// Only metadata with no additional Chat semantics can be omitted: empty
// citations and the direct caller already implied by a Chat function call.
func ValidateAnthropicRelayBlockMetadata(block map[string]any, path string) error {
	switch block["type"] {
	case "thinking":
		if err := fieldsOnly(block, path, "type", "thinking", "signature"); err != nil {
			return err
		}
		if _, ok := block["thinking"].(string); !ok {
			return conversionError(path+"thinking", "expected thinking text")
		}
		if signature, exists := block["signature"]; exists {
			if _, ok := signature.(string); !ok {
				return conversionError(path+"signature", "expected a signature string")
			}
		}
	case "redacted_thinking":
		_, err := ParseAnthropicThinking(block, path)
		return err
	case "text":
		if err := fieldsOnly(block, path, "type", "text", "citations"); err != nil {
			return err
		}
		if value := block["citations"]; value != nil {
			citations, ok := value.([]any)
			if !ok || len(citations) != 0 {
				return conversionError(path+"citations", "only empty citations can be represented in Chat")
			}
		}
	case "tool_use":
		if err := fieldsOnly(block, path, "type", "id", "name", "input", "caller"); err != nil {
			return err
		}
		if value := block["caller"]; value != nil {
			caller, ok := value.(map[string]any)
			if !ok {
				return conversionError(path+"caller", "expected a caller object")
			}
			if caller["type"] != "direct" {
				return conversionError(path+"caller.type", "only direct tool calls can be represented in Chat")
			}
			if err := fieldsOnly(caller, path+"caller.", "type"); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateAnthropicRelayDeltaFields reports the exact unsupported delta field.
func ValidateAnthropicRelayDeltaFields(delta map[string]any, path string) error {
	switch delta["type"] {
	case "text_delta":
		return fieldsOnly(delta, path, "type", "text")
	case "input_json_delta":
		return fieldsOnly(delta, path, "type", "partial_json")
	case "thinking_delta":
		return fieldsOnly(delta, path, "type", "thinking")
	case "signature_delta":
		return fieldsOnly(delta, path, "type", "signature")
	}
	return nil
}
