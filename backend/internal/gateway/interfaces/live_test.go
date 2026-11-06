package interfaces

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAPILiveCreatesSidebandBinding(t *testing.T) {
	a := testApp(t)
	key := "sk-gw-" + random(32)
	a.data.Providers = []provider{{ID: 1, Type: "api_key", Status: "active", BaseURL: "https://api.openai.com/v1", APIKey: "upstream-secret"}}
	a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.openai.com/v1/realtime/calls" || !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
			t.Fatal("invalid live upstream request")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Location": []string{"/v1/realtime/calls/call-test"}}, Body: io.NopCloser(strings.NewReader("answer-sdp"))}, nil
	})}
	r := httptest.NewRequest("POST", "/v1/live", strings.NewReader(`{"sdp":"offer-sdp","session":{"model":"gpt-realtime"}}`))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "answer-sdp" {
		t.Fatalf("status = %d", w.Code)
	}
	if call := a.liveCalls["call-test"]; call.ProviderID != 1 || call.KeyHash != digest(key) || time.Now().After(call.Expires) {
		t.Fatal("call ownership not recorded")
	}
}
