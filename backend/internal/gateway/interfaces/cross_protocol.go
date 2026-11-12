package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bosapi/backend/internal/gateway/domain"
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/logging"
)

func conversionEnvelope(source string, code errorcode.Code, err error) map[string]any {
	field := "body"
	reason := "protocol conversion failed"
	var failure *gatewayinfra.ConversionError
	if errors.As(err, &failure) {
		field, reason = failure.Field, failure.Reason
	}
	detail := map[string]any{"code": string(code), "type": "invalid_request_error", "param": field, "message": field + ": " + reason}
	switch code {
	case errorcode.UnsupportedUpstreamFeature, errorcode.UpstreamError, errorcode.UpstreamTimeout, errorcode.DeliveryInterrupted, errorcode.ClientDisconnected:
		detail["type"] = "api_error"
	}
	envelope := map[string]any{"error": detail}
	if source == "messages" {
		envelope["type"] = "error"
	} else if source == "gemini" {
		status, name := http.StatusBadGateway, "INTERNAL"
		switch code {
		case errorcode.InvalidRequest, errorcode.UnsupportedConversion:
			status, name = http.StatusBadRequest, "INVALID_ARGUMENT"
		case errorcode.RateLimited, errorcode.QuotaExceeded:
			status, name = http.StatusTooManyRequests, "RESOURCE_EXHAUSTED"
		case errorcode.UpstreamTimeout:
			status, name = http.StatusGatewayTimeout, "DEADLINE_EXCEEDED"
		}
		envelope = map[string]any{"error": map[string]any{"code": status, "status": name, "message": field + ": " + reason, "bos_code": string(code)}}
	}
	return envelope
}
func crossFailure(w http.ResponseWriter, r *http.Request, source string, status int, code errorcode.Code, err error) {
	recordConversionFailure(r, code, err)
	respond(w, status, conversionEnvelope(source, code, err))
}

// relayReplyShape reports only normalized block categories and text byte counts;
// provider output, tool arguments, identifiers and names are intentionally omitted.
func relayReplyShape(blocks []gatewayinfra.RelayBlock) string {
	counts := map[string]int{"text": 0, "tool_use": 0, "thinking": 0, "image": 0, "tool_result": 0, "other": 0}
	textBytes := 0
	for _, block := range blocks {
		kind := block.Type
		if _, ok := counts[kind]; !ok {
			kind = "other"
		}
		counts[kind]++
		if kind == "text" || kind == "thinking" || kind == "tool_result" {
			textBytes += len(block.Text)
		}
	}
	order := []string{"text", "tool_use", "thinking", "image", "tool_result", "other"}
	types := make([]string, 0, len(order))
	for _, kind := range order {
		if counts[kind] > 0 {
			types = append(types, kind+":"+strconv.Itoa(counts[kind]))
		}
	}
	return "response_block_count=" + strconv.Itoa(len(blocks)) + " response_block_types=" + strings.Join(types, ",") + " response_text_bytes=" + strconv.Itoa(textBytes)
}

func (a *app) crossProtocolAPI(w http.ResponseWriter, r *http.Request, p provider, source string) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestBodyLimit(r)))
	if err != nil {
		crossFailure(w, r, source, 400, errorcode.InvalidRequest, &gatewayinfra.ConversionError{Field: "body", Reason: "request body could not be read within the request limit"})
		return
	}
	if err := validateRelayHeaders(r, p, source); err != nil {
		crossFailure(w, r, source, 400, errorcode.UnsupportedConversion, err)
		return
	}
	q, err := gatewayinfra.ParseRelayRequest(raw, source, p.APIFormat)
	if err != nil {
		crossFailure(w, r, source, 400, errorcode.UnsupportedConversion, err)
		return
	}
	if q.GeminiThinkingBudget > 0 {
		logging.Printf(r.Context(), "WARN", "gateway.conversion.thinking_budget_advisory", "provider_id=%d source_protocol=gemini target_protocol=openai_chat param=generationConfig.thinkingConfig.thinkingBudget requested_budget=%d mapped_thinking=enabled budget_enforced=false", p.ID, q.GeminiThinkingBudget)
	}
	if source == "gemini" {
		metadata, ok := requestGemini(r)
		if !ok {
			crossFailure(w, r, source, 400, errorcode.InvalidRequest, &gatewayinfra.ConversionError{Field: "endpoint", Reason: "invalid Gemini operation"})
			return
		}
		q.Model = metadata.Model
		q.Stream = metadata.Operation == "streamGenerateContent"
		switch p.APIFormat {
		case "openai_chat", "":
			if authorization, ok := r.Context().Value(gatewayToolAuthorizationRequestKey{}).(gatewayinfra.ToolAuthorization); ok {
				ctx := context.WithValue(r.Context(), gatewayToolAuthorizationRequestKey{}, authorization.WithProtocolAlias(domain.ToolProtocolGemini, domain.ToolProtocolOpenAI))
				r = r.WithContext(ctx)
			}
		case "anthropic_messages":
			if authorization, ok := r.Context().Value(gatewayToolAuthorizationRequestKey{}).(gatewayinfra.ToolAuthorization); ok {
				ctx := context.WithValue(r.Context(), gatewayToolAuthorizationRequestKey{}, authorization.WithProtocolAlias(domain.ToolProtocolGemini, domain.ToolProtocolAnthropic))
				r = r.WithContext(ctx)
			}
		}
	}
	publicModel := q.Model
	if model := requestModel(r); model != "" && model != q.Model {
		crossFailure(w, r, source, 400, errorcode.UnsupportedConversion, &gatewayinfra.ConversionError{Field: "model", Reason: "body model must match the authorized request model"})
		return
	}
	if target := p.ModelMapping[q.Model]; target != "" {
		q.Model = target
	}
	if p.APIFormat == "gemini" && !validGeminiModel(q.Model) {
		recordGatewayFailure(r, "provider_configuration", errorcode.InvalidProvider, "model_mapping", "mapped model is invalid for Gemini")
		fail(w, 502, errorcode.InvalidProvider)
		return
	}
	payload, path, err := q.Upstream(p.APIFormat)
	if err != nil {
		crossFailure(w, r, source, 400, errorcode.UnsupportedConversion, err)
		return
	}
	upstreamRequest := r.Clone(r.Context())
	upstreamURL := *r.URL
	upstreamURL.RawQuery = ""
	upstreamRequest.URL = &upstreamURL
	res, cancel, err := a.nativeRoundTrip(upstreamRequest, p, path, payload)
	if err != nil {
		if source == "gemini" {
			nativeError(w, "gemini", upstreamErrorStatus(r.Context(), err), upstreamErrorCode(r.Context(), err))
		} else if source == "messages" {
			nativeError(w, "anthropic_messages", upstreamErrorStatus(r.Context(), err), upstreamErrorCode(r.Context(), err))
		} else {
			fail(w, upstreamErrorStatus(r.Context(), err), upstreamErrorCode(r.Context(), err))
		}
		return
	}
	defer cancel()
	defer res.Body.Close()
	if retry := res.Header.Get("Retry-After"); retry != "" {
		w.Header().Set("Retry-After", retry)
	}
	if res.StatusCode == 429 {
		if source == "gemini" {
			writeUpstreamLimitError(&nativeLimitWriter{ResponseWriter: w, format: "gemini"}, res, time.Now())
		} else if source == "messages" {
			writeUpstreamLimitError(&nativeLimitWriter{ResponseWriter: w, format: "anthropic_messages"}, res, time.Now())
		} else {
			writeUpstreamLimitError(w, res, time.Now())
		}
		return
	}
	if res.StatusCode >= 400 {
		status := upstreamStatus(res.StatusCode)
		if res.StatusCode == 400 {
			status = 400
		}
		if source == "gemini" {
			nativeError(w, "gemini", status, upstreamCode(res.StatusCode))
		} else if source == "messages" {
			nativeError(w, "anthropic_messages", status, upstreamCode(res.StatusCode))
		} else {
			fail(w, status, upstreamCode(res.StatusCode))
		}
		return
	}
	id := "relay_" + random(16)
	if q.Stream {
		if !strings.Contains(strings.ToLower(res.Header.Get("Content-Type")), "text/event-stream") {
			crossFailure(w, r, source, 502, errorcode.UnsupportedUpstreamFeature, &gatewayinfra.ConversionError{Field: "response.content-type", Reason: "expected an SSE stream"})
			return
		}
		a.crossProtocolStream(w, r, res.Body, q, p.APIFormat, source, publicModel, id)
		return
	}
	raw, err = io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil {
		code := recordResponseReadFailure(r, "response.body", err)
		status, _ := gatewayRequestResult(r, http.StatusOK, code)
		respond(w, status, conversionEnvelope(source, code, responseFieldError("body", "upstream response body read failed")))
		return
	}
	if len(raw) > 8<<20 {
		crossFailure(w, r, source, 502, errorcode.UnsupportedUpstreamFeature, responseFieldError("body", "upstream response could not be read within the conversion limit"))
		return
	}
	if value, err := gatewayinfra.DecodeProtocolJSON(raw); err == nil {
		if p.APIFormat != "openai_chat" && p.APIFormat != "" || gatewayinfra.ValidateChatRelayUsage(value) == nil {
			observeUsageValue(usageState(r), value, "response.completed")
		}
	}
	reply, err := gatewayinfra.ParseRelayReply(raw, p.APIFormat, id)
	if err != nil {
		crossFailure(w, r, source, 502, errorcode.UnsupportedUpstreamFeature, err)
		return
	}
	reply.IncludeThoughts = q.IncludeThoughts
	converted, err := reply.JSON(source, publicModel)
	if err != nil {
		setGatewayResponseShapeDiagnostic(r.Context(), relayReplyShape(reply.Blocks))
		crossFailure(w, r, source, 502, errorcode.UnsupportedUpstreamFeature, err)
		return
	}
	a.writeToolJSON(w, r, 200, converted)
}

func validateRelayHeaders(r *http.Request, p provider, source string) error {
	if r.Header.Get("OpenAI-Beta") != "" {
		return &gatewayinfra.ConversionError{Field: "headers.OpenAI-Beta", Reason: "OpenAI beta capabilities cannot be mapped to this upstream"}
	}
	if source == "messages" {
		for name, values := range r.Header {
			if len(values) == 0 {
				continue
			}
			lower := strings.ToLower(name)
			// Chat capabilities are validated from the body. Ignore beta hints
			// so new client releases do not require a header allowlist update;
			// the OpenAI adapter never forwards these headers upstream.
			if (p.APIFormat == "openai_chat" || p.APIFormat == "") && lower == "anthropic-beta" {
				continue
			}
			// The SDK sets this CORS hint in terminal requests too. BOS owns its
			// browser access policy and never forwards this hint upstream.
			if (p.APIFormat == "openai_chat" || p.APIFormat == "") && lower == "anthropic-dangerous-direct-browser-access" && len(values) == 1 && values[0] == "true" {
				continue
			}
			if strings.HasPrefix(lower, "anthropic-") && (lower != "anthropic-version" || len(values) != 1 || values[0] != "2023-06-01") {
				return &gatewayinfra.ConversionError{Field: "headers." + lower, Reason: "Anthropic capability/version headers require an Anthropic upstream"}
			}
		}
	}
	return nil
}

// crossProtocolStream is implemented separately from the native byte relay so
// source protocol authorization still runs on the converted executable content.
func sseJSON(event string, value any) []byte {
	payload, _ := json.Marshal(value)
	prefix := ""
	if event != "" {
		prefix = "event: " + event + "\n"
	}
	return []byte(prefix + "data: " + string(payload) + "\n\n")
}
