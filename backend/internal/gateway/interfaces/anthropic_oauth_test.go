package interfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bosapi/backend/internal/platform/errorcode"
)

func claudeOAuthTestProvider() provider {
	return provider{ID: 11, Type: "oauth", Platform: "anthropic", APIFormat: "anthropic_messages", BaseURL: "https://api.anthropic.com", AccessToken: "PRIVATE_ACCESS_TOKEN", RefreshToken: "PRIVATE_REFRESH_TOKEN", AccountID: "account-test"}
}

func TestAnthropicOAuthWireCompatibility(t *testing.T) {
	for _, path := range []string{"messages", "messages/count_tokens"} {
		streams := []bool{false, true}
		if path == "messages/count_tokens" {
			streams = []bool{false}
		}
		for _, stream := range streams {
			t.Run(path+map[bool]string{false: "/json", true: "/sse"}[stream], func(t *testing.T) {
				logs := captureUpstreamLogs(t)
				a := testApp(t)
				payload := strings.Replace(claudeCodeHistory, `"stream":true`, `"stream":false`, 1)
				if stream {
					payload = claudeCodeHistory
				}
				if path == "messages/count_tokens" {
					payload = `{"model":"claude-test","messages":[{"role":"user","content":"hello"}]}`
				}
				a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.URL.RequestURI() != "/v1/"+path+"?beta=true" {
						t.Fatalf("wrong endpoint: %s", r.URL)
					}
					if r.Header.Get("Authorization") != "Bearer PRIVATE_ACCESS_TOKEN" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
						t.Fatal("credential isolation failed")
					}
					if path == "messages/count_tokens" {
						if r.Header.Get("User-Agent") != "python-sdk/old" || r.Header.Get("Accept") != "text/event-stream" {
							t.Fatal("count_tokens client headers unexpectedly rewritten")
						}
					} else if r.Header.Get("User-Agent") != "claude-cli/"+claudeOAuthVersion+" (external, cli)" || r.Header.Get("X-Stainless-Runtime") != "node" || r.Header.Get("X-Stainless-Package-Version") != "0.94.0" || r.Header.Get("Accept") != "application/json" {
						t.Fatal("inconsistent client identity")
					}
					if r.Header.Get("X-Client-Request-Id") == "" || (r.Header.Get("X-Stainless-Helper-Method") == "stream") != stream {
						t.Fatal("missing request/stream identity")
					}
					for _, beta := range []string{"custom-beta", "second-beta", "oauth-2025-04-20", "claude-code-20250219"} {
						if !strings.Contains(r.Header.Get("Anthropic-Beta"), beta) {
							t.Fatalf("missing beta: %s", beta)
						}
					}
					if path == "messages" && strings.Contains(r.Header.Get("Anthropic-Beta"), "interleaved-thinking-2025-05-14") {
						t.Fatal("unrequested optional beta was forced on the account")
					}
					if path == "messages/count_tokens" && (!strings.Contains(r.Header.Get("Anthropic-Beta"), "token-counting-2024-11-01") || r.Header.Get("X-Stainless-Helper-Method") != "") {
						t.Fatal("missing token counting capability")
					}
					raw, _ := io.ReadAll(r.Body)
					var before, after map[string]json.RawMessage
					_ = json.Unmarshal([]byte(payload), &before)
					if err := json.Unmarshal(raw, &after); err != nil {
						t.Fatal(err)
					}
					for name, value := range before {
						if name != "system" && !bytes.Equal(after[name], value) {
							t.Fatalf("changed opaque field %s", name)
						}
					}
					if path == "messages/count_tokens" {
						if !bytes.Equal(raw, []byte(payload)) || after["metadata"] != nil {
							t.Fatal("count_tokens payload was adapted")
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"input_tokens":1}`))}, nil
					}
					var blocks []json.RawMessage
					_ = json.Unmarshal(after["system"], &blocks)
					var original []json.RawMessage
					_ = json.Unmarshal(before["system"], &original)
					if len(blocks) != len(original)+2 {
						t.Fatal("lost system blocks")
					}
					for i := range original {
						if !bytes.Equal(blocks[i+2], original[i]) {
							t.Fatal("changed system/cache instructions")
						}
					}
					if !bytes.Contains(blocks[0], []byte("cc_version="+claudeOAuthVersion+".")) {
						t.Fatal("missing matching attribution")
					}
					id := readClaudeIdentity(t, raw)
					if id["account_uuid"] != "account-test" || len(id["device_id"]) != 64 || len(id["session_id"]) != 36 {
						t.Fatal("invalid metadata identity")
					}
					if path == "messages/count_tokens" {
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"input_tokens":1}`))}, nil
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: PRIVATE_RESPONSE\n\n"))}, nil
				})}
				r := httptest.NewRequest("POST", "/v1/"+path+"?key=PRIVATE_QUERY", nil)
				r.Header.Set("User-Agent", "python-sdk/old")
				r.Header.Set("X-Stainless-Runtime", "python")
				r.Header.Set("X-Stainless-Package-Version", "old-version")
				r.Header.Set("Authorization", "Bearer PRIVATE_GATEWAY_KEY")
				r.Header.Set("X-Api-Key", "PRIVATE_CLIENT_KEY")
				r.Header.Set("Cookie", "PRIVATE_COOKIE")
				r.Header.Set("Proxy-Authorization", "PRIVATE_PROXY_KEY")
				r.Header.Set("Accept", "text/event-stream")
				r.Header.Add("Anthropic-Beta", "custom-beta")
				r.Header.Add("Anthropic-Beta", "second-beta,oauth-2025-04-20")
				res, cancel, err := a.nativeRoundTrip(r, claudeOAuthTestProvider(), path, []byte(payload))
				if err != nil {
					t.Fatal(err)
				}
				defer cancel()
				defer res.Body.Close()
				raw, _ := io.ReadAll(res.Body)
				if path == "messages/count_tokens" {
					if string(raw) != `{"input_tokens":1}` {
						t.Fatalf("invalid count_tokens JSON response: %s", raw)
					}
				} else if string(raw) != "data: PRIVATE_RESPONSE\n\n" {
					t.Fatal("response stream changed")
				}
				wantMode := "mode=adapted"
				if path == "messages/count_tokens" {
					wantMode = "mode=count_tokens"
				}
				if !strings.Contains(logs.String(), wantMode) {
					t.Fatal("missing compatibility diagnostics")
				}
				for _, secret := range []string{"PRIVATE_", "signed-history", "opaque-history", "previous reasoning", "account-test"} {
					if strings.Contains(logs.String(), secret) {
						t.Fatalf("log leaked %s", secret)
					}
				}
			})
		}
	}
}

func readClaudeIdentity(t *testing.T, payload []byte) map[string]string {
	t.Helper()
	var body struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	var id map[string]string
	if err := json.Unmarshal([]byte(body.Metadata.UserID), &id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAnthropicOAuthStableScopedSession(t *testing.T) {
	p := claudeOAuthTestProvider()
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	r = r.WithContext(context.WithValue(r.Context(), gatewayAPIKeyRequestKey{}, apiKey{ID: 22}))
	payload := []byte(`{"system":"PRIVATE_SYSTEM","metadata":{"user_id":"PRIVATE_CALLER","other":9007199254740993},"messages":[{"role":"user","content":"first"}]}`)
	adapt := func(r *http.Request, p provider, payload []byte) map[string]string {
		t.Helper()
		raw, native, err := prepareAnthropicOAuth(r, p, payload)
		if err != nil || native {
			t.Fatalf("unexpected adaptation: native=%t err=%v", native, err)
		}
		if !bytes.Contains(raw, []byte(`"other":9007199254740993`)) || !bytes.Contains(raw, []byte(`"text":"PRIVATE_SYSTEM"`)) {
			t.Fatal("metadata/system lost")
		}
		return readClaudeIdentity(t, raw)
	}
	first := adapt(r, p, payload)
	nextPayload := bytes.Replace(payload, []byte(`"content":"first"}]`), []byte(`"content":"first"},{"role":"assistant","content":"ok"},{"role":"user","content":"continue"}]`), 1)
	p.AccessToken = "rotated-token"
	next := adapt(r, p, nextPayload)
	if first["session_id"] != next["session_id"] || first["device_id"] != next["device_id"] {
		t.Fatal("session changed on continuation/token rotation")
	}
	otherKey := r.WithContext(context.WithValue(r.Context(), gatewayAPIKeyRequestKey{}, apiKey{ID: 23}))
	if adapt(otherKey, p, payload)["session_id"] == first["session_id"] {
		t.Fatal("session shared between gateway keys")
	}
	p.ID++
	if adapt(r, p, payload)["device_id"] == first["device_id"] {
		t.Fatal("client shared between providers")
	}
}

func TestAnthropicOAuthNativeBodyAndProxyIdentity(t *testing.T) {
	for _, ua := range []string{"claude-cli/2.1.321 (external, cli)", "proxy/1.0"} {
		t.Run(ua, func(t *testing.T) {
			a := testApp(t)
			payload := strings.Replace(claudeCodeHistory, `"model":`, `"metadata":{"user_id":"native-session"},"model":`, 1)
			payload = strings.Replace(payload, "client attribution", "x-anthropic-billing-header: cc_version=2.1.321.abc; cc_entrypoint=cli;", 1)
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				if string(raw) != payload {
					t.Fatal("native payload/cache prefix changed")
				}
				if r.Header.Get("User-Agent") != ua {
					t.Fatal("native client headers changed")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})}
			r := httptest.NewRequest("POST", "/v1/messages", nil)
			r.Header.Set("User-Agent", ua)
			res, cancel, err := a.nativeRoundTrip(r, claudeOAuthTestProvider(), "messages", []byte(payload))
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			res.Body.Close()
		})
	}
}

func TestAnthropicOAuthRejectsMalformedCompatibilityFields(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	for _, raw := range []string{`null`, `{`, `{"metadata":[]}`, `{"metadata":"secret"}`, `{"system":{}}`} {
		if _, _, err := prepareAnthropicOAuth(r, claudeOAuthTestProvider(), []byte(raw)); err == nil {
			t.Fatal("accepted malformed request")
		}
	}
}

func TestAnthropicOAuthValidationFailureIsClientErrorAndNeverTransport(t *testing.T) {
	logs := captureUpstreamLogs(t)
	a := testApp(t)
	called := false
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})}
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	_, _, err := a.nativeRoundTrip(r, claudeOAuthTestProvider(), "messages", []byte(`{"model":"claude-test","metadata":[],"messages":[]}`))
	if err == nil || upstreamErrorStatus(r.Context(), err) != http.StatusBadRequest || upstreamErrorCode(r.Context(), err) != errorcode.InvalidRequest {
		t.Fatalf("validation error mapping: err=%v status=%d", err, upstreamErrorStatus(r.Context(), err))
	}
	if called {
		t.Fatal("invalid request reached upstream")
	}
	if strings.Contains(logs.String(), "gateway.upstream.request_started") || strings.Contains(logs.String(), "gateway.upstream.failed") || strings.Contains(logs.String(), "transport_error") {
		t.Fatalf("client validation was diagnosed as an upstream failure: %s", logs.String())
	}
}

func TestAnthropicOAuthAttributionUTF16(t *testing.T) {
	// All three sampled UTF-16 units are ASCII in the first case. The second
	// samples surrogate halves; UTF-8 byte indexing would produce another hash.
	for text, expected := range map[string]string{"012345678901234567890": "a86", "0123😀a😀bcdefghijkl😀": "482"} {
		raw, _ := json.Marshal(text)
		blocks, _ := json.Marshal([]map[string]string{{"type": "text", "text": text}})
		if claudeAttributionFingerprint(raw) != expected || claudeAttributionFingerprint(blocks) != expected {
			t.Fatal("inconsistent attribution formats")
		}
	}
}
