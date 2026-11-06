package domain

import (
	"context"
	"errors"
	"strings"
	"time"

	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/securityevidence"
)

// SecurityEventFilter contains bounded security event search criteria.
type SecurityEventFilter struct {
	OrganizationID uint64
	APIKeyID       uint64
	RuleID         string
	Action         string
	ToolName       string
	ClientIP       string
	EventType      string
	Severity       string
	MinSeverity    string
}

// SecurityEvent exposes only metadata safe for administrative review.
type SecurityEvent struct {
	EventType       string    `json:"event_type"`
	Severity        string    `json:"severity"`
	ID              uint64    `json:"id"`
	RequestID       string    `json:"request_id"`
	OrganizationID  *uint64   `json:"organization_id"`
	APIKeyID        *uint64   `json:"api_key_id"`
	ClientIP        string    `json:"client_ip"`
	RuleID          string    `json:"rule_id"`
	RuleName        string    `json:"rule_name,omitempty"`
	Action          string    `json:"action"`
	HitCount        uint32    `json:"hit_count"`
	HTTPStatus      *uint16   `json:"http_status"`
	ToolName        string    `json:"tool_name"`
	ToolProtocol    string    `json:"tool_protocol"`
	ToolDirection   string    `json:"tool_direction"`
	CallIDHash      string    `json:"call_id_hash"`
	RuleVersion     uint64    `json:"rule_version"`
	RuleScope       string    `json:"rule_scope"`
	RuleSensitivity string    `json:"rule_sensitivity"`
	CreatedAt       time.Time `json:"created_at"`
}

// SecurityEventRepository defines safe security event reads.
type SecurityEventRepository interface {
	ListSecurityEvents(context.Context, SecurityEventFilter, int, int) ([]SecurityEvent, int64, error)
	GetSecurityEventDetail(context.Context, uint64, uint64) (SecurityEventDetail, error)
}

// SecurityEventReadScope separates each administrator's tenant and global read receipts.
type SecurityEventReadScope struct {
	UserID         uint64
	OrganizationID uint64
}

// SecurityEventReadRepository persists administrator notification state independently of event data.
type SecurityEventReadRepository interface {
	CountUnreadSecurityEvents(context.Context, SecurityEventReadScope) (int64, error)
	MarkSecurityEventsRead(context.Context, SecurityEventReadScope) error
}

// SecurityEventReadState provides the sidebar notification count.
type SecurityEventReadState struct {
	UnreadCount int64 `json:"unread_count"`
}

var ErrSecurityEventNotFound = errors.New("security event not found")

// SecurityEventDetail is only returned by the administrator detail endpoint.
// Lists, key portal responses and webhook envelopes remain metadata-only.
type SecurityEventDetail struct {
	SecurityEvent
	ProcessingResult  string                  `json:"processing_result"`
	EvidenceStatus    string                  `json:"evidence_status"`
	EvidenceCount     uint8                   `json:"evidence_count"`
	EvidenceLimit     int                     `json:"evidence_limit"`
	EvidenceExpiresAt *time.Time              `json:"evidence_expires_at,omitempty"`
	Evidence          []securityevidence.View `json:"evidence"`
}

// ClassifySecurityEvent keeps API, outbox and event history classifications aligned.
func ClassifySecurityEvent(ruleID, action, toolName string) (eventType, severity string) {
	eventType = "other"
	if securityevidence.IsInjectionReason(ruleID) {
		return "injection", "high"
	}
	switch {
	case strings.HasPrefix(ruleID, "authentication_"):
		eventType = "authentication"
	case ruleID == "rate_limit":
		eventType = "rate_limit"
	case ruleID == string(errorcode.QuotaExceeded):
		eventType = "quota"
	case ruleID == string(errorcode.PolicyNotAllowed) || ruleID == string(errorcode.ModelNotAllowed):
		eventType = "access_policy"
	case toolName != "" || strings.HasPrefix(ruleID, "tool_") || ruleID == string(errorcode.UnauthorizedTool):
		eventType = "tool_call"
	case ruleID == "prompt_secret":
		eventType = "sensitive_content"
	case ruleID == "model_output_code":
		eventType = "model_output"
	}
	switch {
	case ruleID == string(errorcode.ToolCallInvalid):
		severity = "info"
	case eventType == "tool_call" || ruleID == "prompt_secret":
		severity = "warning"
	case ruleID == string(errorcode.ModelNotAllowed),
		ruleID == "authentication_key_not_found_or_unusable",
		ruleID == string(errorcode.InvalidProvider),
		ruleID == "authentication_missing_credential",
		ruleID == "model_output_code":
		severity = "info"
	case action == "block":
		severity = "high"
	case action == "observe":
		severity = "warning"
	default:
		severity = "info"
	}
	return
}
func ValidSecurityEventType(value string) bool {
	switch value {
	case "", "authentication", "rate_limit", "quota", "access_policy", "tool_call", "sensitive_content", "model_output", "injection", "other":
		return true
	}
	return false
}
func ValidSecuritySeverity(value string) bool {
	switch value {
	case "", "info", "warning", "high", "critical":
		return true
	}
	return false
}
