package infrastructure

import (
	"encoding/json"

	"bosapi/backend/internal/gateway/domain"
	"bosapi/backend/internal/platform/errorcode"
)

func isStructuredToolError(value any) bool {
	var envelope struct {
		Error domain.ToolExecutionError `json:"error"`
	}
	encoded := toolJSON(value)
	if content, ok := value.(string); ok {
		encoded = []byte(content)
	}
	return json.Unmarshal(encoded, &envelope) == nil && envelope.Error.Code == -32601 && envelope.Error.Type == errorcode.UnauthorizedTool
}

func toolErrorContent() string {
	return string(toolJSON(map[string]any{"error": domain.UnauthorizedToolError()}))
}

func openAIToolError(id string) map[string]any {
	return map[string]any{"role": "tool", "tool_call_id": id, "content": toolErrorContent()}
}

func responsesToolError(id string) map[string]any {
	return map[string]any{"type": "function_call_output", "call_id": id, "output": toolErrorContent()}
}

func anthropicToolError(id string) map[string]any {
	return map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": true, "content": toolErrorContent()}
}

func mcpToolError(id any) map[string]any {
	failure := domain.UnauthorizedToolError()
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{
		"code": failure.Code, "message": "Method not found", "data": failure,
	}}
}
