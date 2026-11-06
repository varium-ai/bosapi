package domain

import (
	"context"
	"time"
)

// LoginLockDecision reports whether a login attempt is currently denied.
type LoginLockDecision struct {
	Locked     bool
	RetryAfter time.Duration
}

// LoginLockStore maintains shared, account-scoped login failure state. Keys are
// normalized account identifiers; store implementations must not retain secret
// material or disclose account existence.
type LoginLockStore interface {
	Check(context.Context, string, time.Time) (LoginLockDecision, error)
	RecordFailure(context.Context, string, time.Time) (LoginLockDecision, error)
	Unlock(context.Context, string) error
}
