package domain

import (
	"context"
	"time"
)

// AdminErrorFilter bounds error dashboard queries with an opaque keyset cursor.
type AdminErrorFilter struct {
	OrganizationID uint64
	APIKeyID       uint64
	ErrorCode      string
	From           time.Time
	To             time.Time
	Before         time.Time
	BeforeID       uint64
}

// AdminError exposes only sanitized failed request metadata.
type AdminError struct {
	ID             uint64    `json:"-"`
	Time           time.Time `json:"time"`
	RequestID      string    `json:"request_id"`
	OrganizationID uint64    `json:"org_id"`
	APIKeyID       uint64    `json:"key_id"`
	HTTPStatus     uint16    `json:"http_status"`
	Outcome        string    `json:"outcome"`
	ErrorCode      string    `json:"error_code"`
	Model          string    `json:"model"`
	Endpoint       string    `json:"endpoint"`
	DurationMillis uint32    `json:"duration_ms"`
}

// AdminErrorRepository defines safe keyset-paginated error reads.
type AdminErrorRepository interface {
	ListAdminErrors(context.Context, AdminErrorFilter, int) ([]AdminError, bool, error)
}
