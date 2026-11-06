package infrastructure

import "fmt"

// normalizeChatHints extracts output constraints and consumes Claude client hints.
// Caller-supplied thinking controls are forwarded unchanged for upstream validation.
// JSON schemas and tool argument objects remain opaque.
func (q *RelayRequest) normalizeChatHints(root map[string]any) error {
	if err := normalizeChatSafeguards(root); err != nil {
		return err
	}
	for _, name := range []string{"thinking", "reasoning_effort", "enable_thinking"} {
		if value, exists := root[name]; exists {
			q.Controls[name] = value
			delete(root, name)
		}
	}
	if value, exists := root["output_config"]; exists {
		config, ok := value.(map[string]any)
		if !ok {
			return conversionError("output_config", "expected an object")
		}
		if err := fieldsOnly(config, "output_config.", "effort", "format"); err != nil {
			return err
		}
		if format := config["format"]; format != nil {
			schema, err := chatOutputSchema(format)
			if err != nil {
				return err
			}
			q.OutputSchema = schema
		}
		if effort, exists := config["effort"]; exists {
			switch effort {
			case "low", "medium", "high", "xhigh", "max":
			default:
				return conversionError("output_config.effort", "unsupported effort hint")
			}
		}
		delete(root, "output_config")
	}
	if value, exists := root["context_management"]; exists {
		if err := validateChatContextHint(value); err != nil {
			return err
		}
		delete(root, "context_management")
	}
	if value, exists := root["metadata"]; exists {
		metadata, ok := value.(map[string]any)
		if !ok {
			return conversionError("metadata", "expected an object")
		}
		if err := fieldsOnly(metadata, "metadata.", "user_id"); err != nil {
			return err
		}
		if id := metadata["user_id"]; id != nil {
			if _, ok := id.(string); !ok {
				return conversionError("metadata.user_id", "expected a string")
			}
		}
		// Caller identity metadata has no inference semantics and is kept local.
		delete(root, "metadata")
	}
	if err := removeChatCacheHint(root, ""); err != nil {
		return err
	}
	for _, name := range []string{"system", "tools"} {
		if err := normalizeChatBlocks(root[name], name); err != nil {
			return err
		}
	}
	messages, _ := root["messages"].([]any)
	for index, entry := range messages {
		if message, ok := entry.(map[string]any); ok {
			if err := normalizeChatBlocks(message["content"], fmt.Sprintf("messages[%d].content", index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func chatOutputSchema(value any) (map[string]any, error) {
	format, ok := value.(map[string]any)
	if !ok {
		return nil, conversionError("output_config.format", "expected an object")
	}
	if err := fieldsOnly(format, "output_config.format.", "type", "schema"); err != nil {
		return nil, err
	}
	if format["type"] != "json_schema" {
		return nil, conversionError("output_config.format.type", "expected json_schema")
	}
	schema, ok := format["schema"].(map[string]any)
	if !ok {
		return nil, conversionError("output_config.format.schema", "expected a JSON Schema object")
	}
	// Preserve constraints, references and exact numbers. The upstream validates
	// its supported schema subset; rewriting it here could change caller intent.
	return schema, nil
}

func validateChatContextHint(value any) error {
	context, ok := value.(map[string]any)
	if !ok {
		return conversionError("context_management", "expected an object")
	}
	if err := fieldsOnly(context, "context_management.", "edits"); err != nil {
		return err
	}
	edits, ok := context["edits"].([]any)
	if !ok {
		return conversionError("context_management.edits", "expected an array")
	}
	for index, entry := range edits {
		path := fmt.Sprintf("context_management.edits[%d].", index)
		edit, ok := entry.(map[string]any)
		if !ok {
			return conversionError(path, "expected an object")
		}
		if err := fieldsOnly(edit, path, "type", "keep"); err != nil {
			return err
		}
		// Claude Code's default asks to retain all thinking. This is a no-op;
		// actual history deletion/compaction must never be silently ignored.
		if edit["type"] != "clear_thinking_20251015" || edit["keep"] != "all" {
			return conversionError(path, "only retaining all thinking history is supported")
		}
	}
	return nil
}

func normalizeChatBlocks(value any, path string) error {
	blocks, _ := value.([]any)
	for index, entry := range blocks {
		block, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		field := fmt.Sprintf("%s[%d].", path, index)
		if err := removeChatCacheHint(block, field); err != nil {
			return err
		}
		if block["type"] == "tool_result" {
			if err := normalizeChatBlocks(block["content"], field+"content"); err != nil {
				return err
			}
		}
	}
	return nil
}

func removeChatCacheHint(value map[string]any, path string) error {
	hint, exists := value["cache_control"]
	if !exists {
		return nil
	}
	cache, ok := hint.(map[string]any)
	if !ok || cache["type"] != "ephemeral" {
		return conversionError(path+"cache_control", "expected an ephemeral cache hint")
	}
	if err := fieldsOnly(cache, path+"cache_control.", "type", "ttl"); err != nil {
		return err
	}
	if ttl, exists := cache["ttl"]; exists && ttl != "5m" && ttl != "1h" {
		return conversionError(path+"cache_control.ttl", "unsupported cache hint TTL")
	}
	delete(value, "cache_control")
	return nil
}
