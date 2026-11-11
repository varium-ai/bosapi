package interfaces

import (
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCodexChatConversion(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[streaming], func(t *testing.T) {
			a := testApp(t)
			p := provider{ID: 1, Type: "oauth", Platform: "openai", APIFormat: "openai_responses", AccessToken: "secret", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour), ModelMapping: map[string]string{"alias": "actual"}}
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				body := string(raw)
				for _, want := range []string{`"model":"actual"`, `"stream":true`, `"store":false`, `"type":"function_call"`, `"call_id":"old"`, `"type":"function_call_output"`, `"name":"read"`} {
					if !strings.Contains(body, want) {
						t.Fatalf("missing %s: %s", want, body)
					}
				}
				if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("ChatGPT-Account-ID") != "account" {
					t.Fatal("bad oauth headers")
				}
				reply := `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hello","annotations":[]}]},{"type":"function_call","call_id":"new","name":"read","arguments":"{\"n\":9007199254740993}"}],"usage":{"input_tokens":10,"output_tokens":3,"input_tokens_details":{"cached_tokens":2}}}}`
				stream := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: " + reply + "\n\n"
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
			})}
			request := `{"model":"alias","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":null,"tool_calls":[{"id":"old","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"old","content":"done"}],"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}]`
			if streaming {
				request += `,"stream":true,"stream_options":{"include_usage":true}`
			}
			request += `}`
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(request))
			w := httptest.NewRecorder()
			r = r.WithContext(context.WithValue(r.Context(), gatewayToolAuthorizationRequestKey{}, gatewayinfra.ParseToolAuthorization([]byte(request))))
			a.codexChat(w, r, p)
			body := w.Body.String()
			if w.Code != 200 || !strings.Contains(body, "hello") || !strings.Contains(body, "new") || !strings.Contains(body, "9007199254740993") || !strings.Contains(body, `"model":"alias"`) {
				t.Fatalf("response %d %s", w.Code, body)
			}
			if streaming && (!strings.Contains(body, "[DONE]") || !strings.Contains(body, `"prompt_tokens":10`)) {
				t.Fatalf("stream %s", body)
			}
		})
	}
}

func TestCodexChatRejectsUnsupportedAndTruncated(t *testing.T) {
	a := testApp(t)
	p := provider{Type: "oauth", Platform: "openai"}
	for _, field := range []string{`"max_tokens":100,`, `"temperature":0.5,`, `"logprobs":true,`} {
		called := false
		a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { called = true; return nil, nil })}
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"x",`+field+`"messages":[{"role":"user","content":"hi"}]}`))
		w := httptest.NewRecorder()
		a.codexChat(w, r, p)
		if w.Code != 400 || called || !strings.Contains(w.Body.String(), "unsupported_conversion") {
			t.Fatalf("%s: %d %s", field, w.Code, w.Body.String())
		}
	}
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"))}, nil
	})}
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"x","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()
	a.codexChat(w, r, p)
	if !strings.Contains(w.Body.String(), "unsupported_upstream_feature") || strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("truncated: %s", w.Body.String())
	}
}

func TestCodexChatGatewayRouteAndToolPolicy(t *testing.T) {
	a, key := geminiTestApp(t)
	a.data.Providers[0] = provider{ID: 1, Type: "oauth", Platform: "openai", APIFormat: "openai_responses", Status: "active", AccessToken: "token", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour), Models: []string{"actual"}, ModelMapping: map[string]string{"alias": "actual"}}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		stream := `data: {"type":"response.completed","response":{"id":"id","status":"completed","output":[{"type":"function_call","call_id":"call","name":"other","arguments":"{\"secret\":1}"}],"usage":{"input_tokens":2,"output_tokens":1}}}` + "\n\n"
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
	})}
	request := `{"model":"alias","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}]}`
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(request))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "unauthorized_tool") || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("tool policy: %d %s", w.Code, w.Body.String())
	}
	select {
	case event := <-a.usageQueue:
		if event.Metrics.TotalTokens != 3 || event.Model != "alias" {
			t.Fatalf("usage: %+v", event)
		}
	default:
		t.Fatal("missing usage")
	}
}

func TestCodexChatGatewayReasoningEffort(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[streaming], func(t *testing.T) {
			a, key := geminiTestApp(t)
			a.data.Providers[0] = provider{ID: 1, Type: "oauth", Platform: "openai", APIFormat: "openai_responses", Status: "active", AccessToken: "token", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour), Models: []string{"actual"}, ModelMapping: map[string]string{"alias": "actual"}}
			called := false
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), `"reasoning":{"effort":"max"}`) || strings.Contains(string(raw), `"reasoning_effort"`) {
					t.Fatalf("bad upstream body: %s", raw)
				}
				stream := `data: {"type":"response.completed","response":{"id":"id","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1}}}` + "\n\n"
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
			})}
			request := `{"model":"alias","reasoning_effort":"max","messages":[{"role":"user","content":"test"}]`
			if streaming {
				request += `,"stream":true`
			}
			request += `}`
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(request))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if !called || w.Code != 200 || !strings.Contains(w.Body.String(), "ok") {
				t.Fatalf("response: %d %s", w.Code, w.Body.String())
			}
			if streaming && !strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("stream: %s", w.Body.String())
			}
		})
	}
}
