package interfaces

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const crossChatOutputSchema = `{"type":"object","properties":{"n":{"type":"integer","minimum":9007199254740993},"label":{"type":"string","description":"Keep this description"}},"required":["n","label"],"additionalProperties":false,"x-client-annotation":{"revision":9007199254740993}}`

func TestMessagesChatStructuredOutputJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, format := range []string{"schema", "null"} {
			t.Run(testBoolName(stream, false)+"/"+format, func(t *testing.T) {
				a, key := crossTestApp(t, "openai_chat")
				const wantText = `{"n":9007199254740993,"label":"ok"}`
				response := `{"id":"structured_1","choices":[{"index":0,"message":{"role":"assistant","content":"{\"n\":9007199254740993,\"label\":\"ok\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`
				contentType := "application/json"
				if stream {
					frames := []string{
						`{"id":"structured_1","choices":[{"index":0,"delta":{"role":"assistant","content":"{\"n\":9007199254740993,"}}]}`,
						`{"choices":[{"index":0,"delta":{"content":"\"label\":\"ok\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`,
						`[DONE]`,
					}
					response = "data: " + strings.Join(frames, "\n\ndata: ") + "\n\n"
					contentType = "text/event-stream"
				}
				calls := 0
				a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					assertMessagesChatStructuredPayload(t, r, format == "schema", stream)
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(response)})}, nil
				})}
				body := crossChatStructuredRequest(format == "schema", stream)
				r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
				r.Header.Set("x-api-key", key)
				r.Header.Set("anthropic-beta", "structured-outputs-2025-11-13,effort-2025-11-24")
				w := httptest.NewRecorder()
				a.routes().ServeHTTP(w, r)
				if w.Code != 200 || calls != 1 {
					t.Fatalf("structured output failed: calls=%d status=%d body=%s", calls, w.Code, w.Body.String())
				}
				if stream {
					var text strings.Builder
					for _, line := range strings.Split(w.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						var event struct {
							Type  string `json:"type"`
							Delta struct {
								Type string `json:"type"`
								Text string `json:"text"`
							} `json:"delta"`
						}
						if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
							t.Fatal(err)
						}
						if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
							text.WriteString(event.Delta.Text)
						}
					}
					if text.String() != wantText || !w.Flushed || !strings.Contains(w.Body.String(), "event: message_stop") || !strings.Contains(w.Body.String(), `"stop_reason":"end_turn"`) || strings.Contains(w.Body.String(), "event: error") {
						t.Fatalf("structured stream changed: %s", w.Body.String())
					}
				} else {
					var reply struct {
						Type       string `json:"type"`
						Model      string `json:"model"`
						StopReason string `json:"stop_reason"`
						Content    []struct {
							Type string `json:"type"`
							Text string `json:"text"`
						} `json:"content"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
						t.Fatal(err)
					}
					if reply.Type != "message" || reply.Model != "alias" || reply.StopReason != "end_turn" || len(reply.Content) != 1 || reply.Content[0].Type != "text" || reply.Content[0].Text != wantText {
						t.Fatalf("structured response changed: %s", w.Body.String())
					}
				}
				select {
				case event := <-a.usageQueue:
					if event.Status != 200 || event.Metrics.TotalTokens != 5 || event.IsStream != stream {
						t.Fatalf("structured output usage: %+v", event)
					}
				default:
					t.Fatal("missing structured output usage")
				}
			})
		}
	}
}

func TestMessagesChatStructuredOutputUpstreamRejectionDoesNotRetry(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(testBoolName(stream, false), func(t *testing.T) {
			logs := captureUpstreamLogs(t)
			a, key := crossTestApp(t, "openai_chat")
			calls := 0
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				assertMessagesChatStructuredPayload(t, r, true, stream)
				return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"structured-rejected"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","code":"unsupported_parameter","param":"response_format","message":"response_format is not supported by this model"}}`))}, nil
			})}
			r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(crossChatStructuredRequest(true, stream)))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if calls != 1 || w.Code != 400 || !strings.Contains(w.Body.String(), `"type":"invalid_request_error"`) || !strings.Contains(w.Body.String(), `"code":"upstream_error"`) || strings.Contains(w.Body.String(), "event: message_stop") {
				t.Fatalf("upstream rejection lost or retried: calls=%d status=%d body=%s", calls, w.Code, w.Body.String())
			}
			if !strings.Contains(logs.String(), "response_format is not supported by this model") || !strings.Contains(logs.String(), "structured-rejected") {
				t.Fatalf("missing upstream rejection diagnostic: %s", logs.String())
			}
		})
	}
}

func crossChatStructuredRequest(schema, stream bool) string {
	format := "null"
	if schema {
		format = `{"type":"json_schema","schema":` + crossChatOutputSchema + `}`
	}
	body := `{"model":"alias","max_tokens":512,"messages":[{"role":"user","content":"Return n and label"}],"output_config":{"effort":"high","format":` + format + `}}`
	if stream {
		body = strings.Replace(body, `"model":`, `"stream":true,"model":`, 1)
	}
	return body
}

func assertMessagesChatStructuredPayload(t *testing.T, r *http.Request, schema, stream bool) {
	t.Helper()
	var upstream map[string]any
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&upstream); err != nil {
		t.Fatal(err)
	}
	if r.URL.Path != "/v1/chat/completions" || upstream["model"] != "actual" || r.Header.Get("anthropic-beta") != "" {
		t.Fatalf("wrong structured output endpoint or model: %s %+v", r.URL, upstream)
	}
	if _, exists := upstream["output_config"]; exists {
		t.Fatalf("Messages output_config leaked upstream: %+v", upstream)
	}
	if stream && upstream["stream"] != true {
		t.Fatalf("stream flag lost: %+v", upstream)
	}
	if !schema {
		if _, exists := upstream["response_format"]; exists {
			t.Fatalf("null format was forwarded: %+v", upstream)
		}
		return
	}
	var want any
	decoder = json.NewDecoder(strings.NewReader(`{"type":"json_schema","json_schema":{"name":"response","strict":true,"schema":` + crossChatOutputSchema + `}}`))
	decoder.UseNumber()
	if err := decoder.Decode(&want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(upstream["response_format"], want) {
		t.Fatalf("schema or structured output options changed: got=%+v want=%+v", upstream["response_format"], want)
	}
}
