package infrastructure

import (
	"encoding/json"
	"strings"
)

// Gemini budgets are token limits, not effort levels. Do not approximate a
// numeric budget with a provider-specific effort or silently discard the limit.
func (q *RelayRequest) parseGeminiThinking(value any, target string) error {
	if value == nil {
		return nil
	}
	const path = "generationConfig.thinkingConfig."
	config, ok := value.(map[string]any)
	if !ok {
		return conversionError("generationConfig.thinkingConfig", "expected an object")
	}
	if err := fieldsOnly(config, path, "thinkingBudget", "thinkingLevel", "includeThoughts"); err != nil {
		return err
	}
	if v, exists := config["includeThoughts"]; exists {
		include, ok := v.(bool)
		if !ok {
			return conversionError(path+"includeThoughts", "expected a boolean")
		}
		if include && target == "anthropic_messages" {
			return conversionError(path+"includeThoughts", "signed Anthropic thinking requires the native Messages protocol")
		}
		q.IncludeThoughts = include
	}
	budget, hasBudget := config["thinkingBudget"]
	level, hasLevel := config["thinkingLevel"]
	if hasBudget && hasLevel {
		return conversionError("generationConfig.thinkingConfig", "thinkingBudget and thinkingLevel cannot be combined")
	}
	if hasLevel {
		text, ok := level.(string)
		if !ok {
			return conversionError(path+"thinkingLevel", "expected a supported thinking level")
		}
		effort := strings.ToLower(text)
		switch effort {
		case "minimal", "low", "medium", "high":
		default:
			return conversionError(path+"thinkingLevel", "unsupported thinking level")
		}
		if target == "anthropic_messages" {
			if effort == "minimal" {
				return conversionError(path+"thinkingLevel", "minimal has no equivalent Anthropic effort")
			}
			q.Controls["thinking"] = map[string]any{"type": "adaptive"}
			q.Controls["output_config"] = map[string]any{"effort": effort}
		} else {
			q.Controls["reasoning_effort"] = effort
		}
	}
	if !hasBudget {
		return nil
	}
	number, ok := budget.(json.Number)
	if !ok {
		return conversionError(path+"thinkingBudget", "expected an integer of -1 or greater")
	}
	n, err := number.Int64()
	if err != nil || n < -1 {
		return conversionError(path+"thinkingBudget", "expected an integer of -1 or greater")
	}
	if n == 0 {
		q.Controls["thinking"] = map[string]any{"type": "disabled"}
		return nil
	}
	if target != "anthropic_messages" {
		if n > 0 {
			return conversionError(path+"thinkingBudget", "OpenAI Chat has no equivalent token budget; use -1 for automatic thinking or thinkingLevel for effort")
		}
		// GLM-compatible Chat providers accept this switch. The upstream
		// remains responsible for validating model-specific capabilities.
		q.Controls["thinking"] = map[string]any{"type": "enabled"}
		return nil
	}
	if n == -1 {
		q.Controls["thinking"] = map[string]any{"type": "adaptive"}
		return nil
	}
	if n < 1024 {
		return conversionError(path+"thinkingBudget", "Anthropic thinking requires a budget of at least 1024 tokens")
	}
	limit := int64(4096) // Must match the Messages relay default.
	if value, ok := q.Controls["max_tokens"].(json.Number); ok {
		limit, _ = value.Int64()
	}
	if n >= limit {
		return conversionError(path+"thinkingBudget", "thinking budget must be less than maxOutputTokens (default 4096)")
	}
	q.Controls["thinking"] = map[string]any{"type": "enabled", "budget_tokens": number}
	return nil
}
