package interfaces

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"github.com/gin-gonic/gin"
)

func TestPromptEventCapturesEvidenceBeforeRedactionAndActualOutcome(t *testing.T) {
	a := testApp(t)
	var events []SecurityEvent
	a.background.SecurityEvents = gatewayinfra.RuleSecurityEventSink{Sink: &recordingSecurityEventSink{events: &events}}
	key := apiKey{ID: 7, OrgID: 9}
	router := gin.New()
	router.Use(func(ctx *gin.Context) { ctx.Set(gatewayAPIKeyContext, key); ctx.Next() }, a.gatewaySecurityMiddleware())
	var forwarded []byte
	router.POST("/v1/chat/completions", func(ctx *gin.Context) { forwarded, _ = io.ReadAll(ctx.Request.Body); ctx.Status(http.StatusOK) })
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"api_key=PRIVATE_KEY password=PRIVATE_PASSWORD"}]}`))
	request.Header.Set("X-Request-ID", "evidence-request")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || len(events) != 1 {
		t.Fatalf("status=%d events=%+v", response.Code, events)
	}
	event := events[0]
	if event.HitCount != 2 || len(event.Evidence) != 2 || event.ProcessingResult != "redacted" || event.RuleVersion != 2 || event.RuleScope != "builtin" || event.RuleName == "" {
		t.Fatalf("lost evidence metadata: %+v", event)
	}
	if event.Evidence[0].Path != "messages[0].content" || !json.Valid(forwarded) || strings.Contains(string(forwarded), "PRIVATE_") {
		t.Fatalf("forwarded=%s event=%+v", forwarded, event)
	}
	encoded, _ := json.Marshal(event)
	if strings.Contains(string(encoded), "PRIVATE_") {
		t.Fatalf("unsafe queued event: %s", encoded)
	}
}

func TestDisabledSecurityInspectionSkipsInspectionAndRedaction(t *testing.T) {
	a := testApp(t)
	var events []SecurityEvent
	a.background.SecurityEvents = gatewayinfra.RuleSecurityEventSink{Sink: &recordingSecurityEventSink{events: &events}}
	upstreamSecurityInspectionEnabled := false
	downstreamSecurityInspectionEnabled := false
	key := apiKey{ID: 7, OrgID: 9, SecurityInspectionUpstreamEnabled: &upstreamSecurityInspectionEnabled, SecurityInspectionDownstreamEnabled: &downstreamSecurityInspectionEnabled}
	router := gin.New()
	router.Use(func(ctx *gin.Context) { ctx.Set(gatewayAPIKeyContext, key); ctx.Next() }, a.gatewaySecurityMiddleware())
	var forwarded []byte
	router.POST("/v1/chat/completions", func(ctx *gin.Context) { forwarded, _ = io.ReadAll(ctx.Request.Body); ctx.Status(http.StatusOK) })
	payload := `{"messages":[{"role":"user","content":"api_key=PRIVATE_KEY password=PRIVATE_PASSWORD"}]}`
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(payload))
	request.Header.Set("X-Request-ID", "disabled-inspection")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || len(events) != 0 || string(forwarded) != payload {
		t.Fatalf("disabled key was inspected or modified: status=%d events=%+v forwarded=%s", response.Code, events, forwarded)
	}
	auditRequest := request.Clone(context.Background())
	auditRequest = auditRequest.WithContext(context.WithValue(auditRequest.Context(), gatewayAPIKeyRequestKey{}, key))
	a.inspectModelContent(auditRequest, "model_output", []byte(`{"choices":[{"message":{"content":"<script>PRIVATE_CODE</script>"}}]}`))
	if len(events) != 0 {
		t.Fatalf("disabled key response was inspected: %+v", events)
	}
}

func TestUpstreamDisabledSkipsRequestInspection(t *testing.T) {
	a := testApp(t)
	var events []SecurityEvent
	a.background.SecurityEvents = gatewayinfra.RuleSecurityEventSink{Sink: &recordingSecurityEventSink{events: &events}}
	upstreamEnabled, downstreamEnabled := false, true
	key := apiKey{ID: 7, OrgID: 9, SecurityInspectionUpstreamEnabled: &upstreamEnabled, SecurityInspectionDownstreamEnabled: &downstreamEnabled}
	router := gin.New()
	router.Use(func(ctx *gin.Context) { ctx.Set(gatewayAPIKeyContext, key); ctx.Next() }, a.gatewaySecurityMiddleware())
	var forwarded []byte
	router.POST("/v1/chat/completions", func(ctx *gin.Context) { forwarded, _ = io.ReadAll(ctx.Request.Body); ctx.Status(http.StatusOK) })
	payload := `{"messages":[{"role":"user","content":"api_key=PRIVATE_KEY password=PRIVATE_PASSWORD"}]}`
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(payload))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || len(events) != 0 || string(forwarded) != payload {
		t.Fatalf("disabled upstream key was inspected or modified: status=%d events=%+v forwarded=%s", response.Code, events, forwarded)
	}
}

func TestDownstreamDisabledSkipsResponseInspection(t *testing.T) {
	a := testApp(t)
	var events []SecurityEvent
	a.background.SecurityEvents = gatewayinfra.RuleSecurityEventSink{Sink: &recordingSecurityEventSink{events: &events}}
	upstreamEnabled, downstreamEnabled := true, false
	key := apiKey{ID: 7, OrgID: 9, SecurityInspectionUpstreamEnabled: &upstreamEnabled, SecurityInspectionDownstreamEnabled: &downstreamEnabled}
	router := gin.New()
	router.Use(func(ctx *gin.Context) { ctx.Set(gatewayAPIKeyContext, key); ctx.Next() }, a.gatewaySecurityMiddleware())
	router.POST("/v1/chat/completions", func(ctx *gin.Context) { ctx.Status(http.StatusOK) })
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	auditRequest := request.Clone(context.Background())
	auditRequest = auditRequest.WithContext(context.WithValue(auditRequest.Context(), gatewayAPIKeyRequestKey{}, key))
	a.inspectModelContent(auditRequest, "model_output", []byte(`{"choices":[{"message":{"content":"<script>PRIVATE_CODE</script>"}}]}`))
	if response.Code != 200 || len(events) != 0 {
		t.Fatalf("disabled downstream key response was inspected: status=%d events=%+v", response.Code, events)
	}
}

func TestModelOutputEventPropagatesMinimalEvidence(t *testing.T) {
	a := testApp(t)
	var events []SecurityEvent
	a.background.SecurityEvents = gatewayinfra.RuleSecurityEventSink{Sink: &recordingSecurityEventSink{events: &events}}
	request := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	request = request.WithContext(context.WithValue(request.Context(), gatewayAPIKeyRequestKey{}, apiKey{ID: 7, OrgID: 9}))
	a.inspectModelContent(request, "model_output", []byte(`{"choices":[{"message":{"content":"<script>PRIVATE_CODE</script>"}}]}`))
	if len(events) != 1 || len(events[0].Evidence) != 1 || events[0].Evidence[0].Path != "choices[0].message.content" || events[0].Evidence[0].Snippet != "[omitted] <script [omitted]" {
		t.Fatalf("events=%+v", events)
	}
}

func TestGreetingWithToolDefinitionsDoesNotPublishSecurityEvent(t *testing.T) {
	a := testApp(t)
	var events []SecurityEvent
	a.background.SecurityEvents = gatewayinfra.RuleSecurityEventSink{Sink: &recordingSecurityEventSink{events: &events}}
	router := gin.New()
	router.Use(func(ctx *gin.Context) { ctx.Set(gatewayAPIKeyContext, apiKey{ID: 7, OrgID: 9}); ctx.Next() }, a.gatewaySecurityMiddleware())
	router.POST("/v1/chat/completions", func(ctx *gin.Context) { ctx.Status(http.StatusOK) })
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"你好"}],"tools":[{"type":"function","function":{"name":"ask-user","description":"Accepts sk- and SG. prefixes; postgres:// connection","parameters":{"properties":{"password":{"type":"string"}}}}}]}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || len(events) != 0 {
		t.Fatalf("greeting generated events: status=%d events=%+v", response.Code, events)
	}
}

func TestGreetingWithAwkRegexInRequestHistoryIsForwardedWithoutAuditOrRedaction(t *testing.T) {
	// Mirrors the observed input[1].content[0].text, line 110 incident. Neither
	// the greeting nor the script contains a credential value.
	script := strings.Repeat("历史说明\n", 109) + `awk '/redis:/ { print $0; next } /db:|password:[[:space:]]*/ { line=$0; if (line ~ /password:/) { print line } }'`
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(endpoint, func(t *testing.T) {
			a := testApp(t)
			var events []SecurityEvent
			a.background.SecurityEvents = gatewayinfra.RuleSecurityEventSink{Sink: &recordingSecurityEventSink{events: &events}}
			router := gin.New()
			router.Use(func(ctx *gin.Context) { ctx.Set(gatewayAPIKeyContext, apiKey{ID: 7, OrgID: 9}); ctx.Next() }, a.gatewaySecurityMiddleware())
			var forwarded []byte
			router.POST(endpoint, func(ctx *gin.Context) { forwarded, _ = io.ReadAll(ctx.Request.Body); ctx.Status(http.StatusOK) })
			body := map[string]any{"model": "example-model"}
			if endpoint == "/v1/responses" {
				body["input"] = []any{map[string]any{"role": "system", "content": "说明"}, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": script}}}, map[string]any{"role": "user", "content": "你好"}}
			} else {
				body["messages"] = []any{map[string]any{"role": "assistant", "content": script}, map[string]any{"role": "user", "content": "你好"}}
			}
			payload, _ := json.Marshal(body)
			request := httptest.NewRequest("POST", endpoint, strings.NewReader(string(payload)))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != 200 || len(events) != 0 || string(forwarded) != string(payload) {
				t.Fatalf("false positive or modified history: status=%d events=%+v forwarded=%s", response.Code, events, forwarded)
			}
		})
	}
}

func TestPromptEventsAndRedactionOnlyCoverCurrentInputDespiteToolHistory(t *testing.T) {
	for _, withToolHistory := range []bool{false, true} {
		for _, current := range []string{"你好", "api_key=CURRENT_INPUT_SECRET"} {
			a := testApp(t)
			var events []SecurityEvent
			a.background.SecurityEvents = gatewayinfra.RuleSecurityEventSink{Sink: &recordingSecurityEventSink{events: &events}}
			router := gin.New()
			router.Use(func(ctx *gin.Context) { ctx.Set(gatewayAPIKeyContext, apiKey{ID: 7, OrgID: 9}); ctx.Next() }, a.gatewaySecurityMiddleware())
			var forwarded []byte
			router.POST("/v1/chat/completions", func(ctx *gin.Context) { forwarded, _ = io.ReadAll(ctx.Request.Body); ctx.Status(http.StatusOK) })
			messages := []any{map[string]any{"role": "user", "content": "password=OLD_USER_SECRET"}}
			if withToolHistory {
				messages = append(messages,
					map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "old-call", "type": "function", "function": map[string]any{"name": "lookup", "arguments": `{"password":"OLD_ARGUMENT_SECRET"}`}}}},
					map[string]any{"role": "tool", "tool_call_id": "old-call", "content": "password=OLD_RESULT_SECRET"},
				)
			}
			messages = append(messages, map[string]any{"role": "user", "content": current})
			payload, _ := json.Marshal(map[string]any{"messages": messages})
			request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(payload)))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != 200 || !strings.Contains(string(forwarded), "OLD_USER_SECRET") || withToolHistory && (!strings.Contains(string(forwarded), "OLD_ARGUMENT_SECRET") || !strings.Contains(string(forwarded), "OLD_RESULT_SECRET")) {
				t.Fatalf("history changed: status=%d body=%s", response.Code, forwarded)
			}
			if current == "你好" {
				if len(events) != 0 || string(forwarded) != string(payload) {
					t.Fatalf("history generated events or was redacted: %+v / %s", events, forwarded)
				}
			} else if len(events) != 1 || events[0].RuleID != "prompt_secret" || events[0].RuleVersion != 2 || events[0].HitCount != 1 || events[0].ProcessingResult != "redacted" || strings.Contains(string(forwarded), "CURRENT_INPUT_SECRET") {
				t.Fatalf("current input not independently protected: events=%+v body=%s", events, forwarded)
			}
		}
	}
}

func TestNaturalLanguagePasswordPublishesEventDespiteNormalModelReply(t *testing.T) {
	var events []SecurityEvent
	reply := `{"choices":[{"message":{"role":"assistant","content":"Got it, thanks. What would you like me to help with?"}}]}`
	a, key := setupToolGateway(t, gatewayinfra.RuleSecurityEventSink{Sink: &recordingSecurityEventSink{events: &events}}, reply, "application/json")
	var forwarded []byte
	a.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		forwarded, _ = io.ReadAll(request.Body)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(reply))}, nil
	})
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"password is OLD_HISTORY_SECRET"},{"role":"assistant","content":"Previous reply"},{"role":"user","content":"password is 432432"}]}`))
	request.Header.Set("Authorization", "Bearer "+key)
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Got it, thanks.") || len(events) != 1 {
		t.Fatalf("event missing despite normal reply: status=%d events=%+v body=%s", response.Code, events, response.Body.String())
	}
	event := events[0]
	if event.RuleID != "prompt_secret" || event.HitCount != 1 || event.ProcessingResult != "redacted" || len(event.Evidence) != 1 || event.Evidence[0].Path != "messages[2].content" || event.Evidence[0].Snippet != "password is [Credential hidden]" || event.Evidence[0].StartColumn != 1 || event.Evidence[0].EndColumn != 19 {
		t.Fatalf("incorrect evidence/outcome: %+v", event)
	}
	encoded, _ := json.Marshal(event)
	if strings.Contains(string(encoded), "432432") || strings.Contains(string(encoded), "OLD_HISTORY_SECRET") || strings.Contains(string(forwarded), "432432") || !strings.Contains(string(forwarded), "password is [Password Hide]") || !strings.Contains(string(forwarded), "OLD_HISTORY_SECRET") {
		t.Fatalf("redaction or scope failed: forwarded=%s event=%s", forwarded, encoded)
	}
}
