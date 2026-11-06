package interfaces

import (
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const claudeCodeHistory = `{"model":"claude-test","stream":true,"max_tokens":4096,"system":[{"type":"text","text":"client attribution"},{"type":"text","text":"system instructions","cache_control":{"type":"ephemeral","ttl":"1h"}}],"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"cache_control":{"type":"ephemeral"},"tools":[{"name":"Read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"thinking","thinking":"previous reasoning","signature":"signed-history"},{"type":"redacted_thinking","data":"opaque-history"},{"type":"tool_use","id":"old-call","name":"Read","input":{"path":"old.txt","id":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"old-call","content":[{"type":"text","text":"old result"}],"is_error":false},{"type":"text","text":"continue","cache_control":{"type":"ephemeral"}}]}],"context_management":{"edits":[]},"future_field":{"enabled":true}}`

func claudeCodeApp(t *testing.T, upstream string) (*app, string) {
	t.Helper()
	a := testApp(t)
	key := "sk-gw-" + random(32)
	a.data.Providers = []provider{{ID: 1, Type: "api_key", Platform: "anthropic", APIFormat: "anthropic_messages", Status: "active", BaseURL: upstream, APIKey: "upstream-provider-key", Models: []string{"claude-test"}}}
	a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}
	a.background.SecurityEvents = gatewayinfra.RuleSecurityEventSink{}
	a.usageQueue = make(chan usageEvent, 8)
	a.client = &http.Client{Timeout: 2 * time.Second}
	return a, key
}

func TestClaudeCodeToolHistoryAndSSE(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pingRead, toolRead := make(chan struct{}), make(chan struct{})
	failures := make(chan string, 4)
	var requests atomic.Int32
	nextBody := strings.NewReplacer("old-call", "new-call", "old.txt", "next.txt", "signed-history", "signed-output", "previous reasoning", "new reasoning", "old result", "new result", `"stream":true`, `"stream":false`).Replace(claudeCodeHistory)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if requests.Add(1) == 2 {
			if string(raw) != nextBody {
				failures <- "returned tool result or thinking signature changed in next turn"
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"msg_2","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":25,"output_tokens":2}}`)
			return
		}
		if string(raw) != claudeCodeHistory {
			failures <- "request fields or tool history changed"
		}
		if r.URL.String() != "/v1/messages?beta=true" || r.Header.Get("x-api-key") != "upstream-provider-key" || r.Header.Get("Authorization") != "" {
			failures <- "wrong endpoint or credential"
		}
		if r.Header.Get("anthropic-beta") != "extended-cache-ttl,future-capability" || r.Header.Get("anthropic-workspace-id") != "workspace-test" {
			failures <- "capability headers lost"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Request-Id", "upstream-request")
		w.Header().Set("Anthropic-Ratelimit-Unified-Status", "allowed")
		send := func(event, data string) {
			io.WriteString(w, "event: "+event+"\ndata: "+data+"\n\n")
			w.(http.Flusher).Flush()
		}
		send("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude-test","content":[],"usage":{"input_tokens":12,"cache_read_input_tokens":5,"cache_creation_input_tokens":3,"output_tokens":0}}}`)
		send("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`)
		send("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"new reasoning"}}`)
		send("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed-output"}}`)
		send("content_block_stop", `{"type":"content_block_stop","index":0}`)
		send("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"new-call","name":"Read","input":{}}}`)
		send("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`)
		send("ping", `{"type":"ping"}`)
		select {
		case <-pingRead:
		case <-ctx.Done():
			return
		}
		send("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"next.txt\"}"}}`)
		send("content_block_stop", `{"type":"content_block_stop","index":1}`)
		select {
		case <-toolRead:
		case <-ctx.Done():
			return
		}
		send("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`)
		send("message_stop", `{"type":"message_stop"}`)
	}))
	defer upstream.Close()
	a, key := claudeCodeApp(t, upstream.URL)
	gateway := httptest.NewServer(a.routes())
	defer gateway.Close()
	req, _ := http.NewRequestWithContext(ctx, "POST", gateway.URL+"/v1/messages?beta=true", strings.NewReader(claudeCodeHistory))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Add("anthropic-beta", "extended-cache-ttl")
	req.Header.Add("anthropic-beta", "future-capability")
	req.Header.Set("anthropic-workspace-id", "workspace-test")
	res, err := gateway.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Request-Id") != "upstream-request" || res.Header.Get("Anthropic-Ratelimit-Unified-Status") != "allowed" || res.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("response: %d %v", res.StatusCode, res.Header)
	}
	scanner := bufio.NewScanner(res.Body)
	var events []map[string]any
	pingSeen, toolSeen := false, false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var value map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &value) != nil {
			t.Fatal(line)
		}
		events = append(events, value)
		if value["type"] == "ping" && !pingSeen {
			pingSeen = true
			close(pingRead)
		}
		if value["type"] == "content_block_stop" && value["index"] == float64(1) && !toolSeen {
			toolSeen = true
			close(toolRead)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !pingSeen || !toolSeen || len(events) != 12 || events[len(events)-1]["type"] != "message_stop" {
		t.Fatalf("incomplete stream: %+v", events)
	}
	serialized, _ := json.Marshal(events)
	for _, text := range []string{"thinking_delta", "signature_delta", "signed-output", "new-call", "next.txt", "tool_use"} {
		if !strings.Contains(string(serialized), text) {
			t.Fatalf("lost %s: %s", text, serialized)
		}
	}
	select {
	case failure := <-failures:
		t.Fatal(failure)
	default:
	}
	select {
	case event := <-a.usageQueue:
		if !event.IsStream || event.Metrics.TotalTokens != 27 {
			t.Fatalf("usage: %+v", event)
		}
	case <-ctx.Done():
		t.Fatal("missing usage")
	}
	// Replay the received tool invocation, result and signed thinking in the next
	// actual HTTP turn, with the same capability headers.
	next := req.Clone(ctx)
	next.Body = io.NopCloser(strings.NewReader(nextBody))
	next.ContentLength = int64(len(nextBody))
	nextRes, err := gateway.Client().Do(next)
	if err != nil {
		t.Fatal(err)
	}
	nextRaw, err := io.ReadAll(nextRes.Body)
	nextRes.Body.Close()
	if err != nil || nextRes.StatusCode != 200 || !strings.Contains(string(nextRaw), "done") || requests.Load() != 2 {
		t.Fatalf("tool continuation: %d %s %v", nextRes.StatusCode, nextRaw, err)
	}
	select {
	case failure := <-failures:
		t.Fatal(failure)
	default:
	}

}

func TestClaudeCodeNativeCountTokens(t *testing.T) {
	a, key := claudeCodeApp(t, "https://upstream.example/v1")
	body := strings.Replace(claudeCodeHistory, `"stream":true,`, "", 1)
	a.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/messages/count_tokens" || string(raw) != body {
			t.Fatalf("count request changed: %s %s", r.URL, raw)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"input_tokens":12345}`))}, nil
	})
	r := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(body))
	r.Header.Set("x-api-key", key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != `{"input_tokens":12345}` {
		t.Fatalf("native count: %d %s", w.Code, w.Body.String())
	}
}

func TestClaudeCodeCapabilityErrors(t *testing.T) {
	for _, status := range []int{400, 429, 529} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			a, key := claudeCodeApp(t, "https://upstream.example")
			raw := `{"type":"error","error":{"type":"invalid_request_error","message":"thinking: adaptive is not supported by this model"},"request_id":"req_test"}`
			a.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "X-Should-Retry": {"false"}, "Retry-After": {"3"}}, Body: io.NopCloser(strings.NewReader(raw))}, nil
			})
			r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(claudeCodeHistory))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != status || w.Body.String() != raw || w.Header().Get("X-Should-Retry") != "false" || w.Header().Get("Retry-After") != "3" {
				t.Fatalf("error changed: %d %s %v", w.Code, w.Body.String(), w.Header())
			}
		})
	}
}

func TestClaudeCodeStreamOutlivesClientTotalTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-time.After(200 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()
	a, key := claudeCodeApp(t, upstream.URL)
	a.client.Timeout = 100 * time.Millisecond
	gateway := httptest.NewServer(a.routes())
	defer gateway.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", gateway.URL+"/v1/messages", strings.NewReader(claudeCodeHistory))
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := gateway.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != 200 || !strings.Contains(string(raw), "message_stop") {
		t.Fatalf("long stream truncated: %d %s %v", res.StatusCode, raw, err)
	}
	if a.client.Timeout != 100*time.Millisecond {
		t.Fatal("shared client timeout was mutated")
	}
}

func TestClaudeCodeHeaderTimeoutAndClientCancellation(t *testing.T) {
	for _, scenario := range []string{"headers", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			cancelled := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				if scenario == "cancel" {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
				close(cancelled)
			}))
			defer upstream.Close()
			a, key := claudeCodeApp(t, upstream.URL)
			a.client.Timeout = 100 * time.Millisecond
			gateway := httptest.NewServer(a.routes())
			defer gateway.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "POST", gateway.URL+"/v1/messages", strings.NewReader(claudeCodeHistory))
			req.Header.Set("Authorization", "Bearer "+key)
			res, err := gateway.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if scenario == "headers" {
				if res.StatusCode != 504 {
					t.Fatalf("header timeout: %d", res.StatusCode)
				}
			} else {
				buf := make([]byte, 1)
				if _, err := res.Body.Read(buf); err != nil {
					t.Fatal(err)
				}
				cancel()
			}
			select {
			case <-cancelled:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream was not cancelled")
			}
		})
	}
}

func TestClaudeCodeErrorsDoNotEchoCredentials(t *testing.T) {
	a, key := claudeCodeApp(t, "https://upstream.example")
	a.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw := `{"type":"error","error":{"type":"invalid_request_error","message":"unsupported thinking; upstream-provider-key; ` + key + `"}}`
		return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw))}, nil
	})
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(claudeCodeHistory))
	req.Header.Set("x-api-key", key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	if w.Code != 400 || !json.Valid(w.Body.Bytes()) || strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), "upstream-provider-key") || !strings.Contains(w.Body.String(), "unsupported thinking") {
		t.Fatalf("error redaction: %d %s", w.Code, w.Body.String())
	}
}
