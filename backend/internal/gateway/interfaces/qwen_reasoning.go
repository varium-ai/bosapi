package interfaces

import (
	"encoding/json"
	"strings"
)

// rewriteQwenReasoningEffort runs after model aliases have been resolved.
// Qwen3.8-27B accepts xhigh, medium and low, but rejects Codex's high setting.
// Keep this compatibility alias specific to that model and the two inference
// endpoints; other models may distinguish high from xhigh.
func rewriteQwenReasoningEffort(payload []byte, path string) []byte {
	if path != "/responses" && path != "/chat/completions" {
		return payload
	}
	// Inspect only routing/effort fields first, avoiding a copy of large input
	// and attachment fields for requests that do not need this compatibility fix.
	var request struct {
		Model           string          `json:"model"`
		Reasoning       json.RawMessage `json:"reasoning"`
		ReasoningEffort json.RawMessage `json:"reasoning_effort"`
	}
	if json.Unmarshal(payload, &request) != nil || !strings.EqualFold(request.Model, "Qwen3.8-27B") {
		return payload
	}

	var effort string
	var reasoning map[string]json.RawMessage
	if path == "/responses" {
		if json.Unmarshal(request.Reasoning, &reasoning) != nil || json.Unmarshal(reasoning["effort"], &effort) != nil || effort != "high" {
			return payload
		}
	} else if json.Unmarshal(request.ReasoningEffort, &effort) != nil || effort != "high" {
		return payload
	}

	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return payload
	}
	if path == "/responses" {
		reasoning["effort"] = json.RawMessage(`"xhigh"`)
		raw, err := json.Marshal(reasoning)
		if err != nil {
			return payload
		}
		fields["reasoning"] = raw
	} else {
		fields["reasoning_effort"] = json.RawMessage(`"xhigh"`)
	}
	// RawMessage preserves unknown fields and the precision of large numbers.
	rewritten, err := json.Marshal(fields)
	if err != nil {
		return payload
	}
	return rewritten
}
