package domain

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrInvalidOTPChallenge indicates a challenge that is invalid, expired, consumed, or exhausted.
	ErrInvalidOTPChallenge = errors.New("invalid otp challenge")
	// ErrAdminEmailExists indicates that the requested administrator email is already registered.
	ErrAdminEmailExists = errors.New("administrator email already exists")
	// ErrOTPRequestTooSoon indicates that a recent challenge is still active.
	ErrOTPRequestTooSoon = errors.New("otp request too soon")
)

// AdminOTPChallenge identifies a one-time challenge without exposing its code hash.
type AdminOTPChallenge struct {
	ID          uint64
	AdminID     uint64
	TargetEmail string
	ExpiresAt   time.Time
}

// SuperAdminInput contains the new administrator identity and an optional one-time verification code.
type SuperAdminInput struct {
	Email          string
	Password       string
	OTP            string
	OrganizationID uint64
	Role           string
}

// SuperAdminRepository persists challenges and verifies any supplied code atomically with user creation.
type SuperAdminRepository interface {
	CreateOTPChallenge(context.Context, uint64, string, string, time.Time) (AdminOTPChallenge, error)
	InvalidateOTPChallenge(context.Context, uint64) error
	CreateAdminWithOTP(context.Context, uint64, string, string, string, uint64, string, time.Time) error
}
