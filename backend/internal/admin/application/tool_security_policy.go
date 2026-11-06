package application

import (
	"context"
	"errors"

	"bosapi/backend/internal/admin/domain"
)

// SecurityPolicyActor identifies the administrator changing the built-in policy.
type SecurityPolicyActor struct {
	UserID, OrganizationID uint64
	Role                   string
}

var ErrSecurityPolicyForbidden = errors.New("security policy change is forbidden")

func (s *SystemSettingService) GetToolSecurityPolicy(ctx context.Context) (domain.ToolSecurityPolicy, error) {
	policy := domain.ToolSecurityPolicy{Mode: "observe"}
	setting, err := s.repo.Get(ctx, domain.SettingKeyToolSecurityMode)
	if err != nil {
		return policy, err
	}
	if setting != nil {
		policy.Mode = setting.ValueText
	}
	return policy, policy.Validate()
}

func (s *SystemSettingService) UpdateToolSecurityPolicy(ctx context.Context, policy domain.ToolSecurityPolicy, actor SecurityPolicyActor) error {
	if actor.Role != "super_admin" {
		return ErrSecurityPolicyForbidden
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	return s.repo.Set(ctx, domain.SettingKeyToolSecurityMode, policy.Mode, false, &actor.UserID)
}
