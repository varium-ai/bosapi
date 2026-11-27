package interfaces

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpstream429DiagnosticsDistinguishMissingRateHeaders(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "supplied"}[supplied], func(t *testing.T) {
			logs := captureUpstreamLogs(t)
			header := make(http.Header)
			if supplied {
				header.Set("Retry-After", "PRIVATE_RETRY_VALUE")
				header.Set("Anthropic-Ratelimit-Unified-Status", "PRIVATE_RATE_VALUE")
			}
			const body = `{"type":"error","error":{"type":"rate_limit_error","message":"Error"}}`
			res := &http.Response{StatusCode: 429, Header: header, Body: io.NopCloser(strings.NewReader(body))}
			observeUpstreamResult(context.Background(), claudeOAuthTestProvider(), res, nil)
			raw, err := io.ReadAll(res.Body)
			res.Body.Close()
			if err != nil || string(raw) != body {
				t.Fatal("diagnostics consumed upstream error")
			}
			flag := map[bool]string{false: "false", true: "true"}[supplied]
			for _, field := range []string{"upstream_retry_after_present=", "upstream_rate_limit_headers_present="} {
				if !strings.Contains(logs.String(), field+flag) {
					t.Fatal("missing rate-limit evidence")
				}
			}
			if strings.Contains(logs.String(), "PRIVATE_") {
				t.Fatal("arbitrary rate-limit header leaked")
			}
		})
	}
}

func TestAnthropicOAuthChatConversionUsesCompatibility(t *testing.T) {
	a := testApp(t)
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		if r.URL.RequestURI() != "/v1/messages?beta=true" || !strings.HasPrefix(r.Header.Get("User-Agent"), "claude-cli/") || !strings.Contains(string(raw), "x-anthropic-billing-header:") {
			t.Fatal("Chat conversion bypassed OAuth compatibility")
		}
		if !strings.Contains(string(raw), "caller system") {
			t.Fatal("Chat system instruction lost")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`))}, nil
	})}
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"claude-test","max_tokens":16,"messages":[{"role":"system","content":"caller system"},{"role":"user","content":"hello"}]}`))
	w := httptest.NewRecorder()
	a.crossProtocolAPI(w, r, claudeOAuthTestProvider(), "chat")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "OK") {
		t.Fatalf("Chat response: %d %s", w.Code, w.Body)
	}
}
