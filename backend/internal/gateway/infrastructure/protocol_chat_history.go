package infrastructure

import (
	"encoding/json"
	"strings"
)

type chatHistoryBuilder struct {
	role, reasoningField string
	text                 strings.Builder
	images               []any
	reasoning            strings.Builder
	hasText              bool
	calls                []any
}

func (builder *chatHistoryBuilder) flush() map[string]any {
	if !builder.hasText && len(builder.calls) == 0 && builder.reasoning.Len() == 0 && len(builder.images) == 0 {
		return nil
	}
	content := any(builder.text.String())
	if len(builder.images) > 0 {
		parts := []any{}
		if builder.text.Len() > 0 {
			parts = append(parts, map[string]any{"type": "text", "text": builder.text.String()})
		}
		parts = append(parts, builder.images...)
		content = parts
	}
	value := map[string]any{"role": builder.role, "content": content}
	if builder.reasoning.Len() > 0 {
		value[builder.reasoningField] = builder.reasoning.String()
	}
	if len(builder.calls) > 0 {
		value["tool_calls"] = builder.calls
	}
	builder.text.Reset()
	builder.reasoning.Reset()
	builder.hasText = false
	builder.images = nil
	builder.calls = nil
	return value
}

func (builder *chatHistoryBuilder) append(block RelayBlock) error {
	switch block.Type {
	case "thinking":
		if builder.role != "assistant" || block.ReasoningField != "reasoning" && block.ReasoningField != "reasoning_content" {
			return conversionError("messages.thinking", "invalid chat reasoning history")
		}
		if builder.reasoningField != "" && builder.reasoningField != block.ReasoningField {
			return conversionError("messages.thinking", "mixed reasoning formats cannot be preserved")
		}
		builder.reasoningField = block.ReasoningField
		builder.reasoning.WriteString(block.Text)
	case "text":
		builder.hasText = true
		builder.text.WriteString(block.Text)
	case "image":
		builder.hasText = true
		builder.images = append(builder.images, map[string]any{"type": "image_url", "image_url": map[string]any{"url": relayImageURL(block)}})
	case "tool_use":
		args, err := json.Marshal(block.Input)
		if err != nil {
			return conversionError("messages.tool_use.input", "tool arguments cannot be encoded as JSON")
		}
		builder.calls = append(builder.calls, map[string]any{"id": block.ID, "type": "function", "function": map[string]any{"name": block.Name, "arguments": string(args)}})
	}
	return nil
}
