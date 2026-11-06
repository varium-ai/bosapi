package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/logging"
)

type gatewayDiagnosticKey struct{}

type gatewayDiagnosticState struct {
	mu      sync.Mutex
	code    errorcode.Code
	summary string
	stream  string
}

func gatewayFailureDiagnostic(ctx context.Context) (errorcode.Code, string) {
	state, _ := ctx.Value(gatewayDiagnosticKey{}).(*gatewayDiagnosticState)
	if state == nil {
		return "", ""
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.code, state.summary
}

func setGatewayFailureDiagnostic(ctx context.Context, code errorcode.Code, summary string) {
	if state, _ := ctx.Value(gatewayDiagnosticKey{}).(*gatewayDiagnosticState); state != nil {
		state.mu.Lock()
		// Keep the original failure when writing its error response also fails.
		if state.code == "" || strings.HasPrefix(state.summary, "failure_stage=provider_policy ") {
			state.code, state.summary = code, summary
		}
		state.mu.Unlock()
	}
}

func clearGatewayPolicyDiagnostic(ctx context.Context) {
	if state, _ := ctx.Value(gatewayDiagnosticKey{}).(*gatewayDiagnosticState); state != nil {
		state.mu.Lock()
		if strings.HasPrefix(state.summary, "failure_stage=provider_policy ") {
			state.code, state.summary = "", ""
		}
		state.mu.Unlock()
	}
}

// gatewayRequestResult is shared by the completion log and usage accounting.
// The HTTP response may already be 200 when a streamed turn fails.
func gatewayRequestResult(r *http.Request, status int, code errorcode.Code) (int, errorcode.Code) {
	if diagnosticCode, _ := gatewayFailureDiagnostic(r.Context()); diagnosticCode != "" {
		code = diagnosticCode
	} else if state := usageState(r); state != nil && state.streamFailure != "" {
		code = state.streamFailure
	}
	if status < http.StatusBadRequest && code != "" {
		status = http.StatusBadGateway
		if code == errorcode.UpstreamTimeout {
			status = http.StatusGatewayTimeout
		}
	}
	return status, code
}

// Record event shape only; raw SSE payloads and arbitrary event/type values
// can contain caller content. Unknown names are deliberately not logged.
func setGatewayStreamDiagnostic(ctx context.Context, frame int, event string, root map[string]any) {
	state, _ := ctx.Value(gatewayDiagnosticKey{}).(*gatewayDiagnosticState)
	if state == nil {
		return
	}
	if kind, ok := root["type"].(string); ok {
		event = kind
	}
	switch event {
	case "message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop", "ping", "error", "chat.chunk", "gemini.chunk", "read":
	default:
		event = "unknown"
	}
	summary := fmt.Sprintf("stream_frame=%d stream_event=%q", frame, event)
	if index, ok := root["index"].(json.Number); ok {
		if n, err := index.Int64(); err == nil && n >= 0 && n <= 1024 {
			summary += fmt.Sprintf(" stream_block_index=%d", n)
		}
	}
	if block, ok := root["content_block"].(map[string]any); ok {
		kind, _ := block["type"].(string)
		switch kind {
		case "text", "tool_use", "thinking", "redacted_thinking", "server_tool_use":
		default:
			kind = "unknown"
		}
		summary += fmt.Sprintf(" stream_block_type=%q", kind)
	}
	state.mu.Lock()
	state.stream = summary
	state.mu.Unlock()
}

func recordResponseReadFailure(r *http.Request, param string, err error) errorcode.Code {
	reason := upstreamFailureReason(r.Context(), err)
	code := errorcode.UpstreamError
	if reason == "timeout" {
		code = errorcode.UpstreamTimeout
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		reason = "unexpected EOF"
	}
	recordGatewayFailure(r, "response_read", code, param, "upstream response body interrupted: "+reason)
	return code
}

// recordGatewayFailure accepts generated diagnostics, never request/response
// bodies or raw parser errors. Unknown JSON field names can still contain caller
// input, so redact both sides' credentials before bounding and quoting them.
func recordGatewayFailure(r *http.Request, stage string, code errorcode.Code, param, reason string) {
	p, _ := r.Context().Value(gatewayProviderRequestKey{}).(provider)
	clean := func(value string, limit int) string {
		for _, secret := range []string{requestSecret(r), p.APIKey, p.AccessToken, p.RefreshToken} {
			if secret != "" {
				value = strings.ReplaceAll(value, secret, "[REDACTED]")
			}
		}
		return logging.Sanitize(value, limit)
	}
	summary := fmt.Sprintf("failure_stage=%s error_param=%q error_reason=%q", stage, clean(param, 192), clean(reason, 512))
	if state, _ := r.Context().Value(gatewayDiagnosticKey{}).(*gatewayDiagnosticState); state != nil {
		state.mu.Lock()
		stream := state.stream
		state.mu.Unlock()
		if stream != "" {
			summary += " " + stream
		}
	}
	setGatewayFailureDiagnostic(r.Context(), code, summary)
	level := "WARN"
	if stage == "response_conversion" || stage == "response_read" || stage == "response_write" || stage == "upstream_stream" || stage == "provider_configuration" || stage == "quota_accounting" {
		level = "ERROR"
	}
	logging.Printf(r.Context(), level, "gateway.request.failed", "method=%s endpoint=%s provider_id=%d provider_name=%q target_protocol=%q error_code=%s %s", r.Method, gatewayLogEndpoint(r), p.ID, clean(p.Name, 128), clean(p.APIFormat, 32), code, summary)
}

func recordConversionFailure(r *http.Request, code errorcode.Code, err error) {
	param, reason := "body", "protocol conversion failed"
	var failure *gatewayinfra.ConversionError
	if errors.As(err, &failure) {
		param, reason = failure.Field, failure.Reason
	}
	stage := "request_conversion"
	if code == errorcode.UnsupportedUpstreamFeature {
		stage = "response_conversion"
	}
	recordGatewayFailure(r, stage, code, param, reason)
}

func recordRequestBodyFailure(r *http.Request, err error) {
	reason := "expected valid JSON"
	if err != nil {
		reason = "request body could not be read"
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			reason = "request body exceeds configured limit"
		}
	}
	recordGatewayFailure(r, "request_validation", errorcode.InvalidRequest, "body", reason)
}
