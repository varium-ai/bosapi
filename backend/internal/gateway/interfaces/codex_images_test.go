package interfaces

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCodexChatImageDetailGateway(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[streaming], func(t *testing.T) {
			a, key := geminiTestApp(t)
			a.data.Providers[0] = provider{ID: 1, Type: "oauth", Platform: "openai", APIFormat: "openai_responses", Status: "active", AccessToken: "token", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour), Models: []string{"actual"}, ModelMapping: map[string]string{"alias": "actual"}}
			called := false
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				var root struct {
					Model string `json:"model"`
					Input []struct {
						Role    string `json:"role"`
						Content []struct {
							Type, Text, Detail string
							ImageURL           string `json:"image_url"`
						} `json:"content"`
					} `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&root); err != nil {
					t.Fatal(err)
				}
				if root.Model != "actual" || len(root.Input) != 1 || root.Input[0].Role != "user" || len(root.Input[0].Content) != 4 {
					t.Fatalf("unexpected upstream input: %+v", root)
				}
				parts := root.Input[0].Content
				if parts[0].Text != "first" || parts[1].Type != "input_image" || parts[1].ImageURL != "https://example.com/first.png" || parts[1].Detail != "low" || parts[2].Text != "second" || parts[3].Type != "input_image" || parts[3].ImageURL != "data:image/png;base64,aGVsbG8=" || parts[3].Detail != "high" {
					t.Fatalf("lost image content/detail/order: %+v", parts)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(codexMetadataStream))}, nil
			})}
			request := `{"model":"alias","messages":[{"role":"system","content":"Describe the images."},{"role":"user","content":[{"type":"text","text":"first"},{"type":"image_url","image_url":{"url":"https://example.com/first.png","detail":"low"}},{"type":"text","text":"second"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8=","detail":"high"}}]}],"stream":false}`
			if streaming {
				request = strings.Replace(request, `"stream":false`, `"stream":true,"stream_options":{"include_usage":true}`, 1)
			}
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(request))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			body := w.Body.String()
			if !called || w.Code != 200 || !strings.Contains(body, `"content":"OK"`) || !strings.Contains(body, `"total_tokens":20`) || streaming && !strings.Contains(body, "[DONE]") {
				t.Fatalf("response: %d %s", w.Code, body)
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
