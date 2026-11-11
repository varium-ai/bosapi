package interfaces

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCodexResponsesSystemInstructionsGateway(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[streaming], func(t *testing.T) {
			a, key := geminiTestApp(t)
			a.data.Providers[0] = provider{ID: 1, Type: "oauth", Platform: "openai", APIFormat: "openai_responses", Status: "active", AccessToken: "token", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour), Models: []string{"actual"}, ModelMapping: map[string]string{"alias": "actual"}}
			called := false
			const preservedInput = `[{"role":"developer","content":"developer"},{"type":"reasoning","encrypted_content":"opaque"},{"type":"function_call","call_id":"old","name":"read","arguments":"{\"n\":9007199254740993}"},{"type":"function_call_output","call_id":"old","output":"done"},{"role":"user","content":"Reply OK"}]`
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				var root map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&root); err != nil {
					t.Fatal(err)
				}
				var actualInput, expectedInput any
				if err := json.Unmarshal(root["input"], &actualInput); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(preservedInput), &expectedInput); err != nil {
					t.Fatal(err)
				}
				if string(root["instructions"]) != `"Reply briefly.\n\nexisting"` || !reflect.DeepEqual(actualInput, expectedInput) || string(root["model"]) != `"actual"` || string(root["stream"]) != "true" || string(root["store"]) != "false" {
					t.Fatalf("bad upstream payload: %s", root)
				}
				stream := strings.Replace(codexMetadataStream, `"output":[],"usage"`, `"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage"`, 1)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
			})}
			request := `{"model":"alias","instructions":"existing","input":[{"role":"system","content":"Reply briefly."},` + preservedInput[1:] + `,"stream":false}`
			if streaming {
				request = strings.Replace(request, `"stream":false`, `"stream":true`, 1)
			}
			r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(request))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if !called || w.Code != 200 || !strings.Contains(w.Body.String(), `"text":"OK"`) || !strings.Contains(w.Body.String(), `"output_tokens":5`) {
				t.Fatalf("response: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCodexResponsesRejectsLossySystemBeforeUpstream(t *testing.T) {
	a := testApp(t)
	called := false
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})}
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"x","input":[{"role":"system","content":[{"type":"input_image","image_url":"https://example.com/a.png"}]},{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()
	a.responses(w, r, provider{Type: "oauth", Platform: "openai", APIFormat: "openai_responses"})
	if called || w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_conversion") || !strings.Contains(w.Body.String(), "input[0].content[0]") {
		t.Fatalf("response: %d %s", w.Code, w.Body.String())
	}
}
