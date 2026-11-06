package interfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bosapi/backend/internal/platform/errorcode"
)

// API key adapters own protocol-specific authentication. Client credentials are
// never forwarded; only the selected provider's credential is sent upstream.
type apiKeyAdapter interface {
	headers(http.Header, http.Header, string)
	accepts(string, string) bool
}

type openAIAdapter struct{}

func (openAIAdapter) headers(dst, src http.Header, key string) {
	dst.Set("Authorization", "Bearer "+key)
}
func (openAIAdapter) accepts(method, path string) bool { return true }

type anthropicAdapter struct{}

func (anthropicAdapter) headers(dst, src http.Header, key string) {
	// Anthropic capability headers are an open list; new client releases can
	// introduce headers without requiring a BOS release.
	for name, values := range src {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "anthropic-") {
			dst[name] = append([]string(nil), values...)
		}
	}
	dst.Set("x-api-key", key)
	version := src.Get("anthropic-version")
	if version == "" {
		version = "2023-06-01"
	}
	dst.Set("anthropic-version", version)
	if beta := strings.Join(src.Values("anthropic-beta"), ","); beta != "" {
		dst.Set("anthropic-beta", beta)
	}
}
func (anthropicAdapter) accepts(method, path string) bool {
	return method == http.MethodPost && (path == "messages" || path == "messages/count_tokens")
}

type geminiAdapter struct{}

func (geminiAdapter) headers(dst, src http.Header, key string) { dst.Set("x-goog-api-key", key) }
func (geminiAdapter) accepts(method, path string) bool {
	_, action, ok := geminiModelAction(path)
	return method == http.MethodPost && ok && geminiOperationSupported(action)
}
func adapterFor(format string) apiKeyAdapter {
	switch format {
	case "anthropic_messages":
		return anthropicAdapter{}
	case "gemini":
		return geminiAdapter{}
	default:
		return openAIAdapter{}
	}
}

func nativeError(w http.ResponseWriter, format string, status int, code errorcode.Code) {
	if format == "gemini" {
		name := "INTERNAL"
		switch status {
		case 400:
			name = "INVALID_ARGUMENT"
		case 401:
			name = "UNAUTHENTICATED"
		case 403:
			name = "PERMISSION_DENIED"
		case 404:
			name = "NOT_FOUND"
		case 429:
			name = "RESOURCE_EXHAUSTED"
		case 504:
			name = "DEADLINE_EXCEEDED"
		}
		respond(w, status, map[string]any{"error": map[string]any{"code": status, "status": name, "message": gatewayErrorMessage(code), "bos_code": string(code)}})
		return
	}
	kind := "api_error"
	switch status {
	case 400:
		kind = "invalid_request_error"
	case 404:
		kind = "not_found_error"
	case 429:
		kind = "rate_limit_error"
	}
	respond(w, status, map[string]any{"type": "error", "error": map[string]any{"type": kind, "message": gatewayErrorMessage(code), "code": string(code)}})
}

// Native requests preserve unknown fields; subscription envelopes are added by
// nativeRoundTrip after model aliases and the existing security policy apply.
func (a *app) nativeAPI(w http.ResponseWriter, r *http.Request, p provider, path string) {
	adapter := adapterFor(p.APIFormat)
	if !adapter.accepts(r.Method, path) {
		recordGatewayFailure(r, "request_validation", errorcode.InvalidRequest, "endpoint", "method or endpoint is not supported by the native protocol")
		nativeError(w, p.APIFormat, 400, errorcode.InvalidRequest)
		return
	}
	if p.Type == "oauth" && p.Platform == "antigravity" && strings.HasSuffix(path, ":countTokens") {
		nativeError(w, p.APIFormat, http.StatusForbidden, errorcode.ProviderCapabilityUnavailable)
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestBodyLimit(r)))
	if err != nil || !json.Valid(payload) {
		recordRequestBodyFailure(r, err)
		nativeError(w, p.APIFormat, 400, errorcode.InvalidRequest)
		return
	}
	if p.APIFormat == "gemini" {
		model, action, _ := geminiModelAction(path)
		if metadata, ok := requestGemini(r); ok {
			model, action = metadata.Model, metadata.Operation
		}
		path = "models/" + model + ":" + action
		if target := p.ModelMapping[model]; target != "" {
			path = "models/" + target + ":" + action
			if !adapter.accepts(r.Method, path) {
				recordGatewayFailure(r, "provider_configuration", errorcode.InvalidProvider, "model_mapping", "mapped model or operation is invalid for Gemini")
				nativeError(w, p.APIFormat, 502, errorcode.InvalidProvider)
				return
			}
		}
	} else {
		payload = rewriteModelInJSON(payload, p.ModelMapping)
	}
	res, cancel, err := a.nativeRoundTrip(r, p, path, payload)
	if err != nil {
		nativeError(w, p.APIFormat, upstreamErrorStatus(r.Context(), err), upstreamErrorCode(r.Context(), err))
		return
	}
	defer cancel()
	defer res.Body.Close()
	if p.APIFormat == "anthropic_messages" {
		for name, values := range res.Header {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "anthropic-") || lower == "request-id" || lower == "x-should-retry" {
				w.Header()[name] = append([]string(nil), values...)
			}
		}
		if res.StatusCode >= 400 && a.anthropicUpstreamError(w, r, p, res) {
			return
		}
	}
	if res.StatusCode == http.StatusTooManyRequests {
		writeUpstreamLimitError(&nativeLimitWriter{ResponseWriter: w, format: p.APIFormat}, res, time.Now())
		return
	}
	if res.StatusCode >= 400 {
		if retry := res.Header.Get("Retry-After"); retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		status := upstreamStatus(res.StatusCode)
		if res.StatusCode == 400 {
			status = 400
		}
		nativeError(w, p.APIFormat, status, upstreamCode(res.StatusCode))
		return
	}
	if strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
	}
	a.forwardInspectedBody(w, r, res.Body, res.StatusCode, res.Header.Get("Content-Type"))
}

// Keep the shared quota classification/backoff rules, with a native envelope.
type nativeLimitWriter struct {
	http.ResponseWriter
	format string
	status int
}

func (w *nativeLimitWriter) WriteHeader(status int) { w.status = status }
func (w *nativeLimitWriter) Write(data []byte) (int, error) {
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return 0, err
	}
	details, _ := value["error"].(map[string]any)
	if w.format == "gemini" {
		details["bos_code"] = details["code"]
		details["code"] = w.status
		details["status"] = "RESOURCE_EXHAUSTED"
	} else {
		value["type"] = "error"
		details["type"] = "rate_limit_error"
	}
	respond(w.ResponseWriter, w.status, value)
	return len(data), nil
}

// Claude Code recognizes capability rejection by upstream error wording. Keep
// valid Anthropic error bodies, but never echo either side's credential.
func (a *app) anthropicUpstreamError(w http.ResponseWriter, r *http.Request, p provider, res *http.Response) bool {
	raw, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		nativeError(w, p.APIFormat, 502, errorcode.UpstreamError)
		return true
	}
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Error.Type == "" || envelope.Error.Message == "" {
		// Shared handling needs an unread body, including 429 quota classification.
		res.Body = io.NopCloser(strings.NewReader(string(raw)))
		return false
	}
	if retry := res.Header.Get("Retry-After"); retry != "" {
		if delay, err := http.ParseTime(retry); err == nil {
			retry = strconv.FormatInt(max(int64(1), int64(time.Until(delay).Seconds()+0.999)), 10)
		}
		w.Header().Set("Retry-After", retry)
	}
	if res.StatusCode == 429 {
		copyResponse := *res
		copyResponse.Body = io.NopCloser(strings.NewReader(string(raw)))
		rec := &recordingWriter{header: make(http.Header)}
		writeUpstreamLimitError(rec, &copyResponse, time.Now())
		for name, values := range rec.header {
			if name != "Content-Type" {
				w.Header()[name] = values
			}
		}
	}
	for _, secret := range []string{p.APIKey, requestSecret(r)} {
		if secret == "" {
			continue
		}
		encoded, _ := json.Marshal(secret)
		raw = []byte(strings.ReplaceAll(string(raw), string(encoded[1:len(encoded)-1]), "[Credential hidden]"))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(raw)
	return true
}

// nativeRoundTrip is shared by native pass-through and cross-protocol relays.
// The caller owns both the body and cancellation lifetime.
func (a *app) nativeRoundTrip(r *http.Request, p provider, path string, payload []byte) (*http.Response, func(), error) {
	endpoint, err := providerEndpoint(p, "/"+path)
	geminiOAuth := codeAssistSubscription(p)
	codeAssist := geminiOAuth && !strings.HasSuffix(path, ":countTokens")
	if geminiOAuth {
		endpoint, payload, err = geminiSubscriptionRequest(p, path, payload)
	}
	if err != nil {
		observeUpstreamResult(r.Context(), p, nil, err)
		return nil, func() {}, err
	}
	parsed, _ := url.Parse(endpoint)
	query := parsed.Query()
	// Allow only protocol options, never a client API key or arbitrary query.
	if p.APIFormat == "gemini" && (codeAssist || r.URL.Query().Get("alt") == "sse" || strings.HasSuffix(path, ":streamGenerateContent")) {
		query.Set("alt", "sse")
	}
	if p.APIFormat == "anthropic_messages" && r.URL.Query().Get("beta") == "true" {
		query.Set("beta", "true")
	}
	parsed.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(r.Context(), r.Method, parsed.String(), strings.NewReader(string(payload)))
	if err != nil {
		observeUpstreamResult(r.Context(), p, nil, err)
		return nil, func() {}, err
	}
	req.Header.Set("Content-Type", "application/json")
	providerHeaders(req.Header, r.Header, p)
	var fields struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(payload, &fields)
	streaming := fields.Stream
	if p.APIFormat == "gemini" {
		streaming = strings.HasSuffix(path, ":streamGenerateContent")
	}
	if state := usageState(r); state != nil {
		state.streaming = streaming
	}
	if accept := r.Header.Get("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	}
	// For streaming, bound time to response headers rather than the whole turn.
	client := *a.client
	if p.Type == "oauth" {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	cancel := func() {}
	var timer *time.Timer
	var expired chan struct{}
	if (streaming || codeAssist) && client.Timeout > 0 {
		var ctx context.Context
		var cancelCause context.CancelCauseFunc
		ctx, cancelCause = context.WithCancelCause(req.Context())
		cancel = func() { cancelCause(context.Canceled) }
		req = req.WithContext(ctx)
		expired = make(chan struct{})
		timer = time.AfterFunc(client.Timeout, func() { cancelCause(context.DeadlineExceeded); close(expired) })
		client.Timeout = 0
	}
	res, err := a.doUpstream(&client, req, p, payload, a.client.Timeout)
	if timer != nil && !timer.Stop() {
		<-expired
		if res != nil {
			res.Body.Close()
		}
		cancel()
		observeUpstreamResult(req.Context(), p, nil, context.DeadlineExceeded)
		return nil, func() {}, context.DeadlineExceeded
	}
	observeUpstreamResult(req.Context(), p, res, err)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}

	if codeAssist && res.StatusCode < 400 {
		res.Body = &geminiSubscriptionBody{source: res.Body, reader: bufio.NewReader(res.Body), streaming: streaming, aggregate: !streaming}
		if !streaming {
			res.Header.Set("Content-Type", "application/json")
		}
		res.ContentLength = -1
		res.Header.Del("Content-Length")
	}
	return res, cancel, nil
}
