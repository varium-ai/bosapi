package interfaces

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequestHeaderSecurityPayloadExcludesGatewayCredentials verifies that
// carrier headers are excluded only on the request-header scan path; body
// content is still eligible for credential checks.
func TestRequestHeaderSecurityPayloadExcludesGatewayCredentials(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	r.Header.Set("Authorization", "Bearer sk-proj-abcdefghijklmnopqrstuvwxyz123456")
	r.Header.Set("x-api-key", "sk-gw-header-secret")
	r.Header.Set("X-GOOG-API-KEY", "sk-gw-google-secret")
	r.Header.Set("X-Custom-Trace", "sk-proj-abcdefghijklmnopqrstuvwxyz123456")

	payload := string(requestHeaderSecurityPayload(r))
	if !strings.Contains(payload, "X-Custom-Trace") || !strings.Contains(payload, "sk-proj-abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("expected custom header in scan payload: %q", payload)
	}
	if strings.Contains(strings.ToLower(payload), "authorization") ||
		strings.Contains(strings.ToLower(payload), "x-api-key") ||
		strings.Contains(strings.ToLower(payload), "x-goog-api-key") {
		t.Fatalf("gateway credential header leaked into scan payload: %q", payload)
	}
}
