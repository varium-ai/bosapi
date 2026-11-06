package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrAdminUserForbidden            = errors.New("only super administrators can change a user's organization")
	ErrInvalidUserOrganization       = errors.New("invalid user organization change")
	ErrAdminUserNotFound             = errors.New("user not found")
	ErrTargetOrganizationUnavailable = errors.New("target organization is missing or inactive")
)

// AdminUserFilter bounds administrative user listing.
type AdminUserFilter struct {
	OrganizationID uint64
	Role           string
	Status         string
	Search         string
}

// AdminUser contains identity metadata without password or token material.
type AdminUser struct {
	ID               uint64     `json:"id"`
	OrganizationID   uint64     `json:"organization_id,omitempty"`
	Email            string     `json:"email"`
	Role             string     `json:"role"`
	Status           string     `json:"status"`
	TOTPEnabled      bool       `json:"totp_enabled"`
	LoginLocked      bool       `json:"login_locked,omitempty"`
	LoginLockedUntil *time.Time `json:"login_locked_until,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type AdminLoginRecord struct {
	ID        uint64    `json:"id"`
	ClientIP  string    `json:"client_ip"`
	Success   bool      `json:"success"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// AdminUserRepository defines safe administrative user reads and updates.
type AdminUserRepository interface {
	ListAdminUsers(context.Context, AdminUserFilter, int, int) ([]AdminUser, int64, error)
	FindByID(context.Context, uint64) (AdminUser, error)
	UpdatePassword(context.Context, uint64, string) error
	ResetMFA(context.Context, uint64) error
	UpdateRole(context.Context, uint64, string, ...uint64) error
	UpdateStatus(context.Context, uint64, string) error
	UpdateOrganization(context.Context, uint64, uint64) error
	ListLoginRecords(context.Context, uint64, int, int) ([]AdminLoginRecord, int64, error)
}
