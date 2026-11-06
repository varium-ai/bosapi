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

const crossChatRequest = `{"model":"alias","max_tokens":512,"messages":[{"role":"system","content":"instructions"},{"role":"user","content":"read a file"}],"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}]}`
const crossMessagesRequest = `{"model":"alias","max_tokens":512,"system":"instructions","messages":[{"role":"user","content":"read a file"}],"tools":[{"name":"read","input_schema":{"type":"object"}}]}`
const crossClaudeResponse = `{"id":"msg_1","type":"message","model":"actual","content":[{"type":"text","text":"hello"},{"type":"tool_use","id":"call_new","name":"read","input":{"n":9007199254740993}}],"stop_reason":"tool_use","usage":{"input_tokens":11,"cache_read_input_tokens":4,"cache_creation_input_tokens":2,"output_tokens":7}}`
const crossGeminiResponse = `{"responseId":"gem_1","candidates":[{"content":{"parts":[{"text":"hello"},{"functionCall":{"id":"call_new","name":"read","args":{"n":9007199254740993}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":17,"cachedContentTokenCount":4,"candidatesTokenCount":5,"thoughtsTokenCount":2}}`
const crossClaudeStream = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"content\":[],\"usage\":{\"input_tokens\":11,\"cache_read_input_tokens\":4,\"cache_creation_input_tokens\":2,\"output_tokens\":0}}}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_new\",\"name\":\"read\",\"input\":{}}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"n\\\":\"}}\n\n" +
	"event: ping\ndata: {\"type\":\"ping\"}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"9007199254740993}\"}}\n\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":7}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

type byteFragmentReader struct{ io.Reader }

func (reader byteFragmentReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 3 {
		buffer = buffer[:3]
	}
	return reader.Reader.Read(buffer)
}

func crossTestApp(t *testing.T, target string) (*app, string) {
	t.Helper()
	a, key := geminiTestApp(t)
	a.data.Providers[0].APIFormat = target
	a.data.Providers[0].Pricing = map[string]any{"models": map[string]any{"actual": map[string]any{"input": 2.0, "output": 4.0, "cache_read": 0.5, "cache_write": 3.0}}}
	return a, key
}

func TestCrossProtocolJSONAndSSEPairs(t *testing.T) {
	for _, pair := range []struct{ source, target string }{{"chat", "anthropic_messages"}, {"chat", "gemini"}, {"messages", "gemini"}} {
		for _, stream := range []bool{false, true} {
			name := pair.source + "_" + pair.target
			if stream {
				name += "_stream"
			}
			t.Run(name, func(t *testing.T) {
				a, key := crossTestApp(t, pair.target)
				request := crossChatRequest
				path := "/v1/chat/completions"
				response := crossClaudeResponse
				contentType := "application/json"
				if pair.source == "messages" {
					request = crossMessagesRequest
					path = "/v1/messages"
				}
				if pair.target == "gemini" {
					response = crossGeminiResponse
				}
				if stream {
					request = strings.Replace(request, `"model":"alias"`, `"model":"alias","stream":true`, 1)
					if pair.source == "chat" {
						request = strings.Replace(request, `"stream":true`, `"stream":true,"stream_options":{"include_usage":true}`, 1)
					}
					contentType = "text/event-stream"
					response = crossClaudeStream
					if pair.target == "gemini" {
						response = "data: " + crossGeminiResponse + "\n\n"
					}
				}
				a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					raw, _ := io.ReadAll(r.Body)
					if pair.target == "anthropic_messages" {
						if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "provider-key" || !strings.Contains(string(raw), `"model":"actual"`) {
							t.Fatalf("Claude request: %s %s %v", r.URL, raw, r.Header)
						}
					} else {
						operation := "generateContent"
						if stream {
							operation = "streamGenerateContent"
						}
						if r.URL.Path != "/v1beta/models/actual:"+operation || r.Header.Get("x-goog-api-key") != "provider-key" {
							t.Fatalf("Gemini request: %s %v", r.URL, r.Header)
						}
					}
					if !strings.Contains(string(raw), "instructions") || !strings.Contains(string(raw), "read") {
						t.Fatalf("request content lost: %s", raw)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(response)})}, nil
				})}
				r := httptest.NewRequest("POST", path, strings.NewReader(request))
				r.Header.Set("Authorization", "Bearer "+key)
				w := httptest.NewRecorder()
				a.routes().ServeHTTP(w, r)
				if w.Code != 200 || !strings.Contains(w.Body.String(), "hello") || !strings.Contains(w.Body.String(), "call_new") || !strings.Contains(w.Body.String(), "9007199254740993") {
					t.Fatalf("response conversion: %d %s", w.Code, w.Body.String())
				}
				if strings.Contains(w.Body.String(), "unauthorized_tool") || strings.Contains(w.Body.String(), "unsupported_upstream_feature") {
					t.Fatalf("converted tool blocked: %s", w.Body.String())
				}
				if stream {
					if !w.Flushed {
						t.Fatal("SSE not flushed")
					}
					if pair.source == "chat" && !strings.Contains(w.Body.String(), "data: [DONE]") || pair.source == "messages" && !strings.Contains(w.Body.String(), "event: message_stop") {
						t.Fatalf("terminal event lost: %s", w.Body.String())
					}
				}
				select {
				case event := <-a.usageQueue:
					want := 56.0 / 1_000_000
					if pair.target == "anthropic_messages" {
						want = 58.0 / 1_000_000
					}
					if event.Model != "alias" || event.Metrics.TotalTokens != 24 || math.Abs(event.Metrics.Cost-want) > 1e-12 || event.IsStream != stream {
						t.Fatalf("usage/pricing: %+v", event)
					}
				default:
					t.Fatal("missing usage")
				}
			})
		}
	}
}

func TestCrossProtocolUnsupportedRequestsNeverReachUpstream(t *testing.T) {
	for _, test := range []struct{ source, target, extra, field string }{{"chat", "gemini", `,"response_format":{"type":"json_schema"}`, "response_format"}, {"chat", "anthropic_messages", `,"logprobs":true`, "logprobs"}, {"messages", "gemini", `,"thinking":{"type":"adaptive"}`, "thinking"}, {"messages", "gemini", `,"cache_control":{"type":"ephemeral"}`, "cache_control"}} {
		t.Run(test.field, func(t *testing.T) {
			a, key := crossTestApp(t, test.target)
			body := crossChatRequest
			path := "/v1/chat/completions"
			if test.source == "messages" {
				body = crossMessagesRequest
				path = "/v1/messages"
			}
			body = strings.TrimSuffix(body, "}") + test.extra + "}"
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("unsupported request reached upstream")
				return nil, nil
			})}
			r := httptest.NewRequest("POST", path, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			var result struct{ Error struct{ Code, Param string } }
			_ = json.Unmarshal(w.Body.Bytes(), &result)
			if w.Code != 400 || result.Error.Code != "unsupported_conversion" || result.Error.Param != test.field {
				t.Fatalf("missing explicit boundary error: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCrossProtocolRejectsUnrepresentableUpstreamAndTruncatedStreams(t *testing.T) {
	for _, tc := range []struct{ target, response, field string }{
		{"gemini", "data: " + strings.Replace(crossGeminiResponse, `"functionCall":`, `"thoughtSignature":"SIGNED_PRIVATE","functionCall":`, 1) + "\n\n", "thoughtSignature"},
		{"anthropic_messages", strings.TrimSuffix(crossClaudeStream, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), "stream"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			a, key := crossTestApp(t, tc.target)
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.response))}, nil
			})}
			body := strings.Replace(crossChatRequest, `"model":"alias"`, `"model":"alias","stream":true`, 1)
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if !strings.Contains(w.Body.String(), "unsupported_upstream_feature") || !strings.Contains(w.Body.String(), tc.field) || strings.Contains(w.Body.String(), "data: [DONE]") || strings.Contains(w.Body.String(), "SIGNED_PRIVATE") {
				t.Fatalf("invalid success: %d %s", w.Code, w.Body.String())
			}
			select {
			case event := <-a.usageQueue:
				if event.Status != 502 || event.ErrorCode != "unsupported_upstream_feature" {
					t.Fatalf("stream failure audit: %+v", event)
				}
			default:
				t.Fatal("missing failure audit")
			}
		})
	}
}

func TestCrossProtocolToolAuthorizationStillApplies(t *testing.T) {
	a, key := crossTestApp(t, "gemini")
	response := strings.Replace(crossGeminiResponse, `"name":"read"`, `"name":"undeclared"`, 1)
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(crossChatRequest))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "9007199254740993") || !strings.Contains(w.Body.String(), "unauthorized_tool") || !strings.Contains(w.Body.String(), "hello") {
		t.Fatalf("tool policy bypassed: %s", w.Body.String())
	}
}

func TestCrossProtocolTransportTimeout(t *testing.T) {
	a, key := crossTestApp(t, "gemini")
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })}
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(crossChatRequest))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 504 {
		t.Fatalf("timeout: %d %s", w.Code, w.Body.String())
	}
}

func TestCrossProtocolToolRoundTrip(t *testing.T) {
	for _, pair := range []struct{ source, target string }{{"chat", "anthropic_messages"}, {"chat", "gemini"}, {"messages", "gemini"}} {
		t.Run(pair.source+"_"+pair.target, func(t *testing.T) {
			a, key := crossTestApp(t, pair.target)
			body := crossChatRequest
			path := "/v1/chat/completions"
			if pair.source == "messages" {
				body = crossMessagesRequest
				path = "/v1/messages"
			}
			requests := 0
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				raw, _ := io.ReadAll(r.Body)
				response := crossGeminiResponse
				if pair.target == "anthropic_messages" {
					response = crossClaudeResponse
					response = strings.Replace(response, `"type":"tool_use",`, `"type":"tool_use","caller":{"type":"direct"},`, 1)
				}
				if requests == 2 {
					for _, expected := range []string{"9007199254740993", "call_new", "tool output"} {
						if !strings.Contains(string(raw), expected) {
							t.Fatalf("history lost %s: %s", expected, raw)
						}
					}
					if pair.target == "gemini" && !strings.Contains(string(raw), "functionResponse") || pair.target == "anthropic_messages" && !strings.Contains(string(raw), "tool_result") {
						t.Fatalf("wrong result protocol: %s", raw)
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			handler := a.routes()
			r := httptest.NewRequest("POST", path, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("first turn %d %s", w.Code, w.Body.String())
			}
			var request, response map[string]any
			decoder := json.NewDecoder(strings.NewReader(body))
			decoder.UseNumber()
			_ = decoder.Decode(&request)
			decoder = json.NewDecoder(strings.NewReader(w.Body.String()))
			decoder.UseNumber()
			_ = decoder.Decode(&response)
			messages := request["messages"].([]any)
			if pair.source == "chat" {
				message := response["choices"].([]any)[0].(map[string]any)["message"]
				messages = append(messages, message, map[string]any{"role": "tool", "tool_call_id": "call_new", "content": "tool output"}, map[string]any{"role": "user", "content": "continue"})
			} else {
				messages = append(messages, map[string]any{"role": "assistant", "content": response["content"]}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_new", "content": "tool output"}}})
			}
			request["messages"] = messages
			next, _ := json.Marshal(request)
			r = httptest.NewRequest("POST", path, strings.NewReader(string(next)))
			r.Header.Set("Authorization", "Bearer "+key)
			w = httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 || requests != 2 {
				t.Fatalf("round trip failed: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCrossProtocolSSEFlushesBeforeUpstreamCompletes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	seen := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-seen:
		case <-ctx.Done():
			return
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":1}}\n\n")
	}))
	defer upstream.Close()
	a, key := crossTestApp(t, "gemini")
	a.data.Providers[0].BaseURL = upstream.URL
	a.client = upstream.Client()
	gateway := httptest.NewServer(a.routes())
	defer gateway.Close()
	body := strings.Replace(crossChatRequest, `"model":"alias"`, `"model":"alias","stream":true`, 1)
	req, _ := http.NewRequestWithContext(ctx, "POST", gateway.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := gateway.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	scanner := bufio.NewScanner(res.Body)
	observed, done := false, false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, `"content":"hello"`) && !observed {
			observed = true
			close(seen)
		}
		if line == "data: [DONE]" {
			done = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !observed || !done {
		t.Fatal("response was buffered or not completed")
	}
}

func TestCrossProtocolInvalidStreamArgumentsFailExplicitly(t *testing.T) {
	a, key := crossTestApp(t, "anthropic_messages")
	response := strings.Replace(crossClaudeStream, `9007199254740993}`, `9007199254740993`, 1)
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}
	body := strings.Replace(crossChatRequest, `"model":"alias"`, `"model":"alias","stream":true`, 1)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "unsupported_upstream_feature") || strings.Contains(w.Body.String(), "[DONE]") || strings.Contains(w.Body.String(), "9007199254740993") {
		t.Fatalf("invalid args released: %s", w.Body.String())
	}
}

func TestCrossProtocolMessagesBetaAndEndpointBoundaries(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens", "/v1/responses", "/v1/embeddings"} {
		t.Run(path, func(t *testing.T) {
			a, key := crossTestApp(t, "gemini")
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("unsupported feature sent upstream")
				return nil, nil
			})}
			r := httptest.NewRequest("POST", path, strings.NewReader(crossMessagesRequest))
			r.Header.Set("Authorization", "Bearer "+key)
			if path == "/v1/messages" {
				r.Header.Set("anthropic-beta", "future-capability")
			}
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_conversion") {
				t.Fatalf("unclear unsupported endpoint: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCrossProtocolMissingUsageIsNotFabricated(t *testing.T) {
	a, key := crossTestApp(t, "gemini")
	response := `{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}]}`
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + response + "\n\n"))}, nil
	})}
	body := strings.Replace(crossMessagesRequest, `"model":"alias"`, `"model":"alias","stream":true`, 1)
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "unsupported_upstream_feature") || !strings.Contains(w.Body.String(), "response.usage") || strings.Contains(w.Body.String(), "event: message_stop") {
		t.Fatalf("fabricated terminal usage: %s", w.Body.String())
	}
	select {
	case event := <-a.usageQueue:
		if event.Metrics.Known || event.Status != 502 {
			t.Fatalf("fabricated billing totals: %+v", event)
		}
	default:
		t.Fatal("missing failure record")
	}
}

func TestCrossProtocolUsageDoesNotDependOnClientUsageOption(t *testing.T) {
	a, key := crossTestApp(t, "gemini")
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + crossGeminiResponse + "\n\n"))}, nil
	})}
	body := strings.Replace(crossChatRequest, `"model":"alias"`, `"model":"alias","stream":true`, 1)
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), `"usage"`) || !strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("wrong client usage behavior: %s", w.Body.String())
	}
	select {
	case event := <-a.usageQueue:
		if !event.Metrics.Known || event.Metrics.TotalTokens != 24 || event.Metrics.CacheReadTokens != 4 || math.Abs(event.Metrics.Cost-56.0/1_000_000) > 1e-12 {
			t.Fatalf("client option changed billing: %+v", event)
		}
	default:
		t.Fatal("missing usage")
	}
}
