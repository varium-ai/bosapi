package interfaces

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"

	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/logging"
)

// doUpstream records transport evidence only. Bodies, headers and URL queries
// may contain prompts or credentials and must never be included in diagnostics.
func (a *app) doUpstream(client *http.Client, req *http.Request, p provider, payload []byte, timeout time.Duration) (*http.Response, error) {
	ctx := req.Context()
	start := time.Now()
	var fields struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(payload, &fields)
	fields.Stream = fields.Stream || req.URL.Query().Get("alt") == "sse" || strings.HasSuffix(req.URL.Path, ":streamGenerateContent")
	deadlineMS := int64(-1)
	if deadline, ok := ctx.Deadline(); ok {
		deadlineMS = max(0, time.Until(deadline).Milliseconds())
	}
	endpoint := req.URL.Scheme + "://" + req.URL.Host + req.URL.EscapedPath()
	timeoutScope := "request"
	if client.Timeout == 0 && timeout > 0 {
		timeoutScope = "response_headers"
	}
	details := fmt.Sprintf("provider_id=%d provider_name=%q method=%s upstream_url=%q model=%q stream=%t timeout_ms=%d timeout_scope=%s deadline_remaining_ms=%d", p.ID, safeUpstreamText(p.Name, p), req.Method, safeUpstreamText(endpoint, p), safeUpstreamText(fields.Model, p), fields.Stream, timeout.Milliseconds(), timeoutScope, deadlineMS)
	logging.Event(ctx, "INFO", "gateway.upstream.request_started", details)
	var mu sync.Mutex
	phase := "transport"
	setPhase := func(value string) { mu.Lock(); phase = value; mu.Unlock() }
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { setPhase("dns") },
		DNSDone: func(info httptrace.DNSDoneInfo) {
			if info.Err == nil {
				setPhase("connect")
			}
		},
		ConnectStart:      func(string, string) { setPhase("connect") },
		TLSHandshakeStart: func() { setPhase("tls") },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err == nil {
				setPhase("write_request")
			}
		},
		GotConn: func(httptrace.GotConnInfo) { setPhase("write_request") },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				setPhase("wait_headers")
			}
		},
		GotFirstResponseByte: func() { setPhase("read_headers") },
	}
	res, err := client.Do(req.WithContext(httptrace.WithClientTrace(ctx, trace)))
	if err == nil {
		observeFirstTokenBody(ctx, res)
	}
	mu.Lock()
	lastPhase := phase
	mu.Unlock()
	if err != nil {
		// url.Error includes the entire URL, including credential query parameters.
		detailErr := err
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			detailErr = urlErr.Err
		}
		logging.Printf(ctx, "ERROR", "gateway.upstream.request_failed", "%s phase=%s reason=%s duration_ms=%d context_error=%q err=%q", details, lastPhase, upstreamFailureReason(ctx, err), time.Since(start).Milliseconds(), safeUpstreamText(fmt.Sprint(context.Cause(ctx)), p), safeUpstreamText(detailErr.Error(), p))
		return res, err
	}
	upstreamID := upstreamResponseRequestID(res)
	level := "INFO"
	if res.StatusCode >= 400 {
		level = "WARN"
	}
	logging.Printf(ctx, level, "gateway.upstream.response_received", "%s phase=response_headers upstream_status=%d upstream_request_id=%q upstream_content_type=%q duration_ms=%d", details, res.StatusCode, safeUpstreamText(upstreamID, p), safeUpstreamText(res.Header.Get("Content-Type"), p), time.Since(start).Milliseconds())
	return res, nil
}

func upstreamResponseRequestID(res *http.Response) string {
	for _, name := range []string{"X-Request-ID", "Request-ID", "X-Amzn-RequestId"} {
		if value := res.Header.Get(name); httpapi.ValidRequestID(value) {
			return value
		}
	}
	return ""
}

func safeUpstreamText(value string, p provider) string {
	for _, secret := range []string{p.APIKey, p.AccessToken, p.RefreshToken} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return logging.Sanitize(value, 512)
}

func upstreamFailureReason(ctx context.Context, err error) string {
	if errors.Is(context.Cause(ctx), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "request_canceled"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns_error"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	var certErr *tls.CertificateVerificationError
	var unknownCA x509.UnknownAuthorityError
	var hostname x509.HostnameError
	if errors.As(err, &certErr) || errors.As(err, &unknownCA) || errors.As(err, &hostname) {
		return "tls_error"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return "connection_error"
	}
	return "transport_error"
}

type upstreamDiagnosticKey struct{}

type upstreamDiagnosticState struct {
	mu      sync.Mutex
	summary string
}

func upstreamDiagnosticSummary(ctx context.Context) string {
	state, _ := ctx.Value(upstreamDiagnosticKey{}).(*upstreamDiagnosticState)
	if state == nil {
		return ""
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.summary
}

func safeUpstreamDiagnostic(value string, p provider) string {
	for _, secret := range []string{p.APIKey, p.AccessToken, p.RefreshToken} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return logging.Sanitize(value, 1024)
}

// observeUpstreamResult retains a bounded diagnostic without consuming error bodies
// needed by protocol adapters and retry classification.
func observeUpstreamResult(ctx context.Context, p provider, res *http.Response, err error) {
	summary := ""
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		summary = fmt.Sprintf("upstream_failure_reason=%s upstream_error=%q", upstreamFailureReason(ctx, err), safeUpstreamDiagnostic(err.Error(), p))
	} else if res != nil {
		summary = fmt.Sprintf("upstream_status=%d upstream_request_id=%q", res.StatusCode, safeUpstreamDiagnostic(upstreamResponseRequestID(res), p))
		if res.StatusCode >= http.StatusBadRequest {
			message := http.StatusText(res.StatusCode)
			if res.Body != nil {
				raw, readErr := io.ReadAll(io.LimitReader(res.Body, 16<<10))
				res.Body = &diagnosticResponseBody{Reader: io.MultiReader(bytes.NewReader(raw), res.Body), Closer: res.Body}
				message = upstreamErrorMessage(raw, message)
				if readErr != nil {
					message += "; read upstream error: " + readErr.Error()
				}
			}
			summary += fmt.Sprintf(" upstream_error=%q", safeUpstreamDiagnostic(message, p))
		}
	}
	if state, _ := ctx.Value(upstreamDiagnosticKey{}).(*upstreamDiagnosticState); state != nil {
		state.mu.Lock()
		state.summary = summary
		state.mu.Unlock()
	}
	if summary != "" && (err != nil || res != nil && res.StatusCode >= http.StatusBadRequest) {
		logging.Printf(ctx, "WARN", "gateway.upstream.failed", "provider_id=%d %s", p.ID, summary)
	}
}

type diagnosticResponseBody struct {
	io.Reader
	io.Closer
}

func upstreamErrorMessage(raw []byte, fallback string) string {
	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		if len(bytes.TrimSpace(raw)) > 0 {
			return string(raw)
		}
		return fallback
	}
	var details struct {
		Message string          `json:"message"`
		Code    json.RawMessage `json:"code"`
		Type    string          `json:"type"`
		Status  string          `json:"status"`
	}
	if json.Unmarshal(envelope.Error, &details) == nil && len(envelope.Error) > 0 {
		return fmt.Sprintf("code=%s type=%s status=%s message=%s", details.Code, details.Type, details.Status, details.Message)
	}
	var message string
	if json.Unmarshal(envelope.Error, &message) == nil && message != "" {
		return message
	}
	if envelope.Message != "" {
		return envelope.Message
	}
	if envelope.Detail != "" {
		return envelope.Detail
	}
	return fallback
}
