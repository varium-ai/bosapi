package interfaces

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiThinkingControlsThroughChatGateway(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, include := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/include=%t", streaming, include), func(t *testing.T) {
				logs := captureUpstreamLogs(t)
				a, key := crossTestApp(t, "openai_chat")
				called := false
				a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					called = true
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					thinking, _ := body["thinking"].(map[string]any)
					if thinking["type"] != "enabled" || body["stream"] != streaming || body["thinkingConfig"] != nil || body["includeThoughts"] != nil {
						t.Fatalf("wrong upstream request: %+v", body)
					}
					response := `{"choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"PRIVATE_REASONING","content":"Visible answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":17,"completion_tokens":82,"total_tokens":99}}`
					kind := "application/json"
					if streaming {
						kind = "text/event-stream"
						response = "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"PRIVATE_REASONING\"},\"finish_reason\":null}]}\n\n" +
							"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Visible answer\"},\"finish_reason\":\"stop\"}]}\n\n" +
							"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":82,\"total_tokens\":99}}\n\ndata: [DONE]\n\n"
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(response))}, nil
				})}
				operation := "generateContent"
				if streaming {
					operation = "streamGenerateContent"
				}
				body := fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":-1,"includeThoughts":%t}}}`, include)
				r := httptest.NewRequest("POST", "/v1beta/models/alias:"+operation, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+key)
				w := httptest.NewRecorder()
				a.routes().ServeHTTP(w, r)
				output := w.Body.String()
				if !called || w.Code != 200 || !strings.Contains(output, `"text":"Visible answer"`) || !strings.Contains(output, `"finishReason":"STOP"`) || strings.Contains(output, "unsupported_") {
					t.Fatalf("status=%d called=%t response=%s", w.Code, called, output)
				}
				if strings.Contains(output, "PRIVATE_REASONING") != include || strings.Contains(output, `"thought":true`) != include || strings.Contains(output, "thoughtSignature") {
					t.Fatalf("incorrect thought visibility: %s", output)
				}
				for _, metric := range []string{`"promptTokenCount":17`, `"candidatesTokenCount":82`, `"totalTokenCount":99`} {
					if !strings.Contains(output, metric) {
						t.Fatalf("usage lost: %s", output)
					}
				}
				if strings.Contains(logs.String(), "PRIVATE_REASONING") {
					t.Fatal("reasoning leaked into logs")
				}
			})
		}
	}
}

func TestGeminiThinkingInvalidControlsDoNotCallUpstream(t *testing.T) {
	for _, config := range []string{`{"thinkingBudget":2048}`, `{"includeThoughts":"true"}`} {
		a, key := crossTestApp(t, "openai_chat")
		called := false
		a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, fmt.Errorf("unexpected upstream request")
		})}
		body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":` + config + `}}`
		r := httptest.NewRequest("POST", "/v1beta/models/alias:streamGenerateContent", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if called || w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_conversion") || !strings.Contains(w.Body.String(), "generationConfig.thinkingConfig.") {
			t.Fatalf("status=%d called=%t response=%s", w.Code, called, w.Body.String())
		}
	}
}
