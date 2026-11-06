package application

import (
	"context"
	"testing"

	"bosapi/backend/internal/admin/domain"
)

func TestToolSecurityPolicyDefaultsPersistsAndValidates(t *testing.T) {
	repo := newMemorySystemSettingRepo()
	service := NewSystemSettingService(repo)
	ctx := context.Background()
	policy, err := service.GetToolSecurityPolicy(ctx)
	if err != nil || policy.Mode != "observe" {
		t.Fatalf("default=%+v err=%v", policy, err)
	}
	actor := SecurityPolicyActor{UserID: 7, Role: "super_admin"}
	for _, mode := range []string{"block", "observe"} {
		if err := service.UpdateToolSecurityPolicy(ctx, domain.ToolSecurityPolicy{Mode: mode}, actor); err != nil {
			t.Fatal(err)
		}
		policy, err = service.GetToolSecurityPolicy(ctx)
		setting := repo.settings[domain.SettingKeyToolSecurityMode]
		if err != nil || policy.Mode != mode || setting.IsSecret || setting.UpdatedBy == nil || *setting.UpdatedBy != 7 {
			t.Fatalf("policy=%+v setting=%+v err=%v", policy, setting, err)
		}
	}
	for _, role := range []string{"admin", "org_admin", "user", ""} {
		if err := service.UpdateToolSecurityPolicy(ctx, domain.ToolSecurityPolicy{Mode: "block"}, SecurityPolicyActor{Role: role}); err != ErrSecurityPolicyForbidden {
			t.Fatalf("role=%q err=%v", role, err)
		}
	}
	for _, mode := range []string{"", "allow", "BLOCK"} {
		if err := service.UpdateToolSecurityPolicy(ctx, domain.ToolSecurityPolicy{Mode: mode}, actor); err != domain.ErrInvalidSecurityPolicy {
			t.Fatalf("mode=%q err=%v", mode, err)
		}
	}
	if policy, _ := service.GetToolSecurityPolicy(ctx); policy.Mode != "observe" {
		t.Fatal("rejected update changed policy")
	}
	repo.settings[domain.SettingKeyToolSecurityMode] = domain.SystemSetting{ValueText: "broken"}
	if _, err := service.GetToolSecurityPolicy(ctx); err == nil {
		t.Fatal("invalid stored policy accepted")
	}
}
