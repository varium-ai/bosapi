package interfaces

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"bosapi/backend/internal/gateway/domain"
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/logging"

	"github.com/coder/websocket"
)

func (a *app) responsesWebSocket(w http.ResponseWriter, r *http.Request, p provider) {
	if !providerCapabilityEnabled(p, "realtime") {
		fail(w, http.StatusForbidden, errorcode.ProviderCapabilityUnavailable)
		return
	}
	markRealtimeUsage(r)
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		fail(w, 426, errorcode.UpgradeRequired)
		return
	}
	target := "wss://chatgpt.com/backend-api/codex/responses"
	headers := make(http.Header)
	if p.Type == "api_key" {
		var err error
		target, err = providerWebSocketEndpoint(p, "/responses")
		if err != nil {
			fail(w, 502, errorcode.UpstreamError)
			return
		}
		headers.Set("Authorization", "Bearer "+p.APIKey)
	} else {
		headers.Set("Authorization", "Bearer "+p.AccessToken)
		headers.Set("ChatGPT-Account-ID", p.AccountID)
		headers.Set("Version", codexVersion)
		headers.Set("Originator", "codex-tui")
		headers.Set("User-Agent", codexUserAgent)
	}
	a.bridgeWebSocket(w, r, target, headers)
}

func providerCapabilityEnabled(p provider, name string) bool {
	if p.Capabilities == nil {
		return true
	}
	value, exists := p.Capabilities[name]
	if !exists {
		return true
	}
	if enabled, ok := value.(bool); ok {
		return enabled
	}
	return true
}

func markRealtimeUsage(r *http.Request) {
	if state := usageState(r); state != nil {
		state.metrics.Realtime = true
	}
}

func observeWebSocketUsage(state *usageMetricsState, payload []byte) {
	if state == nil || len(payload) == 0 {
		return
	}
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return
	}
	observeUsageValue(state, value, "")
}

func observeWebSocketModel(state *usageMetricsState, payload []byte) {
	if state == nil || state.model != "" || len(payload) == 0 {
		return
	}
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return
	}
	state.model = usageModel(value)
}

func observeUsageModelJSON(state *usageMetricsState, payload []byte) {
	if state == nil || state.model != "" || len(payload) == 0 {
		return
	}
	var value map[string]any
	if json.Unmarshal(payload, &value) == nil {
		state.model = usageModel(value)
	}
}

func usageModel(value map[string]any) string {
	if model, ok := value["model"].(string); ok && strings.TrimSpace(model) != "" {
		return strings.TrimSpace(model)
	}
	for _, field := range []string{"response", "session", "data"} {
		if nested, ok := value[field].(map[string]any); ok {
			if model := usageModel(nested); model != "" {
				return model
			}
		}
	}
	return ""
}

func (a *app) bridgeWebSocket(w http.ResponseWriter, r *http.Request, target string, headers http.Header) {
	upstream, _, err := websocket.Dial(r.Context(), target, &websocket.DialOptions{HTTPHeader: headers, HTTPClient: a.client})
	if err != nil {
		logging.Printf(r.Context(), "ERROR", "gateway.websocket.upstream_dial_failed", "target=%s err=%v", target, err)
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	defer upstream.CloseNow()
	downstream, err := websocket.Accept(w, r, nil)
	if err != nil {
		logging.Printf(r.Context(), "ERROR", "gateway.websocket.downstream_accept_failed", "err=%v", err)
		return
	}
	defer downstream.CloseNow()
	upstream.SetReadLimit(2 << 20)
	if !a.toolSecurityBlocks(r) {
		upstream.SetReadLimit(requestBodyLimit(r))
	}
	downstream.SetReadLimit(requestBodyLimit(r))
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	done := make(chan struct{}, 2)
	state := usageState(r)
	requestTools := gatewayinfra.NewToolCallStream(toolCallCaller(r, domain.ToolDirectionRequest), requestToolCalls(r), requestBodyLimit(r))
	sessionAuthorization, _ := r.Context().Value(gatewayToolAuthorizationRequestKey{}).(gatewayinfra.ToolAuthorization)
	activeAuthorization := sessionAuthorization
	responseTools := a.toolDeliveryFilterWithAuthorization(r, func() gatewayinfra.ToolAuthorization { return activeAuthorization }, func(call domain.ToolCall) { requestTools.Remember([]domain.ToolCall{call}) })
	var pendingAuthorization []gatewayinfra.ToolAuthorization
	var responseActive bool
	var toolMu sync.Mutex
	go func() {
		copyFilteredFrames(ctx, upstream, downstream, func(kind websocket.MessageType, payload []byte) [][]byte {
			if kind != websocket.MessageText {
				return [][]byte{payload}
			}
			toolMu.Lock()
			defer toolMu.Unlock()
			var event map[string]json.RawMessage
			_ = json.Unmarshal(payload, &event)
			var eventType string
			_ = json.Unmarshal(event["type"], &eventType)
			if eventType == "error" {
				// Without a response ID, a rejected request cannot safely be
				// paired with a later response. Discard queued grants.
				pendingAuthorization = nil
			}
			if !responseActive && strings.HasPrefix(eventType, "response.") {
				activeAuthorization = sessionAuthorization
				if len(pendingAuthorization) > 0 {
					activeAuthorization = pendingAuthorization[0]
					pendingAuthorization = pendingAuthorization[1:]
				}
				// Bind declarations to one response; later requests cannot expand
				// the policy of an in-flight turn.
				responseActive = true
			}
			observeWebSocketUsage(state, payload)
			observeFirstTokenFrame(r.Context(), payload)
			a.inspectModelContent(r, "model_output", payload)
			output := responseTools.Frame(payload)
			switch eventType {
			case "response.completed", "response.done", "response.failed", "response.incomplete":
				responseActive = false
				activeAuthorization = nil
			}
			return output
		})
		done <- struct{}{}
	}()
	go func() {
		copyFilteredFrames(ctx, downstream, upstream, func(kind websocket.MessageType, payload []byte) [][]byte {
			if kind == websocket.MessageText {
				toolMu.Lock()
				defer toolMu.Unlock()
				calls, parseErr := requestTools.FeedJSON(payload)
				if parseErr != nil {
					requestTools.Reset()
				}
				if parseErr != nil && a.toolSecurityBlocks(r) {
					key, _ := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
					annotated := withToolCallMetadata(r, domain.ToolCallMetadata{Direction: domain.ToolDirectionRequest})
					a.securityEvent(annotated, key, string(errorcode.ToolCallInvalid), "block", http.StatusBadRequest)
					_ = downstream.Write(ctx, websocket.MessageText, []byte(`{"type":"error","error":{"type":"invalid_request_error","code":"invalid_request","message":"Invalid tool call format."}}`))
					return nil
				}
				responseTools.Remember(calls)
				a.observeToolCalls(r, calls, parseErr, domain.ToolDirectionRequest)
				var event map[string]json.RawMessage
				_ = json.Unmarshal(payload, &event)
				var eventType string
				_ = json.Unmarshal(event["type"], &eventType)
				if eventType == "session.update" {
					var session map[string]json.RawMessage
					_ = json.Unmarshal(event["session"], &session)
					if session["tools"] != nil || session["tool_choice"] != nil {
						sessionAuthorization = gatewayinfra.ParseToolAuthorization(event["session"])
					}
				}
				if eventType == "response.create" {
					if len(pendingAuthorization) >= 128 {
						if a.toolSecurityBlocks(r) {
							cancel()
							return nil
						}
						pendingAuthorization = pendingAuthorization[1:]
					}
					authorization := gatewayinfra.ParseToolAuthorization(payload)
					var response map[string]json.RawMessage
					_ = json.Unmarshal(event["response"], &response)
					if event["tools"] == nil && response["tools"] == nil && event["tool_choice"] == nil && response["tool_choice"] == nil {
						authorization = sessionAuthorization
					}
					pendingAuthorization = append(pendingAuthorization, authorization)
				}
				observeWebSocketModel(state, payload)
			}
			return [][]byte{payload}
		})
		done <- struct{}{}
	}()
	<-done
	cancel()
	<-done
	finalizeRealtimeUsage(state)
}

func copyFrames(ctx context.Context, source, destination *websocket.Conn, observe func(websocket.MessageType, []byte) bool) {
	for {
		kind, payload, err := source.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logging.Printf(ctx, "ERROR", "gateway.websocket.frame_read_failed", "err=%v", err)
			}
			return
		}
		if observe != nil && !observe(kind, payload) {
			return
		}
		if err := destination.Write(ctx, kind, payload); err != nil {
			if ctx.Err() == nil {
				logging.Printf(ctx, "ERROR", "gateway.websocket.frame_write_failed", "err=%v", err)
			}
			return
		}
	}
}

func copyFilteredFrames(ctx context.Context, source, destination *websocket.Conn, transform func(websocket.MessageType, []byte) [][]byte) {
	for {
		kind, payload, err := source.Read(ctx)
		if err != nil {
			return
		}
		for _, frame := range transform(kind, payload) {
			if len(frame) == 0 {
				continue
			}
			if err := destination.Write(ctx, kind, frame); err != nil {
				return
			}
		}
	}
}
