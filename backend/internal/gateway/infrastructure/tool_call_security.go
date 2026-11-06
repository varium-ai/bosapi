package infrastructure

import (
	"context"

	"bosapi/backend/internal/gateway/domain"
	"bosapi/backend/internal/platform/securityevidence"
)

// ToolCallInspectionPayload includes decoded strings so JSON escapes cannot hide
// credential or code patterns, including OpenAI's JSON-encoded arguments.
func ToolCallInspectionPayload(call domain.ToolCall) []byte {
	return toolInspectionDocument(call).text
}

func (sink RuleSecurityEventSink) InspectToolCall(ctx context.Context, call domain.ToolCall) (domain.SecurityInspectionResult, error) {
	doc := toolInspectionDocument(call)
	result := inspectDocumentWithAdvanced(ctx, "prompt", doc, sink.Advanced)
	if result.RuleID != "" {
		if securityevidence.IsInjectionReason(result.RuleID) {
			return result, nil
		}
		result.RuleID = "tool_call_secret"
		result.RuleName = "Tool credential check"
		return result, nil
	}
	result = inspectDocumentWithAdvanced(ctx, "model_output", doc, sink.Advanced)
	if result.RuleID != "" {
		if securityevidence.IsInjectionReason(result.RuleID) {
			return result, nil
		}
		result.RuleID = "tool_call_code"
		result.RuleName = "Tool code marker check"
	}
	return result, nil
}
