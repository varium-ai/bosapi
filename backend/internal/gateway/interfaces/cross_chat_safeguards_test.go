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

const chatSafeguardsHint = `"safeguards":[{"type":"dangerous_tool_use","classifier_context":{"private":"PRIVATE_CLASSIFIER_CONTEXT","nested":{"tools":[{"name":"undeclared"}]}}}]`

func TestMessagesChatSafeguardsCompatibilityPreservesToolSecurity(t *testing.T) {
	for _, format := range []string{"openai_chat", ""} {
		for _, stream := range []bool{false, true} {
			for _, blocked := range []bool{false, true} {
				t.Run(format+"/"+testBoolName(stream, blocked), func(t *testing.T) {
					a, key := crossTestApp(t, format)
					sink := &toolRecordingSink{block: blocked}
					a.background.SecurityEvents = sink
					body := strings.Replace(crossMessagesRequest, `"model":`, chatSafeguardsHint+`,"model":`, 1)
					response, contentType := crossOpenAIResponse, "application/json"
					if stream {
						body = strings.Replace(body, `"model":`, `"stream":true,"model":`, 1)
						response, contentType = chatTestStream(), "text/event-stream"
					}
					requests := 0
					a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						requests++
						raw, err := io.ReadAll(r.Body)
						if err != nil {
							t.Fatal(err)
						}
						for _, private := range []string{"safeguards", "classifier_context", "PRIVATE_CLASSIFIER_CONTEXT", "undeclared"} {
							if strings.Contains(string(raw), private) {
								t.Fatalf("local safeguard context leaked upstream: %s", raw)
							}
						}
						if !strings.Contains(string(raw), `"name":"read"`) || !strings.Contains(string(raw), `"model":"actual"`) {
							t.Fatalf("ordinary request was altered: %s", raw)
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(response)})}, nil
					})}
					r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
					r.Header.Set("Authorization", "Bearer "+key)
					w := httptest.NewRecorder()
					a.routes().ServeHTTP(w, r)
					output := w.Body.String()
					if w.Code != 200 || requests != 1 || !strings.Contains(output, "hello") || strings.Contains(output, "unsupported_conversion") {
						t.Fatalf("safeguard hint did not succeed directly: status=%d requests=%d body=%s", w.Code, requests, output)
					}
					if strings.Contains(output, "safeguard_results") || strings.Contains(output, "PRIVATE_CLASSIFIER_CONTEXT") {
						t.Fatalf("unperformed classification was fabricated or local context leaked: %s", output)
					}
					if blocked == strings.Contains(output, "9007199254740993") || len(sink.calls) != 1 || sink.calls[0].Name != "read" {
						t.Fatalf("safeguard compatibility bypassed tool policy: calls=%+v body=%s", sink.calls, output)
					}
					if stream && (!w.Flushed || !strings.Contains(output, "event: message_stop")) {
						t.Fatalf("safeguard hint prevented complete Messages stream: %s", output)
					}
				})
			}
		}
	}
}

func TestMessagesChatSafeguardsDoNotAuthorizeUndeclaredTools(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(testBoolName(stream, false), func(t *testing.T) {
			a, key := crossTestApp(t, "openai_chat")
			body := strings.Replace(crossMessagesRequest, `"model":`, chatSafeguardsHint+`,"model":`, 1)
			response, contentType := strings.Replace(crossOpenAIResponse, `"name":"read"`, `"name":"undeclared"`, 1), "application/json"
			if stream {
				body = strings.Replace(body, `"model":`, `"stream":true,"model":`, 1)
				response, contentType = chatTestStream(), "text/event-stream"
				response = strings.Replace(response, `"name":"re"`, `"name":"undeclared"`, 1)
				response = strings.Replace(response, `"name":"ad",`, "", 1)
			}
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			output := w.Body.String()
			if strings.Contains(output, "9007199254740993") || !strings.Contains(output, "unauthorized_tool") || !strings.Contains(output, "hello") {
				t.Fatalf("classifier context authorized an undeclared tool: %d %s", w.Code, output)
			}
		})
	}
}

func TestNativeAnthropicSafeguardsRemainPassthrough(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(testBoolName(stream, false), func(t *testing.T) {
			a, key := crossTestApp(t, "anthropic_messages")
			body := strings.Replace(crossMessagesRequest, `"model":`, chatSafeguardsHint+`,"model":`, 1)
			// The native adapter preserves provider-defined classification metadata.
			results := `"safeguard_results":[{"type":"dangerous_tool_use","provider_result":"NATIVE_CLASSIFICATION"}]`
			response, contentType := strings.Replace(crossClaudeResponse, `"id":`, results+`,"id":`, 1), "application/json"
			if stream {
				body = strings.Replace(body, `"model":`, `"stream":true,"model":`, 1)
				response, contentType = strings.Replace(crossClaudeStream, `"delta":{"stop_reason":`, `"delta":{`+results+`,"stop_reason":`, 1), "text/event-stream"
			}
			requests := 0
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				var request, original map[string]any
				if err := json.Unmarshal(raw, &request); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(body), &original); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(request["safeguards"], original["safeguards"]) || request["model"] != "actual" {
					t.Fatalf("native safeguard request was modified: %s", raw)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			output := w.Body.String()
			if w.Code != 200 || requests != 1 || !strings.Contains(output, "NATIVE_CLASSIFICATION") || !strings.Contains(output, "safeguard_results") || !strings.Contains(output, "9007199254740993") {
				t.Fatalf("native safeguard response was not preserved: %d requests=%d body=%s", w.Code, requests, output)
			}
			if stream && !strings.Contains(output, "event: message_stop") {
				t.Fatalf("native stream did not complete: %s", output)
			}
		})
	}
}

func TestMessagesSafeguardsCompatibilityDoesNotExpandModelOrProtocolAccess(t *testing.T) {
	for _, tc := range []struct {
		name, format, model string
		status              int
	}{
		{"gemini conversion", "gemini", "alias", http.StatusBadRequest},
		{"unauthorized Chat model", "openai_chat", "unavailable", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, key := crossTestApp(t, tc.format)
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("safeguard compatibility bypassed the model or conversion boundary")
				return nil, nil
			})}
			body := strings.Replace(crossMessagesRequest, `"model":"alias"`, chatSafeguardsHint+`,"model":"`+tc.model+`"`, 1)
			r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("incorrect boundary response: %d %s", w.Code, w.Body.String())
			}
			if tc.format == "gemini" && (!strings.Contains(w.Body.String(), "unsupported_conversion") || !strings.Contains(w.Body.String(), `"param":"safeguards"`)) {
				t.Fatalf("missing explicit Gemini boundary error: %s", w.Body.String())
			}
		})
	}
}
