package domain

import (
	"context"
	"time"
)

// RequestLogFilter defines the exact request and tenant scope for a log lookup.
type RequestLogFilter struct {
	RequestID      string
	OrganizationID uint64
}

// RequestLog exposes safe server events associated with one request.
type RequestLog struct {
	ID             uint64    `json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	Level          string    `json:"level"`
	Event          string    `json:"event"`
	Message        string    `json:"message"`
	RequestID      string    `json:"request_id"`
	ClientIP       string    `json:"ip"`
	OrganizationID uint64    `json:"organization_id"`
	APIKeyMasked   string    `json:"api_key_masked"`
}

// RequestLogRepository persists and reads correlated server events.
type RequestLogRepository interface {
	AppendRequestLog(context.Context, RequestLog) error
	ListRequestLogs(context.Context, RequestLogFilter, int, int) ([]RequestLog, bool, error)
}
