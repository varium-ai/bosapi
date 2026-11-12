package infrastructure

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

func codexTextFormat(value any) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	format, ok := value.(map[string]any)
	if !ok {
		return nil, conversionError("response_format", "expected a format object or null")
	}
	if err := fieldsOnly(format, "response_format.", "type", "json_schema"); err != nil {
		return nil, err
	}
	switch format["type"] {
	case "text", "json_object":
		if _, exists := format["json_schema"]; exists {
			return nil, conversionError("response_format.json_schema", "only valid with json_schema type")
		}
		return map[string]any{"type": format["type"]}, nil
	case "json_schema":
		schema, ok := format["json_schema"].(map[string]any)
		if !ok {
			return nil, conversionError("response_format.json_schema", "expected a schema configuration")
		}
		if err := fieldsOnly(schema, "response_format.json_schema.", "name", "description", "schema", "strict"); err != nil {
			return nil, err
		}
		if _, err := requiredString(schema["name"], "response_format.json_schema.name"); err != nil {
			return nil, err
		}
		if _, ok := schema["schema"].(map[string]any); !ok {
			return nil, conversionError("response_format.json_schema.schema", "expected a JSON Schema object")
		}
		if description, exists := schema["description"]; exists {
			if _, ok := description.(string); !ok {
				return nil, conversionError("response_format.json_schema.description", "expected text")
			}
		}
		if strict, exists := schema["strict"]; exists && strict != nil {
			if _, ok := strict.(bool); !ok {
				return nil, conversionError("response_format.json_schema.strict", "expected a boolean or null")
			}
		}
		result := map[string]any{"type": "json_schema"}
		for key, v := range schema {
			result[key] = v // JSON Schema is opaque; preserve exact numbers/options.
		}
		return result, nil
	default:
		return nil, conversionError("response_format.type", "expected text, json_object or json_schema")
	}
}

// CodexReasoningBlock keeps the original reasoning item for history replay.
// Only explicitly supplied summary text is displayed; ciphertext stays opaque.
func CodexReasoningBlock(item map[string]any, path string) (RelayBlock, error) {
	block := RelayBlock{Type: "codex_reasoning", Input: item}
	if item["type"] != "reasoning" {
		return block, conversionError(path+"type", "expected a reasoning item")
	}
	for _, name := range []string{"id", "encrypted_content", "status"} {
		if value := item[name]; value != nil {
			if _, ok := value.(string); !ok {
				return block, conversionError(path+name, "expected text")
			}
		}
	}
	if summary := item["summary"]; summary != nil {
		parts, ok := summary.([]any)
		if !ok {
			return block, conversionError(path+"summary", "expected summary parts")
		}
		var text strings.Builder
		for i, entry := range parts {
			part, ok := entry.(map[string]any)
			if !ok || part["type"] != "summary_text" {
				return block, conversionError(fmt.Sprintf("%ssummary[%d]", path, i), "expected a summary_text part")
			}
			value, ok := part["text"].(string)
			if !ok {
				return block, conversionError(path+"summary.text", "expected summary text")
			}
			text.WriteString(value)
		}
		block.Text = text.String()
	}
	return block, nil
}

func (block RelayBlock) ChatCodexReasoning(textOffset, toolOffset int) map[string]any {
	return map[string]any{"item": block.Input, "text_offset": textOffset, "tool_offset": toolOffset}
}

func restoreCodexReasoning(turn *RelayMessage, message map[string]any, path string) error {
	visible, _ := message["reasoning_content"].(string)
	if v := message["reasoning_content"]; v != nil {
		if _, ok := v.(string); !ok {
			return conversionError(path+"reasoning_content", "expected summary text")
		}
	}
	value := message["codex_reasoning"]
	if value == nil {
		if visible != "" {
			return conversionError(path+"codex_reasoning", "return the original codex_reasoning items to preserve reasoning history")
		}
		return nil
	}
	items, ok := value.([]any)
	if !ok || len(items) > 1024 {
		return conversionError(path+"codex_reasoning", "expected at most 1024 reasoning items")
	}
	if (len(items) > 0 || visible != "") && turn.Role != "assistant" {
		return conversionError(path+"codex_reasoning", "reasoning history requires an assistant turn")
	}
	text := joinRelayText(turn.Blocks)
	tools := []RelayBlock{}
	for _, block := range turn.Blocks {
		if block.Type == "tool_use" {
			tools = append(tools, block)
		}
	}
	blocks := []RelayBlock{}
	textOffset, toolOffset := 0, 0
	var summary strings.Builder
	appendUntil := func(t, c int) {
		if t > textOffset {
			blocks = append(blocks, RelayBlock{Type: "text", Text: text[textOffset:t]})
		}
		blocks = append(blocks, tools[toolOffset:c]...)
		textOffset, toolOffset = t, c
	}
	for i, entry := range items {
		field := fmt.Sprintf("%scodex_reasoning[%d].", path, i)
		item, ok := entry.(map[string]any)
		if !ok {
			return conversionError(field, "expected a reasoning entry")
		}
		if err := fieldsOnly(item, field, "item", "text_offset", "tool_offset"); err != nil {
			return err
		}
		offsets := [2]int{}
		for j, name := range []string{"text_offset", "tool_offset"} {
			number, ok := item[name].(json.Number)
			if !ok {
				return conversionError(field+name, "expected an integer")
			}
			n, err := number.Int64()
			previous, limit := textOffset, len(text)
			if j == 1 {
				previous, limit = toolOffset, len(tools)
			}
			if err != nil || n < int64(previous) || n > int64(limit) {
				return conversionError(field+name, "offset is outside the message or out of order")
			}
			offsets[j] = int(n)
		}
		if !utf8.ValidString(text[textOffset:offsets[0]]) {
			return conversionError(field+"text_offset", "offset must align with UTF-8 text")
		}
		native, ok := item["item"].(map[string]any)
		if !ok {
			return conversionError(field+"item", "expected a reasoning item")
		}
		block, err := CodexReasoningBlock(native, field+"item.")
		if err != nil {
			return err
		}
		appendUntil(offsets[0], offsets[1])
		blocks = append(blocks, block)
		summary.WriteString(block.Text)
	}
	if message["reasoning_content"] != nil && summary.String() != visible {
		return conversionError(path+"reasoning_content", "summary must match the original codex_reasoning items")
	}
	if len(items) > 0 {
		appendUntil(len(text), len(tools))
		turn.Blocks = blocks
	}
	return nil
}
