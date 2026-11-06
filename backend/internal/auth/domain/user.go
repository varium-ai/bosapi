package domain

import "time"

// User is an authenticated portal identity with no persistence-specific fields.
type User struct {
	ID                  uint64
	Email               string
	PasswordHash        string
	OrganizationID      uint64
	Role                string
	Status              string
	TOTPSecretEncrypted *string
	TOTPEnabled         bool
	TOTPEnabledAt       *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// RefreshSession contains a digest of a refresh token rather than its secret value.
type RefreshSession struct {
	ID        uint64
	UserID    uint64
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

// Identity is the safe user profile returned to portal clients.
type Identity struct {
	ID             uint64     `json:"id"`
	Email          string     `json:"email"`
	OrganizationID uint64     `json:"organization_id,omitempty"`
	Role           string     `json:"role"`
	Status         string     `json:"status"`
	TOTPEnabled    bool       `json:"totp_enabled"`
	TOTPEnabledAt  *time.Time `json:"totp_enabled_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// MFAStatus represents the multi-factor authentication status for an identity.
type MFAStatus struct {
	TOTPEnabled   bool       `json:"totp_enabled"`
	TOTPEnabledAt *time.Time `json:"totp_enabled_at,omitempty"`
}

// MFAStatus returns safe MFA state details for serialization.
func (user User) MFAStatus() MFAStatus {
	return MFAStatus{
		TOTPEnabled:   user.TOTPEnabled,
		TOTPEnabledAt: user.TOTPEnabledAt,
	}
}

// PublicIdentity removes all credential material before a user is serialized.
func (user User) PublicIdentity() Identity {
	return Identity{
		ID:             user.ID,
		Email:          user.Email,
		OrganizationID: user.OrganizationID,
		Role:           user.Role,
		Status:         user.Status,
		TOTPEnabled:    user.TOTPEnabled,
		TOTPEnabledAt:  user.TOTPEnabledAt,
		CreatedAt:      user.CreatedAt,
		UpdatedAt:      user.UpdatedAt,
	}
}
