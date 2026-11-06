package interfaces

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	admindomain "bosapi/backend/internal/admin/domain"
	"github.com/coder/websocket"
)

func TestToolSecurityModeControlsMalformedHistoryAndRefreshesPerRequest(t *testing.T) {
	for _, messages := range []string{
		`[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"lookup","arguments":""}}]}]`,
		`[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"lookup","arguments":{}}}]}]`,
		`[{"role":"tool","content":"result"}]`,
		`[{"role":"assistant","tool_calls":{}}]`,
	} {
		t.Run(messages, func(t *testing.T) {
			sink := &toolRecordingSink{}
			a, secret := setupToolGateway(t, sink, `{}`, "application/json")
			mode := "observe"
			reads, forwarded := 0, 0
			a.toolSecurityPolicy = func(context.Context) (admindomain.ToolSecurityPolicy, error) {
				reads++
				return admindomain.ToolSecurityPolicy{Mode: mode}, nil
			}
			payload := `{"model":"test","tool_security_mode":"observe","messages":` + messages + `}`
			a.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(r.Body)
				if string(body) != payload {
					t.Fatalf("history changed: %s", body)
				}
				forwarded++
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})
			for _, choice := range []string{"observe", "block", "observe"} {
				mode = choice
				sink.events = nil
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(payload))
				r.Header.Set("Authorization", "Bearer "+secret)
				w := httptest.NewRecorder()
				a.routes().ServeHTTP(w, r)
				want := 200
				if mode == "block" {
					want = 400
				}
				if w.Code != want || len(sink.events) != 1 || sink.events[0].RuleID != "tool_call_invalid" || sink.events[0].Action != mode || sink.events[0].HTTPStatus != want {
					t.Fatalf("mode=%s status=%d events=%+v", mode, w.Code, sink.events)
				}
			}
			if forwarded != 2 || reads != 3 {
				t.Fatalf("forwarded=%d policy reads=%d", forwarded, reads)
			}
		})
	}
}

func TestObserveModePreservesResponseToolFailures(t *testing.T) {
	for _, test := range []struct {
		name, response, rule string
		failure              error
	}{
		{"malformed", `{"choices":[{"message":{"tool_calls":[{"id":"c","type":"function","function":{"name":"shell","arguments":""}}]}}]}`, "tool_call_invalid", nil},
		{"undeclared", `{"choices":[{"message":{"tool_calls":[{"id":"c","type":"function","function":{"name":"shell","arguments":"{}"}}]}}]}`, "unauthorized_tool", nil},
		{"matched rule", `{"choices":[{"message":{"tool_calls":[{"id":"c","type":"function","function":{"name":"shell","arguments":"{}"}}]}}]}`, "123", nil},
		{"inspection unavailable", `{"choices":[{"message":{"tool_calls":[{"id":"c","type":"function","function":{"name":"shell","arguments":"{}"}}]}}]}`, "tool_call_inspection_unavailable", errors.New("private failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			sink := &selectiveToolSink{toolRecordingSink: toolRecordingSink{failure: test.failure}}
			a, secret := setupToolGateway(t, sink, test.response, "application/json")
			a.toolSecurityPolicy = nil // Default production mode is observe.
			payload := `{"model":"test","messages":[]}`
			if test.name != "undeclared" {
				payload = `{"model":"test","messages":[],"tools":[{"type":"function","function":{"name":"shell"}}]}`
			}
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(payload))
			r.Header.Set("Authorization", "Bearer "+secret)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			wantEvents := 1
			if test.name == "undeclared" {
				wantEvents = 2 // Authorization and the matched rule are both audited.
			}
			if w.Code != 200 || w.Body.String() != test.response || len(sink.events) != wantEvents || sink.events[0].RuleID != test.rule || sink.events[0].Action != "observe" {
				t.Fatalf("status=%d body=%s events=%+v", w.Code, w.Body.String(), sink.events)
			}
			for _, event := range sink.events {
				if event.Action != "observe" {
					t.Fatalf("observe emitted a blocking event: %+v", event)
				}
			}
		})
	}
}

func TestToolSecurityPolicyReadFailureRejectsWithoutForwarding(t *testing.T) {
	sink := &toolRecordingSink{}
	a, secret := setupToolGateway(t, sink, `{}`, "application/json")
	a.toolSecurityPolicy = func(context.Context) (admindomain.ToolSecurityPolicy, error) {
		return admindomain.ToolSecurityPolicy{}, errors.New("private database error")
	}
	a.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("forwarded with unknown policy"); return nil, nil })
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test"}`))
	r.Header.Set("Authorization", "Bearer "+secret)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestObserveLargeToolResponseIsForwarded(t *testing.T) {
	a := testApp(t)
	a.toolSecurityPolicy = nil
	payload := `{"output":[{"type":"function_call","call_id":"c","name":"shell","arguments":"{\"text\":\"` + strings.Repeat("x", (2<<20)+1) + `\"}"}]}`
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	w := httptest.NewRecorder()
	a.forwardInspectedBody(w, r, strings.NewReader(payload), http.StatusOK, "application/json")
	if w.Code != 200 || w.Body.String() != payload {
		t.Fatal("observe removed a large response")
	}
}

func TestWebSocketFormatModeKeepsConnectionAndChecksEveryRequest(t *testing.T) {
	for _, mode := range []string{"observe", "block"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			forwarded := make(chan string, 3)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				for {
					kind, payload, err := conn.Read(ctx)
					if err != nil {
						return
					}
					forwarded <- string(payload)
					if conn.Write(ctx, kind, []byte(`{"type":"response.completed","response":{"output":[]}}`)) != nil {
						return
					}
				}
			}))
			defer upstream.Close()
			sink := &toolRecordingSink{}
			a := testApp(t)
			a.background.SecurityEvents = sink
			a.client = upstream.Client()
			done := make(chan struct{})
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				r = r.WithContext(context.WithValue(r.Context(), gatewayToolSecurityModeKey{}, mode))
				r = r.WithContext(context.WithValue(r.Context(), gatewayAPIKeyRequestKey{}, apiKey{ID: 7, OrgID: 9}))
				a.bridgeWebSocket(w, r, strings.Replace(upstream.URL, "http://", "ws://", 1), nil)
			}))
			defer gateway.Close()
			conn, _, err := websocket.Dial(ctx, strings.Replace(gateway.URL, "http://", "ws://", 1), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			malformed := `{"type":"response.create","response":{"input":[{"type":"function_call","call_id":"c","name":"lookup","arguments":""}]}}`
			for index, payload := range []string{malformed, malformed, `{"type":"response.create","response":{"input":[]}}`} {
				if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
					t.Fatal(err)
				}
				_, reply, err := conn.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				want := "response.completed"
				if mode == "block" && index < 2 {
					want = "invalid_request"
				}
				if !strings.Contains(string(reply), want) {
					t.Fatalf("reply=%s", reply)
				}
			}
			_ = conn.Close(websocket.StatusNormalClosure, "done")
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("bridge did not stop")
			}
			wantForwarded := 3
			if mode == "block" {
				wantForwarded = 1
			}
			if len(forwarded) != wantForwarded || len(sink.events) != 2 {
				t.Fatalf("forwarded=%d events=%+v", len(forwarded), sink.events)
			}
			for _, event := range sink.events {
				if event.Action != mode || event.RuleID != "tool_call_invalid" {
					t.Fatalf("event=%+v", event)
				}
			}
		})
	}
}
