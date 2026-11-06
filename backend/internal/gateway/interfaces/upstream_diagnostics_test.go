package interfaces

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func captureUpstreamLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &output
}

func TestUpstreamTransportDiagnosticsRetainHTTPStatusAndIDWithoutConsumingBody(t *testing.T) {
	output := captureUpstreamLogs(t)
	const responseBody = `{"error":{"message":"PRIVATE_UPSTREAM_BODY"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "upstream-123")
		w.Header().Set("Authorization", "Bearer PRIVATE_RESPONSE_SECRET")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, responseBody)
	}))
	defer server.Close()
	a := testApp(t)
	a.client = server.Client()
	payload := []byte(`{"model":"test-model","messages":[{"role":"user","content":"PRIVATE_PROMPT"}]}`)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	response, err := a.doUpstream(a.client, req, provider{ID: 7, Name: "Channel with spaces", APIKey: "PRIVATE_PROVIDER_KEY"}, payload, a.client.Timeout)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != responseBody || response.StatusCode != 401 {
		t.Fatalf("response changed: status=%d body=%q err=%v", response.StatusCode, body, err)
	}
	logs := output.String()
	for _, wanted := range []string{"event=gateway.upstream.request_started", "event=gateway.upstream.response_received", "upstream_status=401", `upstream_request_id="upstream-123"`, "provider_id=7", `provider_name="Channel with spaces"`, `model="test-model"`, "duration_ms="} {
		if !strings.Contains(logs, wanted) {
			t.Fatalf("missing %q in %s", wanted, logs)
		}
	}
	for _, secret := range []string{"PRIVATE_PROVIDER_KEY", "PRIVATE_PROMPT", "PRIVATE_UPSTREAM_BODY", "PRIVATE_RESPONSE_SECRET"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("diagnostics leaked %s", secret)
		}
	}
}

func TestUpstreamDiagnosticsCaptureCancellationPhaseAndOmitURLSecrets(t *testing.T) {
	output := captureUpstreamLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		httptrace.ContextClientTrace(r.Context()).WroteRequest(httptrace.WroteRequestInfo{})
		cancel()
		return nil, context.Canceled
	})}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://user:PRIVATE_PASSWORD@example.com/v1/chat/completions?key=PRIVATE_QUERY", nil)
	a := testApp(t)
	_, err := a.doUpstream(client, req, provider{ID: 8}, nil, 20*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error changed: %v", err)
	}
	logs := output.String()
	for _, wanted := range []string{"reason=request_canceled", "phase=wait_headers", "timeout_ms=20000", "deadline_remaining_ms=-1"} {
		if !strings.Contains(logs, wanted) {
			t.Fatalf("missing %q in %s", wanted, logs)
		}
	}
	for _, secret := range []string{"PRIVATE_PASSWORD", "PRIVATE_QUERY"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("diagnostics leaked %s", secret)
		}
	}
}

func TestNativeStreamingHeaderTimerIsLoggedAsTimeout(t *testing.T) {
	output := captureUpstreamLogs(t)
	a := testApp(t)
	a.client = &http.Client{Timeout: 10 * time.Millisecond, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		httptrace.ContextClientTrace(r.Context()).WroteRequest(httptrace.WroteRequestInfo{})
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	_, cancel, err := a.nativeRoundTrip(r, provider{ID: 9, APIFormat: "anthropic_messages", BaseURL: "https://example.com"}, "messages", []byte(`{"model":"claude-test","stream":true}`))
	defer cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if !strings.Contains(output.String(), "reason=timeout") || strings.Contains(output.String(), "reason=request_canceled") {
		t.Fatalf("header timeout was misclassified: %s", output)
	}
}

func TestUpstreamFailureReasons(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason string
	}{
		{context.Canceled, "request_canceled"}, {context.DeadlineExceeded, "timeout"},
		{&net.DNSError{Err: "no such host"}, "dns_error"},
		{&net.OpError{Op: "dial", Err: errors.New("connection refused")}, "connection_error"},
		{errors.New("EOF"), "transport_error"},
	} {
		if got := upstreamFailureReason(context.Background(), tc.err); got != tc.reason {
			t.Errorf("%v: got %s, want %s", tc.err, got, tc.reason)
		}
	}
}

func TestUpstreamDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"openai", `{"error":{"message":"model unavailable","code":"model_not_found","type":"invalid_request_error"}}`, "model unavailable", 404},
		{"anthropic", `{"error":{"message":"invalid x-api-key","type":"authentication_error"}}`, "authentication_error", 401},
		{"gemini", `{"error":{"message":"quota exhausted","code":429,"status":"RESOURCE_EXHAUSTED"}}`, "RESOURCE_EXHAUSTED", 429},
		{"plain text", "upstream overloaded", "upstream overloaded", 503},
		{"empty", "", "Bad Gateway", 502},
		{"credentials", `{"error":{"message":"bad provider-secret oauth-secret refresh-secret\ninjected"}}`, "[REDACTED]", 403},
		{"large", strings.Repeat("x", 20000), "xxx", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), upstreamDiagnosticKey{}, &upstreamDiagnosticState{})
			res := &http.Response{StatusCode: tc.status, Header: http.Header{"X-Request-Id": []string{"upstream-123"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			observeUpstreamResult(ctx, provider{ID: 2, APIKey: "provider-secret", AccessToken: "oauth-secret", RefreshToken: "refresh-secret"}, res, nil)
			summary := upstreamDiagnosticSummary(ctx)
			if !strings.Contains(summary, tc.want) || !strings.Contains(summary, "upstream-123") {
				t.Fatalf("missing diagnostic: %s", summary)
			}
			if strings.Contains(summary, "provider-secret") || strings.Contains(summary, "oauth-secret") || strings.Contains(summary, "refresh-secret") || strings.ContainsAny(summary, "\r\n") {
				t.Fatalf("unsafe diagnostic: %s", summary)
			}
			raw, err := io.ReadAll(res.Body)
			if err != nil || string(raw) != tc.body {
				t.Fatalf("response body changed: %v", err)
			}
			observeUpstreamResult(ctx, provider{}, &http.Response{StatusCode: 200, Header: http.Header{"Request-Id": {"retry-success"}}}, nil)
			if got := upstreamDiagnosticSummary(ctx); got != `upstream_status=200 upstream_request_id="retry-success"` {
				t.Fatalf("successful retry did not replace failure evidence: %s", got)
			}
		})
	}
}

func TestUpstreamTransportDiagnosticOmitsURL(t *testing.T) {
	ctx := context.WithValue(context.Background(), upstreamDiagnosticKey{}, &upstreamDiagnosticState{})
	observeUpstreamResult(ctx, provider{ID: 2}, nil, &url.Error{Op: "Post", URL: "https://user:secret@example.com?key=hidden", Err: errors.New("connection refused")})
	summary := upstreamDiagnosticSummary(ctx)
	if !strings.Contains(summary, "connection refused") || strings.Contains(summary, "secret") || strings.Contains(summary, "hidden") {
		t.Fatal(summary)
	}
}

func TestRequestCompletedIncludesUpstreamDiagnostic(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	router := gin.New()
	a := &app{}
	router.Use(a.gatewayAccessLogMiddleware())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		res := &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"account disabled","code":"account_disabled"}}`))}
		observeUpstreamResult(c.Request.Context(), provider{ID: 2}, res, nil)
		c.Status(502)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "event=gateway.request.completed") {
			if !strings.Contains(line, "upstream_status=403") || !strings.Contains(line, "account disabled") {
				t.Fatal(line)
			}
			return
		}
	}
	t.Fatal("missing completed event")
}

func TestUpstreamCallPathsRecordFailure(t *testing.T) {
	for _, name := range []string{"direct", "native", "oauth"} {
		t.Run(name, func(t *testing.T) {
			logs := captureUpstreamLogs(t)
			const responseBody = `{"error":{"message":"model backend unavailable"}}`
			a := &app{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 502, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(responseBody))}, nil
			})}}
			ctx := context.WithValue(context.Background(), upstreamDiagnosticKey{}, &upstreamDiagnosticState{})
			p := provider{ID: 2, Type: "api_key", APIFormat: "openai_chat", BaseURL: "https://upstream.example/v1", APIKey: "secret"}
			var res *http.Response
			var err error
			switch name {
			case "direct":
				res, err = a.direct(ctx, p, http.MethodPost, "/chat/completions", []byte(`{}`))
			case "native":
				var cancel context.CancelFunc
				res, cancel, err = a.nativeRoundTrip(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx), p, "chat/completions", []byte(`{}`))
				defer cancel()
			case "oauth":
				res, err = a.upstreamRequest(ctx, p, "/responses", []byte(`{}`))
			}
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			summary := upstreamDiagnosticSummary(ctx)
			if !strings.Contains(summary, "upstream_status=502") || !strings.Contains(summary, "model backend unavailable") {
				t.Fatal(summary)
			}
			for _, event := range []string{"gateway.upstream.request_started", "gateway.upstream.response_received", "gateway.upstream.failed"} {
				if count := strings.Count(logs.String(), "event="+event); count != 1 {
					t.Fatalf("expected one %s event, got %d: %s", event, count, logs)
				}
			}
			body, err := io.ReadAll(res.Body)
			if err != nil || string(body) != responseBody {
				t.Fatalf("response body changed: body=%q err=%v", body, err)
			}
		})
	}
}
