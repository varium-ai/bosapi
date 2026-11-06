package interfaces

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUpstream429ClassificationAndRetry(t *testing.T) {
	now := time.Date(2026, 10, 13, 0, 0, 0, 500000000, time.UTC)
	for _, test := range []struct {
		name, body, header, code, reason, retry string
	}{
		{"QPS", `{"error":{"code":"rate_limit_exceeded","message":"private provider detail"}}`, "3", "rate_limited", "upstream_rate_limit", "3"},
		{"missing retry", `{"error":{"type":"rate_limit_error"}}`, "", "rate_limited", "upstream_rate_limit", "1"},
		{"HTTP date", `{}`, now.Add(3 * time.Second).Format(http.TimeFormat), "rate_limited", "upstream_rate_limit", "3"},
		{"invalid retry", `{}`, "invalid", "rate_limited", "upstream_rate_limit", "1"},
		{"huge retry", `{}`, "9223372036854775807", "rate_limited", "upstream_rate_limit", "1"},
		{"non-JSON", "private provider detail", "2", "rate_limited", "upstream_rate_limit", "2"},
		{"quota code", `{"error":{"code":"insufficient_quota","message":"private provider detail"}}`, "", "quota_exceeded", "upstream_quota", ""},
		{"quota type", `{"error":{"type":"quota_exceeded"}}`, "", "quota_exceeded", "upstream_quota", ""},
		{"billing", `{"error":{"code":"billing_hard_limit_reached"}}`, "", "quota_exceeded", "upstream_quota", ""},
		{"resetting quota", `{"error":{"code":"quota_exceeded"}}`, "60", "quota_exceeded", "upstream_quota", "60"},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{test.header}}, Body: io.NopCloser(strings.NewReader(test.body))}
			response := httptest.NewRecorder()
			response.Header().Set("X-Request-ID", "upstream-retry-test")
			writeUpstreamLimitError(response, upstream, now)
			assertGatewayLimitError(t, response, test.code, test.reason, test.retry, "")
			if strings.Contains(response.Body.String(), "private provider detail") {
				t.Fatal("provider detail leaked")
			}
		})
	}
}

func TestProxyAPIKeepsUpstream429AndRetry(t *testing.T) {
	a := testApp(t)
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"7"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"rate_limit_exceeded"}}`))}, nil
	})}
	response := httptest.NewRecorder()
	response.Header().Set("X-Request-ID", "proxy-limit-test")
	request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"test","input":"hello"}`))
	a.proxyAPI(response, request, provider{BaseURL: "https://api.openai.com/v1", APIKey: "test-provider-key"}, "/embeddings")
	assertGatewayLimitError(t, response, "rate_limited", "upstream_rate_limit", "7", "")
}
