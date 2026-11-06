package domain

import "bosapi/backend/internal/platform/errorcode"

// ToolExecutionError is the shared, content-independent failure for a tool that
// is unavailable to the caller. Its code follows JSON-RPC/MCP method-not-found.
type ToolExecutionError struct {
	Code    int            `json:"code"`
	Type    errorcode.Code `json:"type"`
	Message string         `json:"message"`
}

func UnauthorizedToolError() ToolExecutionError {
	return ToolExecutionError{Code: -32601, Type: errorcode.UnauthorizedTool, Message: "Tool is not authorized."}
}
