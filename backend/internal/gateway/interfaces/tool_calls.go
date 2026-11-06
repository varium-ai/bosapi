package interfaces

import (
	"context"
	"errors"
	"net/http"

	"bosapi/backend/internal/gateway/domain"
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
)

type gatewayRuleMatchRequestKey struct{}
type gatewayToolCallsRequestKey struct{}
type gatewayToolAuthorizationRequestKey struct{}
type gatewayToolMetadataRequestKey struct{}

func withSecurityInspection(r *http.Request, result SecurityInspectionResult) *http.Request {
	match := domain.SecurityRuleMatch{RuleID: result.RuleID, RuleName: result.RuleName, Action: result.Action, HitCount: result.HitCount, Evidence: result.Evidence}
	if result.RuleID == "prompt_secret" || result.RuleID == "model_output_code" || result.RuleID == "tool_call_secret" || result.RuleID == "tool_call_code" {
		match.Version, match.Scope = 1, "builtin"
	}
	if result.RuleID == "prompt_secret" {
		match.Version = 2 // Latest user input only; v1 inspected request history.
	}
	return r.WithContext(context.WithValue(r.Context(), gatewayRuleMatchRequestKey{}, match))
}

type gatewaySecurityProcessingRequestKey struct{}

func withSecurityProcessing(r *http.Request, result string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), gatewaySecurityProcessingRequestKey{}, result))
}

func toolCallCaller(r *http.Request, direction domain.ToolDirection) domain.ToolCallContext {
	key, _ := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
	return domain.ToolCallContext{OrganizationID: uint64(key.OrgID), APIKeyID: uint64(key.ID), Direction: direction}
}

func requestToolCalls(r *http.Request) []domain.ToolCall {
	calls, _ := r.Context().Value(gatewayToolCallsRequestKey{}).([]domain.ToolCall)
	return calls
}

func securityInspectionDisabled(r *http.Request, direction domain.ToolDirection) bool {
	key, ok := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
	if !ok {
		return false
	}
	if direction == domain.ToolDirectionResponse {
		return key.SecurityInspectionDownstreamEnabled != nil && !*key.SecurityInspectionDownstreamEnabled
	}
	return key.SecurityInspectionUpstreamEnabled != nil && !*key.SecurityInspectionUpstreamEnabled
}

func withToolCallMetadata(r *http.Request, metadata domain.ToolCallMetadata) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), gatewayToolMetadataRequestKey{}, metadata))
}

// toolCallErrorMapping keeps protocol errors independent of customer endpoints.
// MCP failures use the same safe gateway envelope; no MCP route is registered.
func toolCallErrorMapping(err error) (int, errorcode.Code) {
	var failure *domain.ToolCallParseError
	if errors.As(err, &failure) {
		if failure.Direction == domain.ToolDirectionRequest {
			return http.StatusBadRequest, errorcode.InvalidRequest
		}
		return http.StatusBadGateway, errorcode.UpstreamError
	}
	return http.StatusServiceUnavailable, errorcode.SecurityInspectionUnavailable
}

func (a *app) inspectToolCalls(r *http.Request, calls []domain.ToolCall, enforce bool) (bool, error) {
	allowed := true
	var failure error
	for _, call := range calls {
		pass, err := a.inspectSingleToolCall(r, call, enforce)
		if !pass {
			allowed = false
		}
		if err != nil {
			failure = err
		}
	}
	return allowed, failure
}

func (a *app) inspectSingleToolCall(r *http.Request, call domain.ToolCall, enforce bool) (bool, error) {
	if securityInspectionDisabled(r, call.Direction) {
		return true, nil
	}
	enforce = enforce && a.toolSecurityBlocks(r)
	var result SecurityInspectionResult
	var err error
	if inspector, ok := a.background.SecurityEvents.(domain.ToolCallInspector); ok {
		result, err = inspector.InspectToolCall(r.Context(), call)
	} else if inspector, ok := a.background.SecurityEvents.(SecurityContentInspector); ok {
		result, err = inspector.InspectContent(r.Context(), "prompt", gatewayinfra.ToolCallInspectionPayload(call))
	} else {
		return true, nil
	}
	key := apiKey{ID: int(call.APIKeyID), OrgID: int(call.OrganizationID)}
	annotated := withToolCallMetadata(r, call.Metadata())
	if err != nil {
		action := "observe"
		if enforce {
			action = "block"
		}
		a.securityEvent(annotated, key, string(errorcode.ToolCallInspectionUnavailable), action, http.StatusOK)
		return !enforce, err
	}
	blocked := !result.Allowed || result.Action == "block"
	if blocked && result.RuleID == "" && len(result.Matches) == 0 {
		result.RuleID = "security_policy"
	}
	matches := result.Matches
	if len(matches) == 0 && result.RuleID != "" {
		action := result.Action
		if !result.Allowed {
			action = "block"
		}
		if action == "" {
			action = "observe"
		}
		match := withSecurityInspection(annotated, result).Context().Value(gatewayRuleMatchRequestKey{}).(domain.SecurityRuleMatch)
		match.Action = action
		matches = []domain.SecurityRuleMatch{match}
	}
	for _, match := range matches {
		if match.Action == "block" {
			blocked = true
		}
	}
	processing := "observed"
	if enforce && blocked {
		processing = "blocked"
	}
	for _, match := range matches {
		action := match.Action
		if !enforce {
			action = "observe"
		}
		eventRequest := withSecurityProcessing(annotated.WithContext(context.WithValue(annotated.Context(), gatewayRuleMatchRequestKey{}, match)), processing)
		a.securityEvent(eventRequest, key, match.RuleID, action, http.StatusOK, match.HitCount)
	}
	return !enforce || !blocked, nil
}

func (a *app) toolDeliveryFilter(r *http.Request, remember ...func(domain.ToolCall)) *gatewayinfra.ToolDeliveryFilter {
	return a.toolDeliveryFilterWithAuthorization(r, func() gatewayinfra.ToolAuthorization {
		authorization, _ := r.Context().Value(gatewayToolAuthorizationRequestKey{}).(gatewayinfra.ToolAuthorization)
		return authorization
	}, remember...)
}

func (a *app) toolDeliveryFilterWithAuthorization(r *http.Request, authorization func() gatewayinfra.ToolAuthorization, remember ...func(domain.ToolCall)) *gatewayinfra.ToolDeliveryFilter {
	// Unauthorized tool calls are access control, not regex inspection, and are
	// therefore always audited even when a Key disables security inspection.
	// Global observe mode preserves the original response while auditing.
	block := a.toolSecurityBlocks(r)
	action := "block"
	if !block {
		action = "observe"
	}
	filter := gatewayinfra.NewToolDeliveryFilter(toolCallCaller(r, domain.ToolDirectionResponse), requestToolCalls(r), func(call domain.ToolCall) bool {
		if call.Kind == domain.ToolCallInvocation {
			if !authorization().Allows(call) {
				annotated := withToolCallMetadata(r, call.Metadata())
				a.securityEvent(annotated, apiKey{ID: int(call.APIKeyID), OrgID: int(call.OrganizationID)}, string(errorcode.UnauthorizedTool), action, http.StatusOK)
				if block {
					return false
				}
			}
		}
		allowed, _ := a.inspectSingleToolCall(r, call, call.Kind == domain.ToolCallInvocation)
		if allowed && call.Kind == domain.ToolCallInvocation && len(remember) > 0 {
			remember[0](call)
		}
		return allowed
	}, func(err error) {
		key, _ := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
		annotated := withToolCallMetadata(r, domain.ToolCallMetadata{Direction: domain.ToolDirectionResponse})
		a.securityEvent(annotated, key, string(errorcode.ToolCallInvalid), action, http.StatusOK)
	})
	filter.SetObserveOnly(!block)
	return filter
}

func (a *app) inspectResponseToolCalls(r *http.Request, payload []byte) {
	parser := gatewayinfra.NewToolCallParser(toolCallCaller(r, domain.ToolDirectionResponse), requestToolCalls(r))
	calls, err := parser.Parse(payload)
	a.observeToolCalls(r, calls, err, domain.ToolDirectionResponse)
}

func (a *app) observeToolCalls(r *http.Request, calls []domain.ToolCall, parseErr error, direction domain.ToolDirection) {
	if parseErr != nil {
		status, _ := toolCallErrorMapping(parseErr)
		if !a.toolSecurityBlocks(r) {
			status = http.StatusOK
		}
		key, _ := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
		var failure *domain.ToolCallParseError
		if errors.As(parseErr, &failure) {
			r = withToolCallMetadata(r, domain.ToolCallMetadata{Protocol: failure.Protocol, Direction: failure.Direction})
		}
		a.securityEvent(r, key, string(errorcode.ToolCallInvalid), "observe", status)
	}
	if !securityInspectionDisabled(r, direction) {
		_, _ = a.inspectToolCalls(r, calls, false)
	}
}
