package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/platform/errorcode"
)

type firstTokenRead struct {
	data string
	at   time.Duration
	err  error
}

type firstTokenReader struct {
	reads  []firstTokenRead
	now    *time.Time
	start  time.Time
	closed bool
}

func (reader *firstTokenReader) Read(buffer []byte) (int, error) {
	if len(reader.reads) == 0 {
		return 0, io.EOF
	}
	read := reader.reads[0]
	*reader.now = reader.start.Add(read.at)
	n := copy(buffer, read.data)
	if n < len(read.data) {
		reader.reads[0].data = read.data[n:]
		return n, nil
	}
	reader.reads = reader.reads[1:]
	return n, read.err
}

func (reader *firstTokenReader) Close() error {
	reader.closed = true
	return nil
}

func TestFirstTokenBodyTiming(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		status     int
		reads      []firstTokenRead
		want       *uint32
		readError  error
	}{
		{"json first read", "application/json", 200, []firstTokenRead{{"", time.Millisecond, nil}, {" \n", 5 * time.Millisecond, nil}, {"{", 15 * time.Millisecond, nil}, {"}", time.Second, io.EOF}}, firstTokenMillis(15), nil},
		{"zero is recorded", "application/json", 200, []firstTokenRead{{"{}", 0, io.EOF}}, firstTokenMillis(0), nil},
		{"empty response", "application/json", 200, nil, nil, nil},
		{"error response", "application/json", 500, []firstTokenRead{{`{"error":"unavailable"}`, time.Millisecond, io.EOF}}, nil, nil},
		{"no content on cancellation", "application/json", 200, []firstTokenRead{{"", time.Millisecond, context.Canceled}}, nil, context.Canceled},
		{"sse skips metadata", "text/event-stream", 200, []firstTokenRead{
			{": keepalive\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\ndata: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n", 5 * time.Millisecond, nil},
			{"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n", 75 * time.Millisecond, nil},
			{"data: [DONE]\n\n", time.Second, io.EOF},
		}, firstTokenMillis(75), nil},
		{"sse fragmented crlf", "text/event-stream; charset=utf-8", 200, []firstTokenRead{
			{"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\r", 10 * time.Millisecond, nil},
			{"\n\r", 20 * time.Millisecond, nil}, {"\n", 30 * time.Millisecond, io.EOF},
		}, firstTokenMillis(30), nil},
		{"sse error only", "text/event-stream", 200, []firstTokenRead{{"event: error\ndata: {\"message\":\"failure\"}\n\ndata: [DONE]\n\n", 10 * time.Millisecond, io.EOF}}, nil, nil},
		{"sse final event without delimiter", "text/event-stream", 200, []firstTokenRead{{"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}", 10 * time.Millisecond, io.EOF}}, firstTokenMillis(10), nil},
		{"sse output before read failure", "text/event-stream", 200, []firstTokenRead{{"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n", 10 * time.Millisecond, io.ErrUnexpectedEOF}}, firstTokenMillis(10), io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			now := start
			timing := &firstTokenTiming{started: start, now: func() time.Time { return now }}
			ctx := context.WithValue(context.Background(), gatewayFirstTokenKey{}, timing)
			reader := &firstTokenReader{reads: append([]firstTokenRead(nil), tc.reads...), now: &now, start: start}
			response := &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.kind}}, Body: reader}
			observeFirstTokenBody(ctx, response)
			if timing.elapsedMillis() != nil {
				t.Fatal("response headers recorded a first token")
			}
			got, err := io.ReadAll(response.Body)
			if !errors.Is(err, tc.readError) {
				t.Fatalf("read error = %v, want %v", err, tc.readError)
			}
			var wantBody strings.Builder
			for _, read := range tc.reads {
				wantBody.WriteString(read.data)
			}
			if string(got) != wantBody.String() {
				t.Fatalf("response body changed: %q", got)
			}
			assertFirstTokenMillis(t, timing.elapsedMillis(), tc.want)
			if err := response.Body.Close(); err != nil || !reader.closed {
				t.Fatalf("body close was not forwarded: %v", err)
			}
		})
	}
}

func TestFirstTokenOutputProtocols(t *testing.T) {
	for _, tc := range []struct {
		name, payload, event string
		want                 bool
	}{
		{"chat text", `{"choices":[{"delta":{"content":"hello"}}]}`, "", true},
		{"chat reasoning", `{"choices":[{"delta":{"reasoning_content":"thinking"}}]}`, "", true},
		{"chat tool", `{"choices":[{"delta":{"tool_calls":[{"function":{"name":"read"}}]}}]}`, "", true},
		{"chat tool arguments", `{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{"}}]}}]}`, "", true},
		{"anthropic text", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`, "", true},
		{"anthropic thinking", `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"hmm"}}`, "", true},
		{"anthropic tool", `{"type":"content_block_start","content_block":{"type":"tool_use","name":"read"}}`, "", true},
		{"responses text", `{"type":"response.output_text.delta","delta":"hello"}`, "", true},
		{"responses event header", `{"delta":"hello"}`, "response.output_text.delta", true},
		{"responses tool", `{"type":"response.output_item.added","item":{"type":"function_call","name":"read"}}`, "", true},
		{"responses completed", `{"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"hello"}]}]}}`, "", true},
		{"responses audio", `{"type":"response.output_audio.delta","delta":"YQ=="}`, "", true},
		{"realtime text", `{"type":"response.text.delta","delta":"hello"}`, "", true},
		{"realtime transcript", `{"type":"response.audio_transcript.delta","delta":"hello"}`, "", true},
		{"realtime output transcript", `{"type":"response.output_audio_transcript.delta","delta":"hello"}`, "", true},
		{"gemini text", crossGeminiResponse, "", true},
		{"gemini media", `{"candidates":[{"content":{"parts":[{"inlineData":{"data":"YQ=="}}]}}]}`, "", true},
		{"empty chat content", `{"choices":[{"delta":{"role":"assistant","content":""}}]}`, "", false},
		{"usage only", `{"usage":{"input_tokens":10}}`, "", false},
		{"empty text block", `{"type":"content_block_start","content_block":{"type":"text","text":""}}`, "", false},
		{"response created", `{"type":"response.created","response":{"output":[]}}`, "", false},
		{"error header", `{"message":"failure"}`, "error", false},
		{"error body", `{"error":{"message":"failure"}}`, "", false},
		{"declared error", `{"type":"error","message":"failure"}`, "", false},
		{"failed response", `{"type":"response.failed","response":{"output":[{"content":"hello"}]}}`, "", false},
		{"ping", `{"type":"ping"}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal([]byte(tc.payload), &value); err != nil {
				t.Fatal(err)
			}
			if got := firstTokenEventOutput(value, tc.event); got != tc.want {
				t.Fatalf("output = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFirstTokenTimingBoundsAndFrame(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, elapsed := range []time.Duration{-time.Second, 0, 15 * time.Millisecond, time.Duration(^uint32(0))*time.Millisecond + time.Second} {
		t.Run(elapsed.String(), func(t *testing.T) {
			timing := &firstTokenTiming{started: start, now: func() time.Time { return start.Add(elapsed) }}
			ctx := context.WithValue(context.Background(), gatewayFirstTokenKey{}, timing)
			for _, payload := range []string{`not json`, `{"type":"response.created"}`} {
				observeFirstTokenFrame(ctx, []byte(payload))
				assertFirstTokenMillis(t, timing.elapsedMillis(), nil)
			}
			observeFirstTokenFrame(ctx, []byte(`{"type":"response.output_text.delta","delta":"hello"}`))
			want := uint32(max(0, min(elapsed.Milliseconds(), int64(^uint32(0)))))
			assertFirstTokenMillis(t, timing.elapsedMillis(), &want)
			timing.now = func() time.Time { return start.Add(time.Hour) }
			observeFirstTokenFrame(ctx, []byte(`{"type":"response.output_text.delta","delta":"later"}`))
			assertFirstTokenMillis(t, timing.elapsedMillis(), &want)
		})
	}
	observeFirstTokenFrame(context.Background(), []byte(`{}`))
	observeFirstTokenBody(context.Background(), nil)
	assertFirstTokenMillis(t, (*firstTokenTiming)(nil).elapsedMillis(), nil)
	(&firstTokenTiming{}).observe()
}

func TestGatewayFirstTokenSurvivesNonStreamingResponseAndPersistence(t *testing.T) {
	a, key := crossTestApp(t, "openai_chat")
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start
	timing := &firstTokenTiming{started: start, now: func() time.Time { return now }}
	ctx := context.WithValue(context.Background(), gatewayFirstTokenKey{}, timing)
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: &firstTokenReader{
			start: start, now: &now, reads: []firstTokenRead{
				{"data: {\"type\":\"response.created\",\"response\":{\"output\":[]}}\n\n", 5 * time.Millisecond, nil},
				{"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n", 25 * time.Millisecond, nil},
				{"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n", time.Second, io.EOF},
			},
		}}, nil
	})}
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"alias","input":"hello","stream":false}`)).WithContext(ctx)
	state := &usageMetricsState{}
	r = r.WithContext(context.WithValue(r.Context(), gatewayUsageStateContextKey{}, state))
	r = r.WithContext(context.WithValue(r.Context(), gatewayAPIKeyRequestKey{}, a.data.Keys[0]))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.responses(w, r, a.data.Providers[0])
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hello") {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
	a.recordRequest(r, a.data.Providers[0], w.Code, time.Second, errorcode.Code(""))
	select {
	case event := <-a.usageQueue:
		assertFirstTokenMillis(t, event.FirstTokenMillis, firstTokenMillis(25))
		assertFirstTokenMillis(t, gatewayUsageRecord(event).FirstTokenMillis, firstTokenMillis(25))
		if event.Metrics.TotalTokens != 2 {
			t.Fatalf("timing changed usage: %+v", event.Metrics)
		}
	default:
		t.Fatal("missing usage event")
	}
}

func TestGatewayFirstTokenMiddlewareWiring(t *testing.T) {
	for _, tc := range []struct {
		name, target, response, kind string
	}{
		{"native json", "openai_chat", crossOpenAIResponse, "application/json"},
		{"native sse", "openai_chat", chatTestStream(), "text/event-stream"},
		{"converted json", "anthropic_messages", crossClaudeResponse, "application/json"},
		{"converted sse", "anthropic_messages", crossClaudeStream, "text/event-stream"},
		{"gemini sse", "gemini", "data: " + crossGeminiResponse + "\n\n", "text/event-stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, key := crossTestApp(t, tc.target)
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.kind}}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(tc.response)})}, nil
			})}
			body := crossChatRequest
			if tc.kind == "text/event-stream" {
				body = strings.Replace(body, `"model":`, `"stream":true,"model":`, 1)
			}
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "hello") {
				t.Fatalf("response = %d %s", w.Code, w.Body.String())
			}
			select {
			case event := <-a.usageQueue:
				if event.FirstTokenMillis == nil || int64(*event.FirstTokenMillis) > event.Duration.Milliseconds() {
					t.Fatalf("first token is missing or exceeds request duration: %+v", event)
				}
			default:
				t.Fatal("missing usage event")
			}
		})
	}
}

func firstTokenMillis(value uint32) *uint32 { return &value }

func assertFirstTokenMillis(t *testing.T, got, want *uint32) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("first token = %d, want unavailable", *got)
		}
		return
	}
	if got == nil || *got != *want {
		t.Fatalf("first token = %v, want %d ms", got, *want)
	}
}
