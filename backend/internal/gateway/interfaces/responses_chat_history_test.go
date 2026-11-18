package interfaces

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResponsesChatHistoryHTTP(t *testing.T) {
	for _, mode := range []string{"json", "sse", "sse_filtered"} {
		t.Run(mode, func(t *testing.T) {
			a, key := crossTestApp(t, "openai_chat")
			a.data.Providers[0].ModelMapping["other-model"] = "actual"
			other := "sk-gw-" + random(32)
			a.data.Keys = append(a.data.Keys, apiKey{ID: 8, OrgID: 9, Hash: digest(other), Status: "active", ProviderID: 1})
			calls := 0
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				if r.URL.Path != "/v1/chat/completions" || strings.Contains(string(raw), `"previous_response_id"`) || strings.Contains(string(raw), `"store"`) {
					t.Fatalf("unexpected upstream request %s %s", r.URL.Path, raw)
				}
				contentType, response := "application/json", crossOpenAIResponse
				if calls == 1 && mode == "json" {
					response = strings.Replace(response, `"content":"hello"`, `"reasoning_content":"saved reasoning","content":"hello"`, 1)
				}
				if calls == 1 && mode != "json" {
					contentType, response = "text/event-stream", chatTestStream()
					if mode == "sse_filtered" {
						response = strings.ReplaceAll(response, `"name":"re"`, `"name":"blocked_re"`)
					}
				}
				if calls >= 2 {
					for _, part := range []string{`"content":"hello"`, `"role":"assistant"`} {
						if !strings.Contains(string(raw), part) {
							t.Fatalf("missing %s in %s", part, raw)
						}
					}
					if strings.Contains(string(raw), "prior synthetic prompt") != (calls == 2) ||
						strings.Contains(string(raw), "item_reference") {
						t.Fatalf("unexpected replay scope: %s", raw)
					}
					if mode == "json" && !strings.Contains(string(raw), `"reasoning_content":"saved reasoning"`) {
						t.Fatalf("stored reasoning not replayed: %s", raw)
					}
					if mode != "sse_filtered" && (!strings.Contains(string(raw), `"tool_call_id":"call_new"`) || !strings.Contains(string(raw), `9007199254740993`)) {
						t.Fatalf("tool history not restored: %s", raw)
					}
					if mode == "sse_filtered" && strings.Contains(string(raw), `blocked_read`) {
						t.Fatalf("blocked tool reappeared: %s", raw)
					}
					response = `{"choices":[{"message":{"role":"assistant","content":"continued"},"finish_reason":"stop"}]}`
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			routes := a.routes()
			request := func(method, path, body, credential string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+credential)
				w := httptest.NewRecorder()
				routes.ServeHTTP(w, r)
				return w
			}
			first := `{"model":"alias","store":true,"instructions":"first-turn-only","input":"prior synthetic prompt","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}`
			if mode != "json" {
				first = `{"stream":true,` + first[1:]
			}
			created := request("POST", "/v1/responses", first, key)
			if created.Code != 200 {
				t.Fatalf("create: %d %s", created.Code, created.Body.String())
			}
			raw := created.Body.Bytes()
			if mode != "json" {
				_, terminal, ok := strings.Cut(string(raw), "event: response.completed\ndata: ")
				if !ok {
					t.Fatalf("no terminal response: %s", raw)
				}
				var envelope struct{ Response json.RawMessage }
				if err := json.Unmarshal([]byte(strings.TrimSpace(terminal)), &envelope); err != nil {
					t.Fatal(err)
				}
				raw = envelope.Response
			}
			var response struct {
				ID     string
				Output []struct{ ID, Type string }
			}
			if err := json.Unmarshal(raw, &response); err != nil || !chatResponseID(response.ID) {
				t.Fatalf("invalid response ID: %s", raw)
			}
			path := "/v1/responses/" + response.ID
			stored := request("GET", path, "", key)
			if stored.Code != 200 || strings.TrimSpace(stored.Body.String()) != string(raw) {
				t.Fatalf("retrieve: %d %s, want %s", stored.Code, stored.Body.String(), raw)
			}
			if mode == "sse_filtered" && !strings.Contains(stored.Body.String(), "unauthorized_tool") {
				t.Fatalf("unfiltered stored response: %s", stored.Body.String())
			}
			denied := request("GET", path, "", other)
			if denied.Code != 404 || strings.Contains(denied.Body.String(), "hello") {
				t.Fatalf("cross-key GET: %d %s", denied.Code, denied.Body.String())
			}
			input := `[{"type":"function_call_output","call_id":"call_new","output":"synthetic result"}]`
			if mode == "sse_filtered" {
				input = `"please continue"`
			} else {
				for _, item := range response.Output {
					if item.Type == "function_call" {
						input = fmt.Sprintf(`[{"type":"item_reference","id":%q},%s`, item.ID, input[1:])
					}
				}
			}
			next := `{"model":"alias","store":false,"previous_response_id":"` + response.ID + `","input":` + input + `}`
			denied = request("POST", "/v1/responses", next, other)
			if denied.Code != 400 || !strings.Contains(denied.Body.String(), "previous_response_not_found") || calls != 1 {
				t.Fatalf("cross-key resume: %d %s calls=%d", denied.Code, denied.Body.String(), calls)
			}
			continued := request("POST", "/v1/responses", next, key)
			if continued.Code != 200 || !strings.Contains(continued.Body.String(), "continued") || calls != 2 {
				t.Fatalf("continue: %d %s calls=%d", continued.Code, continued.Body.String(), calls)
			}
			var continuation struct{ ID string }
			if err := json.Unmarshal(continued.Body.Bytes(), &continuation); err != nil || !chatResponseID(continuation.ID) {
				t.Fatal("missing continued ID")
			}
			if w := request("GET", "/v1/responses/"+continuation.ID, "", key); w.Code != 404 {
				t.Fatalf("store=false was stored: %d", w.Code)
			}
			var referenced []any
			for _, item := range response.Output {
				switch item.Type {
				case "message", "reasoning", "function_call":
					reference := map[string]string{"id": item.ID}
					if mode != "sse" || item.Type != "message" {
						reference["type"] = "item_reference"
					}
					referenced = append(referenced, reference)
				}
			}
			if len(referenced) == 0 {
				t.Fatal("expected stored output items")
			}
			if mode == "sse_filtered" {
				referenced = append(referenced, "please continue")
			} else {
				referenced = append(referenced, map[string]string{"type": "function_call_output", "call_id": "call_new", "output": "synthetic result"})
			}
			referenceBody, err := json.Marshal(map[string]any{"model": "alias", "input": referenced})
			if err != nil {
				t.Fatal(err)
			}
			assertMissing := func(body, credential string) {
				t.Helper()
				before := calls
				w := request("POST", "/v1/responses", body, credential)
				if w.Code != 400 || !strings.Contains(w.Body.String(), "stored item not found or expired") ||
					strings.Contains(w.Body.String(), "hello") || calls != before {
					t.Fatalf("unavailable item reference: %d %s calls=%d", w.Code, w.Body.String(), calls)
				}
			}
			assertMissing(string(referenceBody), other)
			assertMissing(strings.Replace(string(referenceBody), `"model":"alias"`, `"model":"other-model"`, 1), key)
			missingBody := fmt.Sprintf(`{"model":"alias","input":[{"type":"item_reference","id":%q},"continue"]}`, "fc_"+response.ID+"_999")
			assertMissing(missingBody, key)
			if mode == "sse_filtered" {
				blockedBody := fmt.Sprintf(`{"model":"alias","input":[{"type":"item_reference","id":%q},"continue"]}`, "fc_"+response.ID+"_1")
				assertMissing(blockedBody, key)
			}
			if w := request("POST", "/v1/responses", string(referenceBody), key); w.Code != 200 || !strings.Contains(w.Body.String(), "continued") || calls != 3 {
				t.Fatalf("reference replay: %d %s calls=%d", w.Code, w.Body.String(), calls)
			}
			for name, item := range a.responsesChatHistory.entries {
				item.expires = time.Now().Add(-time.Minute)
				a.responsesChatHistory.entries[name] = item
			}
			if w := request("GET", path, "", key); w.Code != 404 {
				t.Fatalf("expired entry returned: %d", w.Code)
			}
			assertMissing(string(referenceBody), key)
		})
	}
}

func TestResponsesChatHistoryOversizeDoesNotComplete(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(testBoolName(stream, true), func(t *testing.T) {
			a, key := crossTestApp(t, "openai_chat")
			output := strings.Repeat("x", 600<<10)
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				kind, raw := "application/json", strings.Replace(crossOpenAIResponse, `"content":"hello"`, `"content":"`+output+`"`, 1)
				if stream {
					kind, raw = "text/event-stream", strings.Replace(chatTestStream(), `"content":"hello"`, `"content":"`+output+`"`, 1)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(raw))}, nil
			})}
			body, err := json.Marshal(map[string]any{"model": "alias", "store": true, "stream": stream, "input": strings.Repeat("y", 600<<10)})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			expected := http.StatusBadRequest
			if stream {
				expected = http.StatusOK
			}
			if w.Code != expected || !strings.Contains(w.Body.String(), "response exceeds the stored history limit") || strings.Contains(w.Body.String(), "event: response.completed") || len(a.responsesChatHistory.entries) != 0 {
				t.Fatalf("unexpected completion: status=%d body size=%d stored=%d", w.Code, w.Body.Len(), len(a.responsesChatHistory.entries))
			}
		})
	}
}
