package application

import (
	"context"
	"fmt"

	"bosapi/backend/internal/admin/domain"
)

// GetSecurityInspectionPolicy returns the saved global inspection mode. Missing
// or unreadable configuration keeps the existing inspect behavior.
func (s *SystemSettingService) GetSecurityInspectionPolicy(ctx context.Context) (domain.SecurityInspectionPolicy, error) {
	policy := domain.SecurityInspectionPolicy{Mode: "observe"}
	setting, err := s.repo.Get(ctx, domain.SettingKeySecurityInspectionMode)
	if err != nil {
		return policy, fmt.Errorf("read security inspection policy: %w", err)
	}
	if setting != nil && setting.ValueText != "" {
		policy.Mode = setting.ValueText
	}
	return policy, policy.Validate()
}

// UpdateSecurityInspectionPolicy validates the mode before persisting it. Only
// super administrators are allowed to change the global policy.
func (s *SystemSettingService) UpdateSecurityInspectionPolicy(ctx context.Context, policy domain.SecurityInspectionPolicy, actor SecurityRuleActor) error {
	if actor.Role != "super_admin" {
		return ErrSecurityRuleForbidden
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	return s.repo.Set(ctx, domain.SettingKeySecurityInspectionMode, policy.Mode, false, &actor.UserID)
}
