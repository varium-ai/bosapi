package interfaces

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"bosapi/backend/internal/platform/logging"
)

type diagnosticErrorReader struct{ err error }

func (reader diagnosticErrorReader) Read([]byte) (int, error) { return 0, reader.err }

type diagnosticWriteFailure struct {
	http.ResponseWriter
	writes, allowed int
}

func (writer *diagnosticWriteFailure) Write(payload []byte) (int, error) {
	writer.writes++
	if writer.writes > writer.allowed {
		return 0, errors.New("PRIVATE_WRITE_ERROR")
	}
	return writer.ResponseWriter.Write(payload)
}

func (writer *diagnosticWriteFailure) Flush() {
	writer.ResponseWriter.(http.Flusher).Flush()
}

func TestCrossStreamFailureDiagnosticsAndUsageAgree(t *testing.T) {
	start := strings.SplitAfterN(crossClaudeStream, "\n\n", 2)[0]
	unsupportedCaller := strings.Replace(crossClaudeStream, `"type":"tool_use",`, `"type":"tool_use","caller":{"type":"code_execution_20260120","tool_id":"PRIVATE_TOOL"},`, 1)
	for _, tc := range []struct {
		name, response, stage, code, param, reason, streamContext string
		readErr                                                   error
		writeFailure                                              bool
		allowedWrites, resultStatus                               int
	}{
		{name: "unsupported caller", response: unsupportedCaller, stage: "response_conversion", code: "unsupported_upstream_feature", param: "response.content_block.caller.type", reason: "only direct tool calls", streamContext: `stream_frame=5 stream_event="content_block_start" stream_block_index=1 stream_block_type="tool_use"`, resultStatus: 502},
		{name: "unsupported delta field", response: strings.Replace(crossClaudeStream, `"type":"text_delta",`, `"type":"text_delta","future_field":"PRIVATE_METADATA",`, 1), stage: "response_conversion", code: "unsupported_upstream_feature", param: "response.delta.future_field", reason: "unsupported in this protocol conversion", streamContext: `stream_frame=3 stream_event="content_block_delta" stream_block_index=0`, resultStatus: 502},
		{name: "read interrupted", response: start, readErr: io.ErrUnexpectedEOF, stage: "response_read", code: "upstream_error", param: "response.stream", reason: "unexpected EOF", streamContext: `stream_frame=2 stream_event="read"`, resultStatus: 502},
		{name: "read timeout", response: start, readErr: context.DeadlineExceeded, stage: "response_read", code: "upstream_timeout", param: "response.stream", reason: "timeout", resultStatus: 504},
		{name: "read canceled", response: start, readErr: context.Canceled, stage: "response_read", code: "upstream_error", param: "response.stream", reason: "request_canceled", resultStatus: 502},
		{name: "upstream error event", response: start + "event: error\ndata: " + `{"type":"error","error":{"type":"overloaded_error","message":"PRIVATE_PROVIDER_MESSAGE"}}` + "\n\n", stage: "upstream_stream", code: "upstream_error", param: "response.error", reason: "overloaded_error", streamContext: `stream_frame=2 stream_event="error"`, resultStatus: 502},
		{name: "unknown upstream error type", response: start + "event: error\ndata: " + `{"type":"error","error":{"type":"PRIVATE_PROVIDER_TYPE","message":"PRIVATE_PROVIDER_MESSAGE"}}` + "\n\n", stage: "upstream_stream", code: "upstream_error", param: "response.error", reason: "unknown", resultStatus: 502},
		{name: "oversized SSE line", response: start + "data: " + strings.Repeat("PRIVATE_OVERSIZE", 100000), stage: "response_conversion", code: "unsupported_upstream_feature", param: "response.stream", reason: "SSE frame exceeds limit", resultStatus: 502},
		{name: "client write interrupted", response: crossClaudeStream, writeFailure: true, stage: "response_write", code: "delivery_interrupted", param: "response.body", reason: "downstream response write failed", streamContext: `stream_frame=1 stream_event="message_start"`, resultStatus: 502},
		{name: "error response write preserves primary failure", response: unsupportedCaller, writeFailure: true, allowedWrites: 2, stage: "response_conversion", code: "unsupported_upstream_feature", param: "response.content_block.caller.type", reason: "only direct tool calls", resultStatus: 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureUpstreamLogs(t)
			sink := &diagnosticLogSink{}
			logging.SetSink(sink)
			t.Cleanup(func() { logging.SetSink(nil) })
			a, key := crossTestApp(t, "anthropic_messages")
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				var body io.Reader = strings.NewReader(tc.response)
				if tc.readErr != nil {
					body = io.MultiReader(body, diagnosticErrorReader{tc.readErr})
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "Request-Id": {"upstream-diagnostic-123"}}, Body: io.NopCloser(body)}, nil
			})}
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?key=PRIVATE_QUERY", strings.NewReader(strings.Replace(crossChatRequest, `"model":`, `"stream":true,"model":`, 1)))
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("X-Request-ID", "req_stream_diagnostic")
			recorder := httptest.NewRecorder()
			var writer http.ResponseWriter = recorder
			if tc.writeFailure {
				writer = &diagnosticWriteFailure{ResponseWriter: recorder, allowed: tc.allowedWrites}
			}
			a.routes().ServeHTTP(writer, r)
			line := completedDiagnostic(t, logs.String())
			for _, want := range []string{"level=ERROR", "status=200", "outcome=error", "error_code=" + tc.code, "failure_stage=" + tc.stage, `error_param="` + tc.param + `"`, tc.reason, tc.streamContext, "upstream_status=200", `upstream_request_id="upstream-diagnostic-123"`} {
				if !strings.Contains(line, want) {
					t.Fatalf("missing %q in completion: %s", want, line)
				}
			}
			persisted := false
			for _, record := range sink.records {
				if record.Event == "gateway.request.completed" && record.RequestID == "req_stream_diagnostic" && strings.Contains(record.Message, "failure_stage="+tc.stage) && strings.Contains(record.Message, tc.streamContext) {
					persisted = true
				}
			}
			if !persisted {
				t.Fatal("completion evidence did not reach request-log persistence")
			}
			select {
			case event := <-a.usageQueue:
				if event.Status != tc.resultStatus || string(event.ErrorCode) != tc.code || !strings.Contains(line, "result_status="+strconv.Itoa(tc.resultStatus)) {
					t.Fatalf("usage and completion differ: usage=%+v completion=%s", event, line)
				}
			default:
				t.Fatal("missing usage event")
			}
			if recorder.Code != 200 || strings.Contains(recorder.Body.String(), "data: [DONE]") {
				t.Fatalf("failed stream completed successfully: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			for _, secret := range []string{"PRIVATE_", key, "provider-key"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("diagnostic leaked %q: %s", secret, logs)
				}
			}
		})
	}
}

func TestCrossJSONReadFailureDiagnosticsAndUsageAgree(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		err        error
		status     int
	}{
		{"interrupted", "upstream_error", io.ErrUnexpectedEOF, 502},
		{"timeout", "upstream_timeout", context.DeadlineExceeded, 504},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureUpstreamLogs(t)
			a, key := crossTestApp(t, "anthropic_messages")
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(diagnosticErrorReader{tc.err})}, nil
			})}
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(crossChatRequest))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			line := completedDiagnostic(t, logs.String())
			if w.Code != tc.status || !strings.Contains(line, "failure_stage=response_read") || !strings.Contains(line, "error_code="+tc.code) || strings.Contains(line, "unsupported_upstream_feature") {
				t.Fatalf("JSON read failure misclassified: status=%d log=%s", w.Code, line)
			}
			select {
			case event := <-a.usageQueue:
				if event.Status != tc.status || string(event.ErrorCode) != tc.code {
					t.Fatalf("incorrect JSON failure usage: %+v", event)
				}
			default:
				t.Fatal("missing usage event")
			}
		})
	}
}

func TestNativeSSEErrorIsRecordedWithoutChangingResponse(t *testing.T) {
	for _, format := range []string{"anthropic_messages", "openai_chat"} {
		t.Run(format, func(t *testing.T) {
			logs := captureUpstreamLogs(t)
			a, key := crossTestApp(t, format)
			response := "event: ping\ndata: {\"type\":\"ping\"}\n\nevent: error\ndata: " + `{"type":"error","error":{"type":"overloaded_error","message":"PRIVATE_NATIVE_ERROR"}}` + "\n\n"
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "Request-Id": {"native-upstream-123"}}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(response)})}, nil
			})}
			path, body := "/v1/chat/completions", crossChatRequest
			if format == "anthropic_messages" {
				path, body = "/v1/messages", crossMessagesRequest
			}
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Replace(body, `"model":`, `"stream":true,"model":`, 1)))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			line := completedDiagnostic(t, logs.String())
			for _, want := range []string{"status=200", "result_status=502", "outcome=error", "error_code=upstream_error", "failure_stage=upstream_stream", "overloaded_error", `upstream_request_id="native-upstream-123"`} {
				if !strings.Contains(line, want) {
					t.Fatalf("missing %q in %s", want, line)
				}
			}
			if w.Code != 200 || w.Body.String() != response || strings.Contains(logs.String(), "PRIVATE_NATIVE_ERROR") {
				t.Fatalf("native response changed or leaked: status=%d body=%s logs=%s", w.Code, w.Body.String(), logs)
			}
			select {
			case event := <-a.usageQueue:
				if event.Status != 502 || string(event.ErrorCode) != "upstream_error" {
					t.Fatalf("native SSE failure recorded as success: %+v", event)
				}
			default:
				t.Fatal("missing usage event")
			}
		})
	}
}
