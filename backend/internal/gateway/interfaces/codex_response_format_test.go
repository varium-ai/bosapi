package interfaces

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCodexChatSSEBodyWithoutSSEContentType(t *testing.T) {
	for _, contentType := range []string{"", "text/plain; charset=utf-8", "application/json", "application/octet-stream", "Text/Event-Stream; charset=utf-8"} {
		for _, streaming := range []bool{false, true} {
			t.Run(contentType+map[bool]string{false: "/json", true: "/stream"}[streaming], func(t *testing.T) {
				a := testApp(t)
				closed := false
				a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: &codexFormatBody{Reader: byteFragmentReader{strings.NewReader(codexMetadataStream)}, closed: &closed}}, nil
				})}
				request := `{"model":"x","messages":[{"role":"system","content":"Reply briefly."},{"role":"user","content":"Reply OK"}],"max_tokens":32,"stream":false}`
				if streaming {
					request = strings.Replace(request, `"stream":false`, `"stream":true,"stream_options":{"include_usage":true}`, 1)
				}
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(request))
				w := httptest.NewRecorder()
				a.codexChat(w, r, provider{Type: "oauth", Platform: "openai", AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour)})
				body := w.Body.String()
				if w.Code != 200 || !closed || !strings.Contains(body, `"content":"OK"`) || !strings.Contains(body, `"total_tokens":20`) || strings.Contains(body, "unsupported_upstream_feature") || streaming && !strings.Contains(body, "[DONE]") {
					t.Fatalf("response: %d closed=%t %s", w.Code, closed, body)
				}
			})
		}
	}
}

type codexFormatBody struct {
	io.Reader
	closed *bool
}

func (b *codexFormatBody) Close() error { *b.closed = true; return nil }

func TestCodexResponseFormatRejectsInvalidAndTruncatedBodies(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"PRIVATE_ERROR_BODY"}}`,
		`<!DOCTYPE html><html>PRIVATE_ERROR_BODY</html>`,
		"", "arbitrary plain text", "data: not JSON\n\n",
		"event: response.created\ndata: {\"type\":\"response.created\"}\n\n",
		"data: {\"type\":\"response.unknown\"}\n\n" + codexMetadataStream,
	} {
		for _, streaming := range []bool{false, true} {
			a := testApp(t)
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			request := `{"model":"x","messages":[{"role":"user","content":"hi"}],"stream":false}`
			if streaming {
				request = strings.Replace(request, "false", "true", 1)
			}
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(request))
			w := httptest.NewRecorder()
			a.codexChat(w, r, provider{Type: "oauth", Platform: "openai"})
			if !strings.Contains(w.Body.String(), "unsupported_upstream_feature") || strings.Contains(w.Body.String(), "[DONE]") || strings.Contains(w.Body.String(), "PRIVATE_ERROR_BODY") || !streaming && w.Code != 502 {
				t.Fatalf("invalid response: %d %s", w.Code, w.Body.String())
			}
		}
	}
}

func TestCodexResponseSniffPreservesTimingAndClose(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start
	timing := &firstTokenTiming{started: start, now: func() time.Time { return now }}
	reader := &firstTokenReader{start: start, now: &now, reads: []firstTokenRead{
		{"event: response.created\ndata: {\"type\":\"response.created\"}\n\n", 5 * time.Millisecond, nil},
		{"data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n", 75 * time.Millisecond, io.EOF},
	}}
	response := &http.Response{StatusCode: 200, Header: http.Header{}, Body: reader}
	ctx := context.WithValue(context.Background(), gatewayFirstTokenKey{}, timing)
	observeFirstTokenBody(ctx, response)
	body, err := codexChatStream(response)
	if err != nil || timing.elapsedMillis() != nil {
		t.Fatalf("sniff observed metadata as output: %v", err)
	}
	all, err := io.ReadAll(body)
	if err != nil || !strings.HasPrefix(string(all), "event: response.created\n") || !strings.HasSuffix(string(all), "\n\n") {
		t.Fatalf("body changed: %q %v", all, err)
	}
	assertFirstTokenMillis(t, timing.elapsedMillis(), firstTokenMillis(75))
	if err := response.Body.Close(); err != nil || !reader.closed {
		t.Fatalf("underlying body not closed: %v", err)
	}
}
