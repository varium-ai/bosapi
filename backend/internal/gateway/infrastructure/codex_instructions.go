package infrastructure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// NormalizeCodexInstructions promotes a prefix of plain system messages into
// instructions. Preserve untouched items as raw JSON, including opaque reasoning
// and large tool argument numbers. Reject unsupported instruction content rather
// than silently dropping it or moving a later message across conversation turns.
func NormalizeCodexInstructions(root map[string]json.RawMessage) error {
	var items []json.RawMessage
	if json.Unmarshal(root["input"], &items) != nil {
		return nil // String input and other validation are owned by the caller.
	}
	var promoted []string
	kept := make([]json.RawMessage, 0, len(items))
	for i, raw := range items {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil {
			kept = append(kept, raw)
			continue
		}
		var role string
		_ = json.Unmarshal(item["role"], &role)
		if role != "system" {
			kept = append(kept, raw)
			continue
		}
		path := fmt.Sprintf("input[%d]", i)
		if len(kept) > 0 {
			return conversionError(path+".role", "system messages must precede other input items")
		}
		if err := codexInstructionFields(item, path, "type", "role", "content"); err != nil {
			return err
		}
		if kind := item["type"]; len(kind) > 0 && !bytes.Equal(bytes.TrimSpace(kind), []byte(`"message"`)) {
			return conversionError(path+".type", "system input must be a message")
		}
		var text string
		if content := bytes.TrimSpace(item["content"]); len(content) > 0 && content[0] == '"' && json.Unmarshal(content, &text) == nil {
			promoted = append(promoted, text)
			continue
		}
		var parts []map[string]json.RawMessage
		if json.Unmarshal(item["content"], &parts) != nil || bytes.Equal(bytes.TrimSpace(item["content"]), []byte("null")) {
			return conversionError(path+".content", "expected text instruction content")
		}
		var joined strings.Builder
		for j, part := range parts {
			partPath := fmt.Sprintf("%s.content[%d]", path, j)
			if err := codexInstructionFields(part, partPath, "type", "text"); err != nil {
				return err
			}
			var kind, value string
			_ = json.Unmarshal(part["type"], &kind)
			text := bytes.TrimSpace(part["text"])
			if (kind != "input_text" && kind != "text") || len(text) == 0 || text[0] != '"' || json.Unmarshal(text, &value) != nil {
				return conversionError(partPath, "only text instruction parts can be promoted")
			}
			joined.WriteString(value)
		}
		promoted = append(promoted, joined.String())
	}
	if len(promoted) == 0 {
		return nil
	}
	var existing string
	if value := root["instructions"]; len(value) > 0 && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		if json.Unmarshal(value, &existing) != nil {
			return conversionError("instructions", "expected text instructions")
		}
	}
	if existing != "" {
		promoted = append(promoted, existing)
	}
	root["instructions"], _ = json.Marshal(strings.Join(promoted, "\n\n"))
	root["input"], _ = json.Marshal(kept)
	return nil
}

func codexInstructionFields(value map[string]json.RawMessage, path string, allowed ...string) error {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		found := false
		for _, name := range allowed {
			found = found || key == name
		}
		if !found {
			return conversionError(path+"."+key, "instruction metadata cannot be promoted to text")
		}
	}
	return nil
}
