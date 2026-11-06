package domain

import "testing"

func TestSecurityEventClassificationsOverrideAction(t *testing.T) {
	for _, test := range []struct{ name, rule, tool, kind, severity string }{
		{"model access", "model_not_allowed", "", "access_policy", "info"},
		{"unusable key", "authentication_key_not_found_or_unusable", "", "authentication", "info"},
		{"missing credential", "authentication_missing_credential", "", "authentication", "info"},
		{"invalid provider", "invalid_provider", "", "other", "info"},
		{"prompt secret", "prompt_secret", "", "sensitive_content", "warning"},
		{"model output", "model_output_code", "", "model_output", "info"},
		{"prompt injection", "prompt_injection", "", "injection", "high"},
		{"credential solicitation", "credential_solicitation", "", "injection", "high"},
		{"instruction boundary", "instruction_boundary", "", "injection", "high"},
		{"invalid tool call", "tool_call_invalid", "", "tool_call", "info"},
		{"invalid named tool call", "tool_call_invalid", "shell", "tool_call", "info"},
		{"unauthorized tool", "unauthorized_tool", "", "tool_call", "warning"},
		{"inspection unavailable", "tool_call_inspection_unavailable", "", "tool_call", "warning"},
		{"first custom tool rule", "1", "shell", "tool_call", "warning"},
		{"second custom tool rule", "2", "shell", "tool_call", "warning"},
		{"future tool rule", "tool_future_rule", "", "tool_call", "warning"},
		{"named tool rule", "custom", "shell", "tool_call", "warning"},
		{"output tool context", "model_output_code", "shell", "tool_call", "warning"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, action := range []string{"allow", "observe", "block", ""} {
				t.Run("action="+action, func(t *testing.T) {
					kind, severity := ClassifySecurityEvent(test.rule, action, test.tool)
					if kind != test.kind || severity != test.severity {
						t.Fatalf("classification = %s/%s, want %s/%s", kind, severity, test.kind, test.severity)
					}
				})
			}
		})
	}
}

func TestSecurityEventClassificationsPreserveOtherActionLevels(t *testing.T) {
	for _, test := range []struct{ name, rule, kind string }{
		{"authentication", "authentication_expired", "authentication"},
		{"rate limit", "rate_limit", "rate_limit"},
		{"quota", "quota_exceeded", "quota"},
		{"access policy", "policy_not_allowed", "access_policy"},
		{"custom prompt rule", "12", "other"},
		{"unknown", "unknown", "other"},
		{"empty", "", "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, action := range []struct{ value, severity string }{
				{"allow", "info"}, {"observe", "warning"}, {"block", "high"}, {"", "info"},
			} {
				t.Run("action="+action.value, func(t *testing.T) {
					kind, severity := ClassifySecurityEvent(test.rule, action.value, "")
					if kind != test.kind || severity != action.severity {
						t.Fatalf("classification = %s/%s, want %s/%s", kind, severity, test.kind, action.severity)
					}
				})
			}
		})
	}
}
