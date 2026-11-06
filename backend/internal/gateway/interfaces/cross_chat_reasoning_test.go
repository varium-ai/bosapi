package interfaces

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessagesChatReasoningStreamsBeforeTools(t *testing.T) {
	for _, field := range []string{"reasoning", "reasoning_content"} {
		t.Run(field, func(t *testing.T) {
			a, key := crossTestApp(t, "openai_chat")
			thinking := "data: {\"choices\":[{\"index\":0,\"delta\":{\"" + field + "\":\"think first\"}}]}\n\n"
			response := thinking + thinking + chatTestStream()
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(response)})}, nil
			})}
			body := strings.Replace(crossMessagesRequest, `"model":"alias"`, `"model":"alias","stream":true`, 1)
			r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("anthropic-beta", "claude-code-20250219,effort-2025-11-24")
			r.Header.Set("anthropic-dangerous-direct-browser-access", "true")
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			output := w.Body.String()
			if w.Code != 200 || strings.Count(output, `"type":"thinking"`) != 1 || strings.Count(output, `"type":"thinking_delta"`) != 2 || !strings.Contains(output, "bos-chat-reasoning-v1:"+field) || !strings.Contains(output, "event: message_stop") {
				t.Fatalf("reasoning stream failed: %d %s", w.Code, output)
			}
			if strings.Index(output, "signature_delta") > strings.Index(output, `"type":"text"`) || strings.Contains(output, "unsupported_") {
				t.Fatalf("reasoning block order corrupted: %s", output)
			}
		})
	}
}

func TestMessagesChatTextChunksShareOneContentBlock(t *testing.T) {
	a, key := crossTestApp(t, "openai_chat")
	frames := []string{
		`{"choices":[{"index":0,"delta":{"content":"BOS"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"_OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`,
		`[DONE]`,
	}
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + strings.Join(frames, "\n\ndata: ") + "\n\n"))}, nil
	})}
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.Replace(crossMessagesRequest, `"model":"alias"`, `"model":"alias","stream":true`, 1)))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	output := w.Body.String()
	if strings.Count(output, `"type":"text"`) != 1 || strings.Count(output, `"type":"text_delta"`) != 2 || strings.Count(output, `"type":"content_block_stop"`) != 1 || !strings.Contains(output, "event: message_stop") {
		t.Fatalf("text chunks did not share a block: %s", output)
	}
}

func TestMessagesChatReasoningOnlyFinishAndToolTransition(t *testing.T) {
	for _, tool := range []bool{false, true} {
		a, key := crossTestApp(t, "openai_chat")
		frames := []string{`{"choices":[{"index":0,"delta":{"reasoning":"consider"}}]}`}
		finish := "stop"
		if tool {
			frames = append(frames, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`)
			finish = "tool_calls"
		}
		frames = append(frames, `{"choices":[{"index":0,"delta":{},"finish_reason":"`+finish+`"}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`, "[DONE]")
		a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + strings.Join(frames, "\n\ndata: ") + "\n\n"))}, nil
		})}
		r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.Replace(crossMessagesRequest, `"model":"alias"`, `"model":"alias","stream":true`, 1)))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if !strings.Contains(w.Body.String(), "signature_delta") || !strings.Contains(w.Body.String(), "event: message_stop") || strings.Contains(w.Body.String(), "unsupported_") {
			t.Fatalf("thinking transition failed: %s", w.Body.String())
		}
	}
}
