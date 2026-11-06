package interfaces

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"bosapi/backend/internal/gateway/domain"
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
)

func (a *app) writeToolJSON(w http.ResponseWriter, r *http.Request, status int, payload []byte) {
	if reqModel, ok := r.Context().Value(gatewayModelRequestKey{}).(string); ok && reqModel != "" {
		payload = restoreResponseModelJSON(payload, reqModel)
	}
	a.inspectModelContent(r, "model_output", payload)
	filtered := a.toolDeliveryFilter(r).JSON(payload)
	if len(filtered) == 0 {
		recordGatewayFailure(r, "response_read", errorcode.UpstreamError, "response.body", "response could not be delivered after tool inspection")
		fail(w, http.StatusBadGateway, errorcode.UpstreamError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Del("Content-Length")
	w.WriteHeader(status)
	if _, err := w.Write(filtered); err != nil {
		recordGatewayFailure(r, "response_write", errorcode.DeliveryInterrupted, "response.body", "downstream response write failed")
	}
}

func restoreResponseModelJSON(payload []byte, reqModel string) []byte {
	if len(payload) == 0 || reqModel == "" {
		return payload
	}
	var doc map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		return payload
	}
	if currentModel, ok := doc["model"].(string); ok && currentModel != "" && currentModel != reqModel {
		doc["model"] = reqModel
		if rewritten, err := json.Marshal(doc); err == nil {
			return rewritten
		}
	}
	return payload
}

func (a *app) forwardInspectedBody(w http.ResponseWriter, r *http.Request, body io.Reader, status int, contentType string) {
	kind := strings.ToLower(contentType)
	if state := usageState(r); state != nil && strings.Contains(kind, "text/event-stream") {
		state.streaming = true
	}
	inspectTools := toolDeliveryPath(gatewayPath(r))
	isJSON := strings.Contains(kind, "json")
	if isJSON && inspectTools {
		limit := int64(2 << 20)
		payload, err := io.ReadAll(io.LimitReader(body, limit+1))
		if int64(len(payload)) > limit && !a.toolSecurityBlocks(r) && err == nil {
			// Stop inspection at the configured bound, preserving a large response.
			a.observeToolCalls(r, nil, &domain.ToolCallParseError{Direction: domain.ToolDirectionResponse}, domain.ToolDirectionResponse)
			w.Header().Set("Content-Type", contentType)
			w.Header().Del("Content-Length")
			w.WriteHeader(status)
			if _, err := w.Write(payload); err == nil {
				_, _ = io.Copy(w, body)
			}
			return
		}
		if err != nil || int64(len(payload)) > limit {
			reason := "upstream response body read failed"
			if int64(len(payload)) > limit {
				reason = "upstream response exceeds inspection limit"
			}
			recordGatewayFailure(r, "response_read", errorcode.UpstreamError, "response.body", reason)
			fail(w, http.StatusBadGateway, errorcode.UpstreamError)
			return
		}
		var value map[string]any
		if json.Unmarshal(payload, &value) == nil {
			observeUsageValue(usageState(r), value, "response.completed")
		}
		a.writeToolJSON(w, r, status, payload)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Del("Content-Length")
	w.WriteHeader(status)
	writer := toolBodyWriter{app: a, request: r, writer: w, filter: a.toolDeliveryFilter(r), scanner: securityAuditScanner{app: a, request: r}, isSSE: strings.Contains(kind, "text/event-stream"), isJSON: isJSON, inspectTools: inspectTools}
	buffer := make([]byte, 4096)
	for {
		n, err := body.Read(buffer)
		if n > 0 && !writer.write(buffer[:n]) {
			return
		}
		if err != nil {
			writer.finish(err)
			return
		}
	}
}

type toolBodyWriter struct {
	app                         *app
	request                     *http.Request
	writer                      http.ResponseWriter
	filter                      *gatewayinfra.ToolDeliveryFilter
	scanner                     securityAuditScanner
	isSSE, isJSON, inspectTools bool
	usageBody                   []byte
}

func (writer *toolBodyWriter) observeStreamError(value map[string]any, event string) {
	if value["error"] != nil || value["type"] == "error" || value["type"] == "response.failed" || event == "error" {
		failure := newUpstreamStreamFailure(value)
		recordGatewayFailure(writer.request, "upstream_stream", errorcode.UpstreamError, "response.error", failure.Error())
	}
}

func (writer *toolBodyWriter) write(chunk []byte) bool {
	writer.scanner.Write(chunk)
	if writer.isJSON {
		writer.usageBody = appendUsageTail(writer.usageBody, chunk)
	}
	chunks := [][]byte{chunk}
	if writer.isSSE {
		observeSSEUsage(usageState(writer.request), chunk, false, writer.observeStreamError)
		if writer.inspectTools {
			chunks = writer.filter.SSE(chunk, false)
		}
	}
	if !writeToolChunks(writer.writer, chunks) {
		recordGatewayFailure(writer.request, "response_write", errorcode.DeliveryInterrupted, "response.body", "downstream response write failed")
		return false
	}
	return true
}

func (writer *toolBodyWriter) finish(err error) {
	if err != io.EOF {
		recordResponseReadFailure(writer.request, "response.body", err)
	}
	if writer.isSSE {
		observeSSEUsage(usageState(writer.request), nil, true, writer.observeStreamError)
		if writer.inspectTools {
			if !writeToolChunks(writer.writer, writer.filter.SSE(nil, true)) {
				recordGatewayFailure(writer.request, "response_write", errorcode.DeliveryInterrupted, "response.body", "downstream response write failed")
			}
		}
	} else if writer.isJSON {
		var value map[string]any
		if json.Unmarshal(writer.usageBody, &value) == nil {
			observeUsageValue(usageState(writer.request), value, "response.completed")
		}
	}
}

func toolDeliveryPath(path string) bool {
	if _, operation, ok := geminiModelAction(path); ok {
		return operation == "generateContent" || operation == "streamGenerateContent"
	}
	return path == "chat/completions" || path == "responses" || strings.HasPrefix(path, "responses/") || path == "messages" || strings.HasPrefix(path, "messages/")
}

func writeToolChunks(w http.ResponseWriter, chunks [][]byte) bool {
	for _, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		if _, err := w.Write(chunk); err != nil {
			return false
		}
	}
	if len(chunks) > 0 {
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	return true
}
