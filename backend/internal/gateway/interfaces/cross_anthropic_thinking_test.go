package interfaces

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
)

func anthropicThinkingStream() string {
	prefix := []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"plan ","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"ahead"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed-"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"history"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque-history"}}`,
		`{"type":"content_block_stop","index":1}`,
	}
	frames := ""
	for _, frame := range prefix {
		frames += "data: " + frame + "\n\n"
	}
	response := strings.ReplaceAll(crossClaudeStream, `"index":0`, `"index":2`)
	response = strings.ReplaceAll(response, `"index":1`, `"index":3`)
	index := strings.Index(response, "event: content_block_start")
	return response[:index] + frames + response[index:]
}

// Model a Chat client that concatenates text/argument deltas and appends the
// provider thinking extension, then returns the assistant turn with tool results.
func collectAnthropicChatStream(t *testing.T, raw string) map[string]any {
	t.Helper()
	message := map[string]any{"role": "assistant"}
	var text, reasoning strings.Builder
	thinking := []any{}
	calls := map[int]map[string]any{}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	done := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "data: [DONE]" {
			done = true
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		root, err := gatewayinfra.DecodeProtocolJSON([]byte(strings.TrimPrefix(line, "data: ")))
		if err != nil || root["error"] != nil {
			t.Fatalf("stream conversion error: %v %s", err, line)
		}
		choices, _ := root["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		delta, _ := choices[0].(map[string]any)["delta"].(map[string]any)
		if content, ok := delta["content"].(string); ok {
			text.WriteString(content)
		}
		if content, ok := delta["reasoning_content"].(string); ok {
			reasoning.WriteString(content)
		}
		if blocks, ok := delta["anthropic_thinking"].([]any); ok {
			thinking = append(thinking, blocks...)
		}
		tools, _ := delta["tool_calls"].([]any)
		for _, tool := range tools {
			chunk := tool.(map[string]any)
			index, _ := chunk["index"].(json.Number).Int64()
			call := calls[int(index)]
			if call == nil {
				call = map[string]any{"type": "function", "function": map[string]any{"arguments": ""}}
				calls[int(index)] = call
			}
			if id, ok := chunk["id"].(string); ok {
				call["id"] = id
			}
			function := call["function"].(map[string]any)
			update := chunk["function"].(map[string]any)
			if name, ok := update["name"].(string); ok {
				function["name"] = name
			}
			if args, ok := update["arguments"].(string); ok {
				function["arguments"] = function["arguments"].(string) + args
			}
		}
	}
	if err := scanner.Err(); err != nil || !done {
		t.Fatalf("incomplete stream: %v %s", err, raw)
	}
	toolCalls := []any{}
	for i := 0; i < len(calls); i++ {
		toolCalls = append(toolCalls, calls[i])
	}
	message["content"], message["reasoning_content"] = text.String(), reasoning.String()
	message["anthropic_thinking"], message["tool_calls"] = thinking, toolCalls
	return message
}

func TestChatAnthropicThinkingJSONAndStreamToolContinuation(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "json"
		if streaming {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			a, key := crossTestApp(t, "anthropic_messages")
			const nativeThoughts = `{"type":"thinking","thinking":"plan ahead","signature":"signed-history"},{"type":"redacted_thinking","data":"opaque-history"},`
			jsonResponse := strings.Replace(crossClaudeResponse, `"content":[`, `"content":[`+nativeThoughts, 1)
			want, _ := gatewayinfra.DecodeProtocolJSON([]byte(jsonResponse))
			calls := 0
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				root, err := gatewayinfra.DecodeProtocolJSON(raw)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(root["thinking"], map[string]any{"type": "adaptive", "display": "summarized"}) || root["output_config"].(map[string]any)["effort"] != "high" {
					t.Fatalf("caller thinking settings changed: %s", raw)
				}
				if calls == 2 {
					messages := root["messages"].([]any)
					if !reflect.DeepEqual(messages[len(messages)-2].(map[string]any)["content"], want["content"]) {
						t.Fatalf("signed tool history changed: %s", raw)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(crossClaudeResponse))}, nil
				}
				response, contentType := jsonResponse, "application/json"
				if streaming {
					response, contentType = anthropicThinkingStream(), "text/event-stream"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(response)})}, nil
			})}
			request, _ := gatewayinfra.DecodeProtocolJSON([]byte(crossChatRequest))
			request["thinking"] = map[string]any{"type": "adaptive", "display": "summarized"}
			request["output_config"] = map[string]any{"effort": "high"}
			if streaming {
				request["stream"] = true
				request["stream_options"] = map[string]any{"include_usage": true}
			}
			serve := func(request map[string]any) *httptest.ResponseRecorder {
				body, _ := json.Marshal(request)
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(body)))
				r.Header.Set("Authorization", "Bearer "+key)
				w := httptest.NewRecorder()
				a.routes().ServeHTTP(w, r)
				if w.Code != 200 || strings.Contains(w.Body.String(), "unsupported_") {
					t.Fatalf("thinking request failed: %d %s", w.Code, w.Body.String())
				}
				return w
			}
			w := serve(request)
			var message map[string]any
			if streaming {
				message = collectAnthropicChatStream(t, w.Body.String())
				if !w.Flushed || !strings.Contains(w.Body.String(), `"total_tokens":24`) {
					t.Fatalf("stream usage/delivery lost: %s", w.Body.String())
				}
			} else {
				root, _ := gatewayinfra.DecodeProtocolJSON(w.Body.Bytes())
				message = root["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
			}
			if message["content"] != "hello" || message["reasoning_content"] != "plan ahead" || len(message["anthropic_thinking"].([]any)) != 2 {
				t.Fatalf("reasoning or opaque history lost: %s", w.Body.String())
			}
			request["messages"] = append(request["messages"].([]any), message, map[string]any{"role": "tool", "tool_call_id": "call_new", "content": "tool result"})
			delete(request, "stream")
			delete(request, "stream_options")
			serve(request)
			if calls != 2 {
				t.Fatalf("tool continuation never reached upstream: calls=%d", calls)
			}
		})
	}
}

func TestChatAnthropicThinkingRejectsIncompleteStreams(t *testing.T) {
	for _, tc := range []struct{ name, response, field string }{
		{"missing signature", strings.ReplaceAll(anthropicThinkingStream(), `"signature":"signed-"`, `"signature":""`), "signature"},
		{"wrong thinking type", strings.ReplaceAll(anthropicThinkingStream(), `"thinking":"ahead"`, `"thinking":3`), "delta.thinking"},
		{"wrong signature type", strings.ReplaceAll(anthropicThinkingStream(), `"signature":"history"`, `"signature":3`), "delta.signature"},
		{"unknown delta", strings.ReplaceAll(anthropicThinkingStream(), `"thinking":"ahead"`, `"thinking":"ahead","future":"PRIVATE"`), "delta.future"},
		{"truncated thinking", strings.Split(anthropicThinkingStream(), `data: {"type":"content_block_stop","index":0}`)[0], "stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := tc.response
			if tc.name == "missing signature" {
				response = strings.ReplaceAll(response, `"signature":"history"`, `"signature":""`)
			}
			a, key := crossTestApp(t, "anthropic_messages")
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			body := strings.Replace(crossChatRequest, `"model":"alias"`, `"model":"alias","stream":true,"thinking":{"type":"adaptive"}`, 1)
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			output := w.Body.String()
			if !strings.Contains(output, "unsupported_upstream_feature") || !strings.Contains(output, tc.field) || strings.Contains(output, "[DONE]") || strings.Contains(output, "PRIVATE") {
				t.Fatalf("incomplete thinking appeared successful: %s", output)
			}
		})
	}
}
