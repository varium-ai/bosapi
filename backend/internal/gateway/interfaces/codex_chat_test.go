package interfaces

import (
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/subscription"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Follow the sequence observed from Codex, including metadata emitted after
// the first text delta. Empty terminal output exercises output_item.done replay.
const codexMetadataStream = `event: response.created
data: {"type":"response.created","response":{"id":"resp_metadata","output":[]}}

event: response.in_progress
data: {"type":"response.in_progress"}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","phase":"final_answer","content":[]}}

event: response.content_part.added
data: {"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"OK"}

event: response.metadata
data: {"type":"response.metadata","metadata":{"moderation":{"fixture_marker":"internal-metadata"}}}

event: response.output_text.done
data: {"type":"response.output_text.done","output_index":0,"content_index":0,"text":"OK"}

event: response.content_part.done
data: {"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"OK","annotations":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"OK","annotations":[]}]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_metadata","status":"completed","output":[],"usage":{"input_tokens":15,"output_tokens":5,"input_tokens_details":{"cached_tokens":0}}}}

`

func TestCodexChatSystemAndMetadataGateway(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[streaming], func(t *testing.T) {
			a, key := geminiTestApp(t)
			a.data.Providers[0] = provider{ID: 1, Type: "oauth", Platform: "openai", APIFormat: "openai_responses", Status: "active", AccessToken: "token", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour), Models: []string{"actual"}, ModelMapping: map[string]string{"alias": "actual"}}
			called := false
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				var root struct {
					Instructions string `json:"instructions"`
					Input        []struct {
						Role string `json:"role"`
					} `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&root); err != nil {
					t.Fatal(err)
				}
				if root.Instructions != "Reply briefly." || len(root.Input) != 1 || root.Input[0].Role != "user" {
					t.Fatalf("system was not promoted exactly once: %+v", root)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(codexMetadataStream)})}, nil
			})}
			request := `{"model":"alias","messages":[{"role":"system","content":"Reply briefly."},{"role":"user","content":"Reply OK"}],"stream":false}`
			if streaming {
				request = strings.Replace(request, `"stream":false`, `"stream":true,"stream_options":{"include_usage":true}`, 1)
			}
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(request))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			body := w.Body.String()
			if !called || w.Code != 200 || !strings.Contains(body, `"content":"OK"`) || !strings.Contains(body, `"total_tokens":20`) || strings.Contains(body, "internal-metadata") || strings.Contains(body, "unsupported_upstream_feature") {
				t.Fatalf("response: %d %s", w.Code, body)
			}
			if streaming && !strings.Contains(body, "[DONE]") {
				t.Fatalf("unfinished stream: %s", body)
			}
			select {
			case event := <-a.usageQueue:
				if event.Metrics.TotalTokens != 20 || event.Model != "alias" {
					t.Fatalf("usage: %+v", event)
				}
			default:
				t.Fatal("missing usage")
			}
		})
	}
}

func TestCodexChatMetadataDoesNotCompleteOrHideUnknownEvents(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, kind := range []string{"response.metadata", "response.unknown_future_event"} {
			t.Run(kind+map[bool]string{false: "/json", true: "/stream"}[streaming], func(t *testing.T) {
				a := testApp(t)
				stream := "data: {\"type\":\"" + kind + "\"}\n\n"
				if kind != "response.metadata" {
					stream += codexMetadataStream
				}
				w := httptest.NewRecorder()
				r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
				a.convertCodexChat(w, r, strings.NewReader(stream), gatewayinfra.RelayRequest{Stream: streaming}, "model")
				if !strings.Contains(w.Body.String(), "unsupported_upstream_feature") || strings.Contains(w.Body.String(), "[DONE]") || (!streaming && w.Code != 502) {
					t.Fatalf("response: %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

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
				if version := subscription.CodexClientVersion(); r.Header.Get("Version") != version || !strings.HasPrefix(r.Header.Get("User-Agent"), r.Header.Get("Originator")+"/"+version+" ") {
					t.Fatal("inference did not use the shared Codex client version")
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
			if count := strings.Count(body, `"id":"new"`); count != 1 {
				t.Fatalf("tool call emitted %d times, want exactly once: %s", count, body)
			}
		})
	}
}

func TestCodexChatRejectsUnsupportedAndTruncated(t *testing.T) {
	a := testApp(t)
	p := provider{Type: "oauth", Platform: "openai"}
	for _, field := range []string{`"temperature":0.5,`, `"logprobs":true,`} {
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

func TestCodexReplyToolCallOrderAndValidation(t *testing.T) {
	call := func(id string) any {
		return map[string]any{"type": "function_call", "call_id": id, "name": "read", "arguments": `{"n":9007199254740993}`}
	}
	output := []any{
		call("first"),
		map[string]any{"type": "reasoning", "id": "reasoning", "summary": []any{}},
		map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "next"}}},
		call("second"),
	}
	for _, status := range []string{"completed", "incomplete"} {
		t.Run(status, func(t *testing.T) {
			root := map[string]any{"status": status, "output": output}
			wantStop := "tool_calls"
			if status == "incomplete" {
				root["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
				wantStop = "length"
			}
			reply, err := codexReply(root, "reply")
			if err != nil {
				t.Fatal(err)
			}
			if reply.Stop != wantStop || len(reply.Blocks) != 4 {
				t.Fatalf("unexpected stop or duplicated blocks: %+v", reply)
			}
			for i, kind := range []string{"tool_use", "codex_reasoning", "text", "tool_use"} {
				if reply.Blocks[i].Type != kind {
					t.Fatalf("output order changed: %+v", reply.Blocks)
				}
			}
			if reply.Blocks[0].ID != "first" || reply.Blocks[3].ID != "second" || reply.Blocks[0].Input["n"] != json.Number("9007199254740993") {
				t.Fatalf("tool identity or arguments changed: %+v", reply.Blocks)
			}
		})
	}
	for _, count := range []int{2, 129} {
		t.Run(fmt.Sprintf("reject_%d_calls", count), func(t *testing.T) {
			calls := make([]any, count)
			for i := range calls {
				id := "duplicate"
				if count > 128 {
					id = fmt.Sprintf("call_%d", i)
				}
				calls[i] = call(id)
			}
			if _, err := codexReply(map[string]any{"status": "completed", "output": calls}, "reply"); err == nil {
				t.Fatal("accepted duplicate IDs or excessive tool calls")
			}
		})
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
