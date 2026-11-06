package interfaces

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	userdomain "bosapi/backend/internal/user/domain"
)

func TestGatewayChatStreamCollectsUsageAndSettlesQuota(t *testing.T) {
	for _, tc := range []struct{ name, options string }{
		{"missing", ""},
		{"null", `,"stream_options":null`},
		{"empty", `,"stream_options":{}`},
		{"disabled", `,"stream_options":{"include_usage":false}`},
		{"enabled", `,"stream_options":{"include_usage":true}`},
		{"extensions", `,"stream_options":{"include_usage":false,"vendor_id":9007199254740993}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			repository := &recordingGatewayKeyRepository{stubGatewayKeyRepository: stubGatewayKeyRepository{key: userdomain.APIKey{
				ID: 115, OrganizationID: 3, Status: "active",
			}}}
			a.keyRepository = repository
			a.usageQueue = make(chan usageEvent, 1)
			a.data.Providers = []provider{{
				ID: 1, Type: "api_key", Status: "active", APIFormat: "openai_chat", BaseURL: "https://upstream.example/v1",
				Models: []string{"glm-upstream"}, ModelMapping: map[string]string{"glm-alias": "glm-upstream"},
			}}
			const text = "data: {\"id\":\"chat_test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\r\n\r\n"
			const usage = "data: {\"id\":\"chat_test\",\"choices\":[],\"usage\":{\"prompt_tokens\":15,\"completion_tokens\":8,\"total_tokens\":23}}\r\n\r\n"
			const done = "data: [DONE]\r\n\r\n"
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				var request struct {
					Model         string                     `json:"model"`
					Stream        bool                       `json:"stream"`
					StreamOptions map[string]json.RawMessage `json:"stream_options"`
					Messages      []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"messages"`
					Extension json.RawMessage `json:"extension"`
				}
				if err := json.Unmarshal(raw, &request); err != nil {
					t.Fatal(err)
				}
				if request.Model != "glm-upstream" || !request.Stream || len(request.Messages) != 1 || request.Messages[0].Role != "user" || request.Messages[0].Content != "hello" || string(request.Extension) != `9007199254740993` {
					t.Fatalf("request fields changed: %s", raw)
				}
				if tc.name == "extensions" && string(request.StreamOptions["vendor_id"]) != "9007199254740993" {
					t.Fatalf("stream options extension changed: %s", raw)
				}
				// Match the production upstream: it emits counts only on opt-in.
				response := text
				if string(request.StreamOptions["include_usage"]) == "true" {
					response += usage
				}
				response += done
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream; charset=utf-8"}}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(response)})}, nil
			})}
			payload := `{"model":"glm-alias","messages":[{"role":"user","content":"hello"}],"stream":true,"extension":9007199254740993` + tc.options + `}`
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
			r.Header.Set("Authorization", "Bearer sk-gw-chat-usage-test")
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != http.StatusOK || !w.Flushed || !strings.Contains(w.Body.String(), "OK") || !strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("stream not delivered: status=%d body=%s", w.Code, w.Body.String())
			}
			select {
			case event := <-a.usageQueue:
				if !event.Metrics.Known || event.Metrics.InputTokens != 15 || event.Metrics.OutputTokens != 8 || event.Metrics.TotalTokens != 23 || !event.IsStream || event.Model != "glm-alias" {
					t.Fatalf("incorrect usage event: %+v", event)
				}
			default:
				t.Fatal("usage event missing")
			}
			record := repository.record
			if record.APIKeyID != 115 || record.StatusCode != 200 || record.InputTokens != 15 || record.OutputTokens != 8 || record.TotalTokens != 23 || !record.IsStream {
				t.Fatalf("incorrect persisted usage/quota: %+v", record)
			}
		})
	}
}

func TestChatStreamUsageLeavesOtherRequestsUnchanged(t *testing.T) {
	for _, tc := range []struct{ name, path, payload string }{
		{"responses stream", "/responses", ` {"model":"test","stream":true,"input":"hello"} `},
		{"messages stream", "/messages", ` {"model":"test","stream":true,"messages":[]} `},
		{"non-streaming", "/chat/completions", ` {"model":"test","stream":false,"messages":[]} `},
		{"implicit non-streaming", "/chat/completions", ` {"model":"test","messages":[]} `},
		{"invalid options", "/chat/completions", ` {"stream":true,"stream_options":"invalid"} `},
		{"invalid stream", "/chat/completions", ` {"stream":"true"} `},
		{"invalid JSON", "/chat/completions", `{"stream":true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(tc.payload)
			if got := requestChatStreamUsage(payload, tc.path); !bytes.Equal(got, payload) {
				t.Fatalf("unrelated or invalid request changed: got %s want %s", got, payload)
			}
		})
	}
}
