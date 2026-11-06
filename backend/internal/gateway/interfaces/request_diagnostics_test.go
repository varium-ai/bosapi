package interfaces

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/logging"
)

func completedDiagnostic(t *testing.T, logs string) string {
	t.Helper()
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "event=gateway.request.completed") {
			return line
		}
	}
	t.Fatal("missing completed event")
	return ""
}

func TestConversionFailureDiagnosticsThroughGateway(t *testing.T) {
	const requestBody = `{"model":"alias","messages":[{"role":"user","content":"PRIVATE_PROMPT"}]}`
	for _, tc := range []struct {
		name, body, header, upstream, param, stage string
		status                                     int
		stream                                     bool
	}{
		{name: "missing model", body: `{"messages":[{"role":"user","content":"PRIVATE_PROMPT"}]}`, param: "model", stage: "request_conversion", status: 400},
		{name: "unsupported field", body: strings.Replace(requestBody, `"model":`, `"speed":"fast","model":`, 1), param: "speed", stage: "request_conversion", status: 400},
		{name: "unsupported header", body: requestBody, header: "unsupported-version", param: "headers.anthropic-version", stage: "request_conversion", status: 400},
		{name: "malformed JSON", body: `{"model":"alias","messages":PRIVATE_PROMPT}`, param: "body", stage: "request_conversion", status: 400},
		{name: "bad upstream JSON", body: requestBody, upstream: "PRIVATE_UPSTREAM_BODY", param: "body", stage: "response_conversion", status: 502},
		{name: "incomplete stream", body: strings.Replace(requestBody, `"model":`, `"stream":true,"model":`, 1), upstream: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"PRIVATE_OUTPUT\"},\"finish_reason\":null}]}\n\n", param: "response.stream", stage: "response_conversion", status: 200, stream: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := captureUpstreamLogs(t)
			sink := &diagnosticLogSink{}
			logging.SetSink(sink)
			t.Cleanup(func() { logging.SetSink(nil) })
			a, key := crossTestApp(t, "openai_chat")
			called := false
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				kind := "application/json"
				if tc.stream {
					kind = "text/event-stream"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(tc.upstream))}, nil
			})}
			r := httptest.NewRequest(http.MethodPost, "/v1/messages?key=PRIVATE_QUERY", strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("X-Request-ID", "req_conversion_diagnostic")
			if tc.header != "" {
				r.Header.Set("anthropic-version", tc.header)
			}
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != tc.status || called != (tc.upstream != "") {
				t.Fatalf("status=%d called=%t body=%s", w.Code, called, w.Body.String())
			}
			code := "unsupported_conversion"
			if tc.stage == "response_conversion" {
				code = "unsupported_upstream_feature"
			}
			if !strings.Contains(w.Body.String(), code) || !strings.Contains(w.Body.String(), `"param":"`+tc.param+`"`) {
				t.Fatalf("client error lost detail: %s", w.Body.String())
			}
			line := completedDiagnostic(t, output.String())
			for _, want := range []string{"level=ERROR", "request_id=req_conversion_diagnostic", "outcome=error", "error_code=" + code, "failure_stage=" + tc.stage, `error_param="` + tc.param + `"`, "error_reason="} {
				if !strings.Contains(line, want) {
					t.Fatalf("missing %q in %s", want, line)
				}
			}
			persisted := false
			for _, record := range sink.records {
				if record.Event == "gateway.request.completed" && record.RequestID == "req_conversion_diagnostic" && strings.Contains(record.Message, `error_param="`+tc.param+`"`) && strings.Contains(record.Message, "failure_stage="+tc.stage) {
					persisted = true
				}
			}
			if !persisted {
				t.Fatal("completed diagnostic did not reach the persistence sink")
			}
			for _, secret := range []string{key, "provider-key", "PRIVATE_PROMPT", "PRIVATE_OUTPUT", "PRIVATE_UPSTREAM_BODY", "PRIVATE_QUERY"} {
				if strings.Contains(output.String(), secret) {
					t.Fatalf("diagnostic leaked %s: %s", secret, output)
				}
			}
		})
	}
}

func TestRequestValidationDiagnosticsDoNotExposeParserInput(t *testing.T) {
	for _, format := range []string{"openai_chat", "anthropic_messages"} {
		t.Run(format, func(t *testing.T) {
			logs := captureUpstreamLogs(t)
			a, key := crossTestApp(t, format)
			path := "/v1/chat/completions"
			if format == "anthropic_messages" {
				path = "/v1/messages"
			}
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"PRIVATE_PROMPT":`))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			line := completedDiagnostic(t, logs.String())
			if w.Code != 400 || !strings.Contains(line, "failure_stage=request_validation") || !strings.Contains(line, `error_reason="expected valid JSON"`) || strings.Contains(logs.String(), "PRIVATE_PROMPT") {
				t.Fatalf("status=%d logs=%s", w.Code, logs)
			}
		})
	}
}

func TestGatewayDiagnosticRedactsBothCredentialsAndUnknownErrors(t *testing.T) {
	logs := captureUpstreamLogs(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("Authorization", "Bearer caller-private-secret")
	r = withGatewayProvider(r, provider{ID: 2, Name: "provider-private-secret", APIKey: "provider-private-secret"})
	r = r.WithContext(context.WithValue(r.Context(), gatewayDiagnosticKey{}, &gatewayDiagnosticState{}))
	recordConversionFailure(r, errorcode.UnsupportedConversion, &gatewayinfra.ConversionError{Field: "caller-private-secret\nfield", Reason: "provider-private-secret\nreason"})
	_, summary := gatewayFailureDiagnostic(r.Context())
	if strings.ContainsAny(summary, "\r\n") || !strings.Contains(summary, "[REDACTED]") {
		t.Fatal(summary)
	}
	recordConversionFailure(r, errorcode.UnsupportedConversion, errors.New("PRIVATE_PARSER_INPUT"))
	for _, secret := range []string{"caller-private-secret", "provider-private-secret", "PRIVATE_PARSER_INPUT"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("leaked %s in %s", secret, logs)
		}
	}
}

func TestSuccessfulStreamCompletionHasNoFailureDiagnostic(t *testing.T) {
	logs := captureUpstreamLogs(t)
	a, key := crossTestApp(t, "openai_chat")
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(chatTestStream()))}, nil
	})}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(strings.Replace(crossMessagesRequest, `"model":`, `"stream":true,"model":`, 1)))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	line := completedDiagnostic(t, logs.String())
	if w.Code != 200 || !strings.Contains(line, "outcome=success") || strings.Contains(line, "failure_stage=") || strings.Contains(logs.String(), "event=gateway.request.failed") {
		t.Fatalf("status=%d logs=%s", w.Code, logs)
	}
}

type interruptedDiagnosticReader struct{}

func (interruptedDiagnosticReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestNativeStreamReadFailureIsVisibleAfterHTTP200(t *testing.T) {
	logs := captureUpstreamLogs(t)
	a, key := crossTestApp(t, "anthropic_messages")
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := io.MultiReader(strings.NewReader("event: ping\ndata: {\"type\":\"ping\"}\n\n"), interruptedDiagnosticReader{})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(body)}, nil
	})}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"alias","stream":true,"messages":[{"role":"user","content":"PRIVATE_PROMPT"}]}`))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	line := completedDiagnostic(t, logs.String())
	for _, want := range []string{"level=ERROR", "status=200", "outcome=error", "error_code=upstream_error", "failure_stage=response_read", "unexpected EOF"} {
		if !strings.Contains(line, want) {
			t.Fatalf("missing %q in %s", want, line)
		}
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event: ping") || strings.Contains(logs.String(), "PRIVATE_PROMPT") {
		t.Fatalf("status=%d body=%s logs=%s", w.Code, w.Body.String(), logs)
	}
	select {
	case event := <-a.usageQueue:
		if event.Status != 502 || event.ErrorCode != errorcode.UpstreamError {
			t.Fatalf("native stream read failure was recorded as success: %+v", event)
		}
	default:
		t.Fatal("missing usage event")
	}
}

func TestPermittedProviderClearsEarlierCandidateDenial(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r = r.WithContext(context.WithValue(r.Context(), gatewayDiagnosticKey{}, &gatewayDiagnosticState{}))
	setGatewayFailureDiagnostic(r.Context(), errorcode.InvalidProvider, `failure_stage=provider_policy error_param="provider_binding" error_reason="candidate is not bound"`)
	a := testApp(t)
	if denial := a.databasePolicyDenial(r.Context(), apiKey{}, 2, "test"); denial != "" {
		t.Fatal(denial)
	}
	status, code := gatewayRequestResult(r, 200, "")
	if status != 200 || code != "" {
		t.Fatalf("recovered route incorrectly failed: status=%d code=%s", status, code)
	}
}

type diagnosticLogSink struct{ records []logging.Record }

func (s *diagnosticLogSink) Append(_ context.Context, record logging.Record) error {
	s.records = append(s.records, record)
	return nil
}
