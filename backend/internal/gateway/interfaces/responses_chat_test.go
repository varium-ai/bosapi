package interfaces

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesChatBridgePreservesHistoryAndFunctionCalls(t *testing.T) {
	for _, mode := range []struct {
		name, format string
		stream       bool
		probes       int
	}{
		{name: "configured_chat_json", format: "openai_chat"},
		{name: "detected_chat_stream", format: "openai_responses", stream: true, probes: 2},
	} {
		t.Run(mode.name, func(t *testing.T) {
			a, key := crossTestApp(t, mode.format)
			a.data.Providers[0].BaseURL = "https://upstream.example/glm/v1/responses"
			body := `{"model":"alias","store":false,"instructions":"be concise","input":[{"role":"user","content":"start"},{"type":"reasoning","summary":[{"type":"summary_text","text":"past thought"}]},{"role":"assistant","content":[{"type":"output_text","text":"previous answer","annotations":[{"type":"url_citation","url":"https://citation.example/","title":"citation metadata","start_index":0,"end_index":8}]}]},{"type":"function_call","call_id":"call_old","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"call_old","output":[{"type":"input_text","text":"old result"},{"type":"input_image","image_url":"https://example.com/tool.png","detail":"low"}]},{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/user.png","detail":"high"}]}],"tools":[{"type":"function","name":"read","strict":true,"parameters":{"type":"object"}}],"safety_identifier":"test-user","modalities":["text"],"truncation":"disabled"}`
			body = `{"metadata":{"source":"CLIENT_METADATA"},"client_metadata":{"source":"CODEX_CLIENT_CONTEXT","tools":[{"type":"function","name":"undeclared"}]},` + body[1:]
			if mode.stream {
				body = `{"stream":true,"stream_options":{"include_obfuscation":false},` + body[1:]
			}
			calls := 0
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				for _, omitted := range []string{"CLIENT_METADATA", "CODEX_CLIENT_CONTEXT", `"client_metadata"`, "citation.example", "citation metadata", `"annotations"`, `"modalities"`, `"truncation"`, `"include_obfuscation"`} {
					if strings.Contains(string(raw), omitted) {
						t.Fatalf("unexpected field or metadata %q in upstream request", omitted)
					}
				}
				status, kind, response := 200, "application/json", crossOpenAIResponse
				path := "/glm/v1/chat/completions"
				if calls <= mode.probes {
					if strings.Contains(string(raw), "previous answer") {
						t.Fatal("probe contained user history")
					}
					if calls == 1 {
						path, status, response = "/glm/v1/responses", 400, legacyInputError
					} else {
						response = probeChatResponse
					}
				} else {
					for _, want := range []string{`"model":"actual"`, `"role":"system"`, `"content":"previous answer"`, `"reasoning_content":"past thought"`, `"tool_call_id":"call_old"`, `"strict":true`, `"safety_identifier":"test-user"`, `"detail":"low","url":"https://example.com/tool.png"`, `"detail":"high","url":"https://example.com/user.png"`} {
						if !strings.Contains(string(raw), want) {
							t.Fatalf("missing %s in %s", want, raw)
						}
					}
					if mode.stream {
						kind, response = "text/event-stream", chatTestStream()
						response = strings.Replace(response, `"content":"hello"`, `"reasoning_content":"thinking now","content":"hello"`, 1)
					}
				}
				if r.URL.Path != path || r.Header.Get("Authorization") != "Bearer provider-key" {
					t.Fatalf("unexpected upstream path or credential for call %d: %s", calls, r.URL.Path)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			for i := 0; i < 2; i++ {
				r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+key)
				w := httptest.NewRecorder()
				a.routes().ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				output := w.Body.String()
				if mode.stream {
					for _, event := range []string{"response.created", "response.reasoning_summary_text.delta", "response.content_part.added", "response.output_text.delta", "response.function_call_arguments.done", "response.completed"} {
						if !strings.Contains(output, "event: "+event+"\n") {
							t.Fatalf("missing %s in %s", event, output)
						}
					}
					if strings.Contains(output, "[DONE]") {
						t.Fatal("Chat sentinel leaked")
					}
				} else {
					var response map[string]any
					if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response["object"] != "response" || response["model"] != "alias" || response["status"] != "completed" {
						t.Fatalf("invalid Responses JSON: %s", output)
					}
				}
				for _, want := range []string{`"type":"output_text"`, `"type":"function_call"`, `"call_id":"call_new"`, "9007199254740993", `"total_tokens":24`} {
					if !strings.Contains(output, want) {
						t.Fatalf("missing %s in %s", want, output)
					}
				}
			}
			if calls != mode.probes+2 {
				t.Fatalf("unexpected upstream call count: %d", calls)
			}
		})
	}
}

func TestResponsesChatBridgeAllowedTools(t *testing.T) {
	for _, tc := range []struct{ mode, upstreamTool string }{
		{"auto", "files__read"},
		{"required", "write"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			a, key := crossTestApp(t, "openai_chat")
			body := `{"model":"alias","input":"start","tools":[{"type":"function","name":"write"},{"type":"function","name":"list"},{"type":"namespace","name":"files","tools":[{"type":"function","name":"read","strict":true}]}],"tool_choice":{"type":"allowed_tools","mode":"` + tc.mode + `","tools":[{"type":"function","name":"read","namespace":"files"},{"type":"function","name":"list"}]}}`
			called := false
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				var upstream struct {
					ToolChoice string `json:"tool_choice"`
					Tools      []struct {
						Function struct {
							Name   string
							Strict bool
						}
					}
				}
				if err := json.NewDecoder(r.Body).Decode(&upstream); err != nil {
					t.Fatal(err)
				}
				if upstream.ToolChoice != tc.mode || len(upstream.Tools) != 2 ||
					upstream.Tools[0].Function.Name != "list" || upstream.Tools[1].Function.Name != "files__read" ||
					!upstream.Tools[1].Function.Strict {
					t.Fatalf("unexpected upstream tool selection: %+v", upstream)
				}
				response := strings.Replace(crossOpenAIResponse, `"name":"read"`, `"name":"`+tc.upstreamTool+`"`, 1)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			output := w.Body.String()
			if !called || w.Code != 200 {
				t.Fatalf("called=%t status=%d body=%s", called, w.Code, output)
			}
			if tc.upstreamTool == "files__read" {
				if !strings.Contains(output, `"namespace":"files"`) || !strings.Contains(output, `"name":"read"`) {
					t.Fatalf("allowed function missing: %s", output)
				}
			} else if !strings.Contains(output, "unauthorized_tool") || strings.Contains(output, `"name":"write"`) {
				t.Fatalf("tool outside the allowlist was not blocked: %s", output)
			}
		})
	}
}

func TestResponsesChatBridgeRejectsStatefulRequestsBeforeSending(t *testing.T) {
	a, key := crossTestApp(t, "openai_chat")
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported stateful request reached upstream")
		return nil, nil
	})}
	for _, tc := range []struct{ field, suffix string }{
		{"previous_response_id", `"store":true,"previous_response_id":"resp_old"`},
		{"store", `"store":"true"`},
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"alias","input":"continue",`+tc.suffix+`}`))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.field) {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestResponsesChatBridgeStatelessStoreAndToolStop(t *testing.T) {
	for _, tc := range []struct {
		name, reason      string
		stream, malformed bool
		wantTool          bool
	}{
		{name: "json", reason: "stop", wantTool: true},
		{name: "sse", reason: "stop", stream: true, wantTool: true},
		{name: "invalid_arguments", reason: "stop", stream: true, malformed: true},
		{name: "token_limit", reason: "length", stream: true},
		{name: "filtered", reason: "content_filter", stream: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, key := crossTestApp(t, "openai_chat")
			body := `{"model":"alias","store":true,"input":[{"role":"user","content":"hello"}],"tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}`
			response, contentType := crossOpenAIResponse, "application/json"
			if tc.stream {
				body = `{"stream":true,` + body[1:]
				response, contentType = chatTestStream(), "text/event-stream"
			}
			response = strings.Replace(response, `"finish_reason":"tool_calls"`, `"finish_reason":"`+tc.reason+`"`, 1)
			if tc.malformed {
				response = strings.Replace(response, `9007199254740993}`, `9007199254740993`, 1)
			}
			calls := 0
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				var chat map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&chat); err != nil || chat["store"] != nil || chat["messages"] == nil {
					t.Fatalf("unexpected Chat request: %v, %v", chat, err)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			out := w.Body.String()
			if calls != 1 || tc.wantTool != strings.Contains(out, `"type":"function_call"`) {
				t.Fatalf("calls=%d status=%d body=%s", calls, w.Code, out)
			}
			if tc.wantTool && (w.Code != http.StatusOK || !strings.Contains(out, `"status":"completed"`) || !strings.Contains(out, "9007199254740993")) {
				t.Fatalf("tool turn did not complete: %s", out)
			}
			if !tc.wantTool && (!strings.Contains(out, "unsupported_upstream_feature") || strings.Contains(out, "event: response.completed")) {
				t.Fatalf("invalid tool turn was not rejected: %s", out)
			}
		})
	}
}

func TestResponsesChatBridgeNamespaceRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, allowed := range []bool{false, true} {
			t.Run(testBoolName(stream, allowed), func(t *testing.T) {
				a, key := crossTestApp(t, "openai_chat")
				body := `{"model":"alias","input":[{"role":"user","content":"start"},{"type":"function_call","namespace":"files","name":"read","call_id":"old","arguments":"{}"},{"type":"function_call_output","call_id":"old","output":"done"}],"tools":[{"type":"namespace","name":"files","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}],"tool_choice":{"type":"function","namespace":"files","name":"read"}}`
				name := "files__read"
				if !allowed {
					name = "other__read"
				}
				response := strings.Replace(crossOpenAIResponse, `"name":"read"`, `"name":"`+name+`"`, 1)
				kind := "application/json"
				if stream {
					body = `{"stream":true,` + body[1:]
					kind = "text/event-stream"
					response = strings.Replace(chatTestStream(), `"name":"re"`, `"name":"`+name+`"`, 1)
					response = strings.Replace(response, `"name":"ad",`, "", 1)
				}
				a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Count(string(raw), `"name":"files__read"`) != 3 || strings.Contains(string(raw), `"namespace"`) {
						t.Fatalf("namespace declaration/history/choice not mapped: %s", raw)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(response))}, nil
				})}
				r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+key)
				w := httptest.NewRecorder()
				a.routes().ServeHTTP(w, r)
				output := w.Body.String()
				if w.Code != http.StatusOK || !strings.Contains(output, "hello") || strings.Contains(output, "9007199254740993") != allowed {
					t.Fatalf("namespace authorization failed: status=%d body=%s", w.Code, output)
				}
				if allowed && (!strings.Contains(output, `"namespace":"files"`) || !strings.Contains(output, `"name":"read"`) || strings.Contains(output, "files__read")) {
					t.Fatalf("namespace not restored: %s", output)
				}
				if !allowed && !strings.Contains(output, "unauthorized_tool") {
					t.Fatalf("undeclared namespace accepted: %s", output)
				}
			})
		}
	}
}

func TestResponsesChatBridgeHostedTools(t *testing.T) {
	for _, choice := range []string{`"auto"`, `"required"`, `{"type":"web_search"}`, `{"type":"function","name":"read"}`} {
		for _, functions := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(choice+"/"+testBoolName(stream, functions), func(t *testing.T) {
					a, key := crossTestApp(t, "openai_chat")
					tools := `{"type":"web_search","filters":{"allowed_domains":["example.com"]}},{"type":"image_generation"}`
					if functions {
						tools += `,{"type":"function","name":"read","parameters":{"type":"object"}}`
					}
					body := `{"model":"alias","tools":[` + tools + `],"tool_choice":` + choice + `,"input":[{"role":"user","content":"hello"},{"type":"web_search_call","id":"ws_old","status":"completed"},{"role":"assistant","content":"previous answer"},{"role":"user","content":"continue"}]}`
					response, contentType := crossOpenAIResponse, "application/json"
					if stream {
						body = `{"stream":true,` + body[1:]
						response, contentType = chatTestStream(), "text/event-stream"
					}
					calls := 0
					if !functions {
						// Keep this fixture focused on declarations and history conversion.
						response = strings.Replace(response, `"name":"read"`, `"name":"undeclared"`, 1)
					}
					a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						calls++
						raw, err := io.ReadAll(r.Body)
						if err != nil {
							t.Fatal(err)
						}
						var request map[string]any
						if err := json.Unmarshal(raw, &request); err != nil {
							t.Fatal(err)
						}
						if strings.Contains(string(raw), "web_search") || strings.Contains(string(raw), "image_generation") || strings.Contains(string(raw), "ws_old") {
							t.Fatalf("hosted tools leaked upstream: %s", raw)
						}
						if (request["tools"] != nil) != functions || (request["tool_choice"] != nil) != functions {
							t.Fatalf("invalid filtered declarations or tool choice: %s", raw)
						}
						if functions && choice == `{"type":"web_search"}` && request["tool_choice"] != "auto" {
							t.Fatalf("hosted tool choice was not reset: %s", raw)
						}
						if len(request["messages"].([]any)) != 3 || !strings.Contains(string(raw), "previous answer") {
							t.Fatalf("ordinary history lost: %s", raw)
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
					})}
					r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
					r.Header.Set("Authorization", "Bearer "+key)
					w := httptest.NewRecorder()
					a.routes().ServeHTTP(w, r)
					if calls != 1 || w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "hello") {
						t.Fatalf("calls=%d status=%d body=%s", calls, w.Code, w.Body.String())
					}
					if functions && !strings.Contains(w.Body.String(), "9007199254740993") {
						t.Fatalf("declared function blocked: %s", w.Body.String())
					}
					if !functions && !strings.Contains(w.Body.String(), "unauthorized_tool") {
						t.Fatalf("undeclared function authorized: %s", w.Body.String())
					}
				})
			}
		}
	}
}

func TestResponsesChatBridgeCodexCompatibility(t *testing.T) {
	const body = `{
		"model":"alias",
		"previous_response_id":null,
		"metadata":{"marker":"PRIVATE_METADATA","tools":[{"type":"function","name":"undeclared"}]},
		"instructions":"be concise",
		"input":[
			{"role":"developer","content":"follow context"},
			"start",
			{"type":"input_text","text":"standalone"},
			{"type":"reasoning","id":"rs_old","summary":[],"encrypted_content":"OPAQUE_REASONING"},
			{"type":"function_call","call_id":"call_a","name":"read","arguments":"{}"},
			{"type":"function_call","call_id":"call_b","name":"read","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_a","output":[{"type":"input_text","text":"image result"},{"type":"input_image","image_url":{"url":"https://example.com/image.png"}}]},
			{"type":"function_call_output","call_id":"call_b","output":{"value":9007199254740993}},
			{"type":"additional_tools","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]},
			{"type":"additional_tools","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}
		],
		"tool_choice":{"type":"function","function":{"name":"read"}}
	}`
	for _, name := range []string{"read", "undeclared"} {
		t.Run(name, func(t *testing.T) {
			a, key := crossTestApp(t, "openai_chat")
			calls := 0
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				var request struct {
					Messages []struct {
						Role       string
						ToolCallID string `json:"tool_call_id"`
						Content    json.RawMessage
					}
					Tools []json.RawMessage
				}
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &request); err != nil {
					t.Fatal(err)
				}
				for _, omitted := range []string{"PRIVATE_METADATA", "OPAQUE_REASONING", "additional_tools", "previous_response_id", "undeclared"} {
					if strings.Contains(string(raw), omitted) {
						t.Fatalf("upstream received %s: %s", omitted, raw)
					}
				}
				messages := request.Messages
				if len(request.Tools) != 1 || len(messages) != 7 ||
					messages[0].Role != "system" || string(messages[0].Content) != `"be concise\n\nfollow context"` ||
					messages[1].Role != "user" || string(messages[1].Content) != `"start"` ||
					messages[2].Role != "user" || string(messages[2].Content) != `"standalone"` ||
					messages[3].Role != "assistant" ||
					messages[4].Role != "tool" || messages[4].ToolCallID != "call_a" || string(messages[4].Content) != `"image result"` ||
					messages[5].Role != "tool" || messages[5].ToolCallID != "call_b" || !strings.Contains(string(messages[5].Content), "9007199254740993") ||
					messages[6].Role != "user" || !strings.Contains(string(messages[6].Content), "https://example.com/image.png") {
					t.Fatalf("unexpected upstream history or tools: %s", raw)
				}
				response := strings.Replace(crossOpenAIResponse, `"name":"read"`, `"name":"`+name+`"`, 1)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			output := w.Body.String()
			if calls != 1 || w.Code != http.StatusOK ||
				strings.Contains(output, `"type":"function_call"`) != (name == "read") ||
				strings.Contains(output, "unauthorized_tool") != (name == "undeclared") {
				t.Fatalf("unexpected authorization or response: calls=%d status=%d body=%s", calls, w.Code, output)
			}
		})
	}
}

func TestResponsesChatBridgeInspectsStreamedTools(t *testing.T) {
	a, key := crossTestApp(t, "openai_chat")
	sink := &toolRecordingSink{block: true}
	a.background.SecurityEvents = sink
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(chatTestStream())),
		}, nil
	})}
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"alias","stream":true,"input":"read a file","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}`))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK || len(sink.calls) != 1 || strings.Contains(w.Body.String(), "9007199254740993") {
		t.Fatalf("unfiltered tool output or unexpected inspection count: status=%d inspected=%d body=%s", w.Code, len(sink.calls), w.Body.String())
	}
}
