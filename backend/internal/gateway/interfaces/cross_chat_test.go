package interfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const crossOpenAIResponse = `{"id":"chat_1","choices":[{"index":0,"message":{"role":"assistant","content":"hello","tool_calls":[{"id":"call_new","type":"function","function":{"name":"read","arguments":"{\"n\":9007199254740993}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":17,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4}}}`

func chatTestStream() string {
	frames := []string{
		`{"id":"chat_1","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_new","type":"function","function":{"name":"re","arguments":"{\"n\":"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"ad","arguments":"9007199254740993}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":17,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4}}}`,
		`[DONE]`,
	}
	return "data: " + strings.Join(frames, "\n\ndata: ") + "\n\n"
}

func TestMessagesChatJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, blocked := range []bool{false, true} {
			t.Run(testBoolName(stream, blocked), func(t *testing.T) {
				a, key := crossTestApp(t, "openai_chat")
				a.data.Providers[0].BaseURL = "https://glm.example/api/paas/v4"
				sink := &toolRecordingSink{block: blocked}
				a.background.SecurityEvents = sink
				body := strings.Replace(crossMessagesRequest, `"model":`, `"thinking":{"type":"enabled","budget_tokens":0},"reasoning_effort":"provider-defined","enable_thinking":false,"output_config":{"effort":"high"},"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"model":`, 1)
				response, kind := crossOpenAIResponse, "application/json"
				if stream {
					body = strings.Replace(body, `"model":"alias"`, `"model":"alias","stream":true`, 1)
					response, kind = chatTestStream(), "text/event-stream"
				}
				a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					raw, _ := io.ReadAll(r.Body)
					if r.URL.String() != "https://glm.example/api/paas/v4/chat/completions" || r.Header.Get("Authorization") != "Bearer provider-key" || r.Header.Get("x-api-key") != "" || r.Header.Get("anthropic-beta") != "" {
						t.Fatalf("credentials or endpoint: %s %v", r.URL, r.Header)
					}
					if !strings.Contains(string(raw), `"model":"actual"`) || !strings.Contains(string(raw), `"role":"system"`) || !strings.Contains(string(raw), `"type":"function"`) {
						t.Fatalf("request conversion: %s", raw)
					}
					var upstream map[string]any
					if err := json.Unmarshal(raw, &upstream); err != nil {
						t.Fatal(err)
					}
					thinking, ok := upstream["thinking"].(map[string]any)
					if !ok || thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(0) || upstream["reasoning_effort"] != "provider-defined" || upstream["enable_thinking"] != false {
						t.Fatalf("caller thinking controls changed: %s", raw)
					}
					for _, hint := range []string{"output_config", "context_management"} {
						if _, exists := upstream[hint]; exists {
							t.Fatalf("Claude client hint leaked upstream: %s", raw)
						}
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(response)})}, nil
				})}
				r := httptest.NewRequest("POST", "/v1/messages?beta=true&key=client-secret", strings.NewReader(body))
				r.Header.Set("x-api-key", key)
				r.Header.Set("anthropic-version", "2023-06-01")
				r.Header.Set("anthropic-beta", "claude-code-20250219,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-tool-changes-2026-07-01,effort-2025-11-24,afk-mode-2026-01-31")
				w := httptest.NewRecorder()
				a.routes().ServeHTTP(w, r)
				if w.Code != 200 || !strings.Contains(w.Body.String(), "hello") || strings.Contains(w.Body.String(), "unsupported_upstream_feature") || !strings.Contains(w.Body.String(), `"model":"alias"`) {
					t.Fatalf("response: %d %s", w.Code, w.Body.String())
				}
				if blocked == strings.Contains(w.Body.String(), "9007199254740993") || len(sink.calls) != 1 || sink.calls[0].Name != "read" {
					t.Fatalf("tool security bypassed: calls=%+v body=%s", sink.calls, w.Body.String())
				}
				if stream && (!w.Flushed || !strings.Contains(w.Body.String(), "event: message_stop") || strings.Contains(w.Body.String(), "data: [DONE]")) {
					t.Fatalf("invalid Messages stream: %s", w.Body.String())
				}
				select {
				case event := <-a.usageQueue:
					if event.Model != "alias" || event.Metrics.TotalTokens != 24 || event.Metrics.CacheReadTokens != 4 || math.Abs(event.Metrics.Cost-56.0/1_000_000) > 1e-12 || event.IsStream != stream {
						t.Fatalf("wrong billing: %+v", event)
					}
				default:
					t.Fatal("missing usage")
				}
			})
		}
	}
}

func testBoolName(stream, blocked bool) string {
	name := "json"
	if stream {
		name = "stream"
	}
	if blocked {
		name += "_blocked"
	}
	return name
}

func TestMessagesChatMultiTurnToolHistory(t *testing.T) {
	a, key := crossTestApp(t, "openai_chat")
	requests := 0
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		raw, _ := io.ReadAll(r.Body)
		if requests == 2 && (!strings.Contains(string(raw), `"tool_call_id":"call_new"`) || !strings.Contains(string(raw), `"role":"tool"`) || !strings.Contains(string(raw), "9007199254740993")) {
			t.Fatalf("tool history lost: %s", raw)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(crossOpenAIResponse))}, nil
	})}
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("turn failed: %d %s", w.Code, w.Body.String())
		}
		return w
	}
	first := call(crossMessagesRequest)
	var reply map[string]any
	decoder := json.NewDecoder(first.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&reply); err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal([]byte(crossMessagesRequest), &request); err != nil {
		t.Fatal(err)
	}
	request["messages"] = append(request["messages"].([]any), map[string]any{"role": "assistant", "content": reply["content"]}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_new", "content": "file content"}, map[string]any{"type": "text", "text": "continue"}}})
	payload, _ := json.Marshal(request)
	call(string(payload))
	if requests != 2 {
		t.Fatalf("unexpected upstream requests: %d", requests)
	}
}

func TestMessagesChatStreamFailureBoundaries(t *testing.T) {
	cases := []struct{ name, response string }{
		{"truncated", strings.Replace(chatTestStream(), "data: [DONE]\n\n", "", 1)},
		{"missing finish", strings.Replace(chatTestStream(), `"finish_reason":"tool_calls"`, `"finish_reason":null`, 1)},
		{"missing usage", strings.Replace(chatTestStream(), `"usage":{"prompt_tokens":17,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4}}`, `"usage":null`, 1)},
		{"invalid arguments", strings.Replace(chatTestStream(), `9007199254740993}`, `9007199254740993`, 1)},
		{"invalid json", "data: {\n\n"},
		{"upstream error", "data: {\"error\":{\"message\":\"provider-key\"}}\n\n"},
		{"reasoning type", strings.Replace(chatTestStream(), `"content":"hello"`, `"content":"hello","reasoning_content":{}`, 1)},
		{"unknown finish", strings.Replace(chatTestStream(), `"finish_reason":"tool_calls"`, `"finish_reason":"future"`, 1)},
		{"malformed usage", strings.Replace(chatTestStream(), `"prompt_tokens":17`, `"prompt_tokens":-1`, 1)},
		{"missing choices", "data: {}\n\n"},
		{"wrong choice", strings.Replace(chatTestStream(), `"index":0`, `"index":1`, 1)},
		{"missing delta", strings.Replace(chatTestStream(), `"delta":{"role":"assistant","content":"hello"}`, `"delta":null`, 1)},
		{"wrong content", strings.Replace(chatTestStream(), `"content":"hello"`, `"content":3`, 1)},
		{"wrong calls", strings.Replace(chatTestStream(), `"tool_calls":[`, `"tool_calls":false,"future":[`, 1)},
		{"wrong function", strings.Replace(chatTestStream(), `"function":{"name":"re","arguments":"{\"n\":"}`, `"function":false`, 1)},
		{"unknown function field", strings.Replace(chatTestStream(), `"name":"re"`, `"name":"re","extra":true`, 1)},
		{"wrong function name", strings.Replace(chatTestStream(), `"name":"re"`, `"name":1`, 1)},
		{"wrong function type", strings.Replace(chatTestStream(), `"type":"function"`, `"type":"custom"`, 1)},
		{"missing tool index", strings.Replace(chatTestStream(), `"index":0,"id":"call_new"`, `"id":"call_new"`, 1)},
		{"bad tool index", strings.Replace(chatTestStream(), `"index":0,"id":"call_new"`, `"index":128,"id":"call_new"`, 1)},
		{"unknown tool field", strings.Replace(chatTestStream(), `"id":"call_new"`, `"id":"call_new","extra":true`, 1)},
		{"content after finish", strings.Replace(chatTestStream(), `"choices":[],"usage"`, `"choices":[{"index":0,"delta":{"content":"late"}}],"usage"`, 1)},
		{"non string finish", strings.Replace(chatTestStream(), `"finish_reason":"tool_calls"`, `"finish_reason":true`, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, key := crossTestApp(t, "openai_chat")
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.response))}, nil
			})}
			r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.Replace(crossMessagesRequest, `"model":"alias"`, `"model":"alias","stream":true`, 1)))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if !strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "event: message_stop") || strings.Contains(w.Body.String(), "provider-key") || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("stream failed open: %s", w.Body.String())
			}
			select {
			case event := <-a.usageQueue:
				if event.Status != 502 {
					t.Fatalf("failure not audited: %+v", event)
				}
			default:
				t.Fatal("missing failure audit")
			}
		})
	}
}

func TestMessagesChatErrorsAndUnsupportedProviders(t *testing.T) {
	for _, status := range []int{400, 401, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			a, key := crossTestApp(t, "openai_chat")
			called := false
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), `"thinking":{"budget_tokens":0,"type":"enabled"}`) {
					t.Fatalf("thinking did not reach upstream: %s", raw)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"2"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"provider-key"}}`))}, nil
			})}
			body := strings.Replace(crossMessagesRequest, `"model":`, `"thinking":{"type":"enabled","budget_tokens":0},"model":`, 1)
			r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
			r.Header.Set("x-api-key", key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if !called || w.Code < 400 || !strings.Contains(w.Body.String(), `"type":"error"`) || strings.Contains(w.Body.String(), "provider-key") || w.Header().Get("Retry-After") != "2" {
				t.Fatalf("invalid error: %d %s %v", w.Code, w.Body.String(), w.Header())
			}
		})
	}
	for _, format := range []string{"openai_responses", "custom", "realtime"} {
		a, key := crossTestApp(t, format)
		a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("unsupported provider called")
			return nil, nil
		})}
		r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(crossMessagesRequest))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_conversion") {
			t.Fatalf("wrong format accepted: %s %s", format, w.Body.String())
		}
	}
}

func TestMessagesChatCanceledRequest(t *testing.T) {
	a, key := crossTestApp(t, "openai_chat")
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(crossMessagesRequest)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code < 400 || !strings.Contains(w.Body.String(), `"type":"error"`) {
		t.Fatalf("cancellation not propagated: %d %s", w.Code, w.Body.String())
	}
}

func TestMessagesChatStreamBoundsOnlyHeaderTimeout(t *testing.T) {
	a, key := crossTestApp(t, "openai_chat")
	a.client = &http.Client{Timeout: time.Minute, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if _, hasDeadline := r.Context().Deadline(); hasDeadline {
			t.Fatal("stream body retains whole-response deadline")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(chatTestStream()))}, nil
	})}
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.Replace(crossMessagesRequest, `"model":"alias"`, `"model":"alias","stream":true`, 1)))
	r.Header.Set("x-api-key", key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "event: message_stop") {
		t.Fatalf("stream failed: %s", w.Body.String())
	}
}

func TestMessagesChatTextFlushesBeforeUpstreamFinishes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	seen := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n"); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		select {
		case <-seen:
		case <-ctx.Done():
			return
		case <-r.Context().Done():
			return
		}
		if _, err := io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"); err != nil {
			return
		}
	}))
	defer upstream.Close()
	a, key := crossTestApp(t, "openai_chat")
	a.data.Providers[0].BaseURL = upstream.URL
	a.client = upstream.Client()
	gateway := httptest.NewServer(a.routes())
	defer gateway.Close()
	body := strings.Replace(crossMessagesRequest, `"model":"alias"`, `"model":"alias","stream":true`, 1)
	req, err := http.NewRequestWithContext(ctx, "POST", gateway.URL+"/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-key", key)
	res, err := gateway.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	scanner := bufio.NewScanner(res.Body)
	observed, done := false, false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, `"text":"hello"`) && !observed {
			observed = true
			close(seen)
		}
		if line == "event: message_stop" {
			done = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !observed || !done {
		t.Fatal("text was buffered until the upstream completed")
	}
}

func TestMessagesChatParallelToolDeltas(t *testing.T) {
	a, key := crossTestApp(t, "openai_chat")
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		frames := []string{
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"second","type":"function","function":{"name":"read","arguments":"{\"n\":"}},{"index":0,"id":"first","type":"function","function":{"name":"read","arguments":"{\"n\":"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}},{"index":1,"function":{"arguments":"2}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`,
			`[DONE]`,
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + strings.Join(frames, "\n\ndata: ") + "\n\n"))}, nil
	})}
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.Replace(crossMessagesRequest, `"model":"alias"`, `"model":"alias","stream":true`, 1)))
	r.Header.Set("x-api-key", key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	first, second := strings.Index(w.Body.String(), `"id":"first"`), strings.Index(w.Body.String(), `"id":"second"`)
	if first < 0 || second < first || !strings.Contains(w.Body.String(), "event: message_stop") || strings.Contains(w.Body.String(), "unsupported_") {
		t.Fatalf("parallel invocations corrupted: %s", w.Body.String())
	}
}

func TestMessagesChatBetaHintsAreIgnored(t *testing.T) {
	for _, format := range []string{"openai_chat", ""} {
		for _, stream := range []bool{false, true} {
			for _, tc := range []struct {
				name   string
				values []string
			}{
				{"known", []string{"claude-code-20250219, prompt-caching-2024-07-31"}},
				{"unknown", []string{"future-capability"}},
				{"mixed", []string{"interleaved-thinking-2025-05-14,future-capability"}},
				{"multiple headers", []string{"claude-code-20250219", "future-capability,another-capability"}},
				{"empty", []string{""}},
			} {
				t.Run(format+"/"+testBoolName(stream, false)+"/"+tc.name, func(t *testing.T) {
					a, key := crossTestApp(t, format)
					body := crossMessagesRequest
					response, kind := crossOpenAIResponse, "application/json"
					if stream {
						body = strings.Replace(body, `"model":"alias"`, `"model":"alias","stream":true`, 1)
						response, kind = chatTestStream(), "text/event-stream"
					}
					called := false
					a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						called = true
						if values := r.Header.Values("anthropic-beta"); len(values) != 0 {
							t.Fatalf("beta hints forwarded to Chat provider: %v", values)
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(response))}, nil
					})}
					r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
					r.Header.Set("Authorization", "Bearer "+key)
					for _, value := range tc.values {
						r.Header.Add("anthropic-beta", value)
					}
					w := httptest.NewRecorder()
					a.routes().ServeHTTP(w, r)
					if !called || w.Code != 200 || !strings.Contains(w.Body.String(), "hello") {
						t.Fatalf("beta hints rejected: %d %s", w.Code, w.Body.String())
					}
					if stream && !strings.Contains(w.Body.String(), "event: message_stop") {
						t.Fatalf("incomplete stream: %s", w.Body.String())
					}
				})
			}
		}
	}
}

func TestMessagesChatLegacyClaudeThinkingBeta(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(testBoolName(stream, false), func(t *testing.T) {
			a, key := crossTestApp(t, "openai_chat")
			body := strings.Replace(crossMessagesRequest, `"model":`, `"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"model":`, 1)
			response, kind := crossOpenAIResponse, "application/json"
			if stream {
				body = strings.Replace(body, `"model":`, `"stream":true,"model":`, 1)
				response, kind = chatTestStream(), "text/event-stream"
			}
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("anthropic-beta") != "" {
					t.Fatal("legacy Claude beta was forwarded to a Chat provider")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			r := httptest.NewRequest("POST", "/v1/messages?beta=true", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("anthropic-version", "2023-06-01")
			r.Header.Set("anthropic-beta", "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05,effort-2025-11-24,afk-mode-2026-01-31")
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "hello") || strings.Contains(w.Body.String(), "unsupported_") {
				t.Fatalf("legacy Claude request rejected: %d %s", w.Code, w.Body.String())
			}
			if stream && !strings.Contains(w.Body.String(), "event: message_stop") {
				t.Fatalf("incomplete legacy Claude stream: %s", w.Body.String())
			}
		})
	}
}

func TestMessagesChatBetaDoesNotEnableUnsupportedCapabilities(t *testing.T) {
	for _, tc := range []struct{ name, hint, beta, field string }{
		{"structured output without schema", `"output_config":{"format":{"type":"json_schema"}}`, "effort-2025-11-24", "output_config.format.schema"},
		{"history deletion", `"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":0}]}`, "context-management-2025-06-27", "context_management.edits[0]."},
		{"safeguards", `"safeguards":[{"type":"future"}]`, "claude-code-20250219", "safeguards"},
		{"unknown beta with structured output without schema", `"output_config":{"format":{"type":"json_schema"}}`, "interleaved-thinking-2025-05-14,future-capability", "output_config.format.schema"},
		{"unknown beta with safeguards", `"safeguards":[{"type":"future"}]`, "future-capability", "safeguards"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, key := crossTestApp(t, "openai_chat")
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("unsupported capability reached upstream")
				return nil, nil
			})}
			body := `{"model":"alias","messages":[{"role":"user","content":"hello"}],` + tc.hint + `}`
			r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("anthropic-beta", tc.beta)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 400 || !strings.Contains(w.Body.String(), `"param":"`+tc.field+`"`) {
				t.Fatalf("unsupported capability accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
