package infrastructure

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Anthropic owns model-specific thinking and output configuration validation.
// Keep caller values intact, including exact token budgets and future options.
func (q *RelayRequest) normalizeAnthropicThinking(root map[string]any) error {
	for _, name := range []string{"thinking", "output_config"} {
		value, exists := root[name]
		if !exists {
			continue
		}
		object, ok := value.(map[string]any)
		if !ok {
			return conversionError(name, "expected an object")
		}
		if name == "thinking" {
			if _, err := requiredString(object["type"], "thinking.type"); err != nil {
				return err
			}
		}
		q.Controls[name] = object
		delete(root, name)
	}
	return nil
}

// ParseAnthropicThinking preserves native signed/opaque blocks. Never use the
// synthetic Chat reasoning marker as a substitute for a provider signature.
func ParseAnthropicThinking(value map[string]any, path string) (RelayBlock, error) {
	block := RelayBlock{ReasoningField: "reasoning_content"}
	block.Type, _ = value["type"].(string)
	switch block.Type {
	case "thinking":
		if err := fieldsOnly(value, path, "type", "thinking", "signature"); err != nil {
			return block, err
		}
		text, ok := value["thinking"].(string)
		if !ok {
			return block, conversionError(path+"thinking", "expected thinking text")
		}
		signature, err := requiredString(value["signature"], path+"signature")
		if err != nil {
			return block, err
		}
		block.Text, block.Signature = text, signature
	case "redacted_thinking":
		if err := fieldsOnly(value, path, "type", "data"); err != nil {
			return block, err
		}
		data, err := requiredString(value["data"], path+"data")
		if err != nil {
			return block, err
		}
		block.Data = data
	default:
		return block, conversionError(path+"type", "expected an Anthropic thinking block")
	}
	return block, nil
}

func (block RelayBlock) AnthropicThinking() map[string]any {
	if block.Type == "redacted_thinking" {
		return map[string]any{"type": block.Type, "data": block.Data}
	}
	return map[string]any{"type": "thinking", "thinking": block.Text, "signature": block.Signature}
}

// Offsets anchor each thinking block among Chat's concatenated answer text and
// tool calls. Only thinking data is duplicated, never executable tool arguments.
// Clients concatenate these arrays during SSE and return them on assistant turns.
func (block RelayBlock) ChatThinking(textOffset, toolOffset int) map[string]any {
	return map[string]any{"block": block.AnthropicThinking(), "text_offset": textOffset, "tool_offset": toolOffset}
}

func restoreAnthropicThinking(turn *RelayMessage, message map[string]any, path string) error {
	value, exists := message["anthropic_thinking"]
	reasoning, hasReasoning := message["reasoning_content"]
	var visible string
	if hasReasoning && reasoning != nil {
		var ok bool
		visible, ok = reasoning.(string)
		if !ok {
			return conversionError(path+"reasoning_content", "expected reasoning text")
		}
	}
	if !exists || value == nil {
		if visible != "" {
			return conversionError(path+"anthropic_thinking", "return the original anthropic_thinking blocks to preserve signed reasoning history")
		}
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return conversionError(path+"anthropic_thinking", "expected an array")
	}
	if (len(items) > 0 || visible != "") && turn.Role != "assistant" {
		return conversionError(path+"anthropic_thinking", "thinking history requires an assistant turn")
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
	var thinking strings.Builder
	appendUntil := func(nextText, nextTool int) {
		if nextText > textOffset {
			blocks = append(blocks, RelayBlock{Type: "text", Text: text[textOffset:nextText]})
		}
		blocks = append(blocks, tools[toolOffset:nextTool]...)
		textOffset, toolOffset = nextText, nextTool
	}
	for index, item := range items {
		field := fmt.Sprintf("%santhropic_thinking[%d].", path, index)
		entry, ok := item.(map[string]any)
		if !ok {
			return conversionError(field, "expected an object")
		}
		if err := fieldsOnly(entry, field, "block", "text_offset", "tool_offset"); err != nil {
			return err
		}
		offsets := [2]int{}
		for i, name := range []string{"text_offset", "tool_offset"} {
			number, ok := entry[name].(json.Number)
			if !ok {
				return conversionError(field+name, "expected a non-negative integer")
			}
			n, err := number.Int64()
			limit, previous := len(text), textOffset
			if i == 1 {
				limit, previous = len(tools), toolOffset
			}
			if err != nil || n < int64(previous) || n > int64(limit) {
				return conversionError(field+name, "offset is outside the message or out of order")
			}
			offsets[i] = int(n)
		}
		if !utf8.ValidString(text[textOffset:offsets[0]]) {
			return conversionError(field+"text_offset", "offset must align with UTF-8 text")
		}
		native, ok := entry["block"].(map[string]any)
		if !ok {
			return conversionError(field+"block", "expected an Anthropic thinking block")
		}
		block, err := ParseAnthropicThinking(native, field+"block.")
		if err != nil {
			return err
		}
		appendUntil(offsets[0], offsets[1])
		blocks = append(blocks, block)
		if block.Type == "thinking" {
			thinking.WriteString(block.Text)
		}
	}
	if hasReasoning && reasoning != nil && thinking.String() != visible {
		return conversionError(path+"reasoning_content", "reasoning text must match the original anthropic_thinking blocks")
	}
	if len(items) > 0 {
		appendUntil(len(text), len(tools))
		turn.Blocks = blocks
	}
	return nil
}
