package application

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"bosapi/backend/internal/admin/domain"
	userdomain "bosapi/backend/internal/user/domain"
)

// ErrInvalidKeyTemplate identifies malformed reusable policy without exposing private templates.
var ErrInvalidKeyTemplate = errors.New("invalid key template")

// KeyTemplateService validates configuration while enforcing the authenticated creator's scope.
type KeyTemplateService struct{ repository domain.KeyTemplateRepository }

// NewKeyTemplateService keeps template persistence behind its creator-scoped domain port.
func NewKeyTemplateService(repository domain.KeyTemplateRepository) *KeyTemplateService {
	return &KeyTemplateService{repository: repository}
}

// List only exposes the caller's templates and bounds pagination.
func (service *KeyTemplateService) List(ctx context.Context, creator uint64, page, size int) ([]domain.KeyTemplate, int64, error) {
	if creator == 0 || page < 1 || size < 1 || size > 100 {
		return nil, 0, ErrInvalidKeyTemplate
	}
	return service.repository.List(ctx, creator, page, size)
}

// Save assigns ownership from trusted identity instead of accepting a client-supplied creator.
func (service *KeyTemplateService) Save(ctx context.Context, creator, organizationID, id uint64, name string, settings domain.KeyTemplateSettings) (domain.KeyTemplate, error) {
	item := domain.KeyTemplate{ID: id, CreatedBy: creator, Name: strings.TrimSpace(name), Settings: settings}
	if creator == 0 || organizationID != 0 && settings.OrganizationID != organizationID {
		return domain.KeyTemplate{}, ErrInvalidKeyTemplate
	}
	if err := validateKeyTemplate(item); err != nil {
		return domain.KeyTemplate{}, err
	}
	if err := service.repository.Save(ctx, &item); err != nil {
		return domain.KeyTemplate{}, fmt.Errorf("save key template: %w", err)
	}
	return item, nil
}

// Delete preserves creator isolation even for platform administrators.
func (service *KeyTemplateService) Delete(ctx context.Context, creator, id uint64) error {
	if creator == 0 || id == 0 {
		return ErrInvalidKeyTemplate
	}
	return service.repository.Delete(ctx, creator, id)
}

func validateKeyTemplate(item domain.KeyTemplate) error {
	policy := item.Settings
	if userdomain.ValidateKeyName(item.Name) != nil || policy.OrganizationID == 0 {
		return ErrInvalidKeyTemplate
	}
	if err := userdomain.ValidateKeyAccessPolicy(policy.AllowedProviderIDs, policy.AllowedModels); err != nil {
		return ErrInvalidKeyTemplate
	}
	if err := userdomain.ValidateKeyQuotaAmounts(policy.QuotaAmount, policy.DailyQuotaAmount, policy.WeeklyQuotaAmount, policy.MonthlyQuotaAmount); err != nil {
		return ErrInvalidKeyTemplate
	}
	if err := userdomain.ValidateKeyQuotaTokens(policy.QuotaTokens, policy.DailyQuotaTokens, policy.WeeklyQuotaTokens, policy.MonthlyQuotaTokens); err != nil {
		return ErrInvalidKeyTemplate
	}
	if err := userdomain.ValidateKeyRateLimits(policy.RateLimit5H, policy.RateLimit1D, policy.RateLimit7D); err != nil {
		return ErrInvalidKeyTemplate
	}
	for _, list := range [][]string{policy.IPAllowlist, policy.IPBlocklist} {
		if len(list) > 256 {
			return ErrInvalidKeyTemplate
		}
		for _, value := range list {
			if net.ParseIP(value) == nil {
				if _, _, err := net.ParseCIDR(value); err != nil {
					return ErrInvalidKeyTemplate
				}
			}
		}
	}
	return nil
}
