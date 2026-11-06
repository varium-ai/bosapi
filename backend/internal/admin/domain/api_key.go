package domain

import (
	"context"

	userdomain "bosapi/backend/internal/user/domain"
)

// AdminKeyFilter contains explicit administrative key query constraints.
type AdminKeyFilter struct {
	OrganizationID uint64
	UserID         uint64
	Status         string
	Search         string
	// OptionsOnly excludes credentials, policy and usage aggregation.
	OptionsOnly bool
}

// AdminKeyRepository defines privileged key operations with a bounded organization scope.
type AdminKeyRepository interface {
	ListAdminKeys(context.Context, AdminKeyFilter, int, int) ([]userdomain.APIKey, int64, error)
	FindAdminKey(context.Context, uint64, uint64) (userdomain.APIKey, error)
	SetAdminKeyStatus(context.Context, uint64, uint64, string) error
	UpdateAdminKey(context.Context, uint64, uint64, map[string]any) error
	CreateAdminKey(context.Context, *userdomain.APIKey) error
	CreateAdminKeys(context.Context, []*userdomain.APIKey) error
	ResetAdminKey(context.Context, uint64, uint64, string, string, string, string) (uint64, error)
	DeleteAdminKeys(context.Context, []uint64, uint64) (int64, error)
	FindUserTOTP(context.Context, uint64) (bool, *string, error)
}
