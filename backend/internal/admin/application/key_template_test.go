package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"bosapi/backend/internal/admin/domain"
)

type keyTemplateRepositoryFake struct {
	saved              domain.KeyTemplate
	creator, deletedID uint64
	failure            error
}

func (fake *keyTemplateRepositoryFake) List(_ context.Context, creator uint64, _, _ int) ([]domain.KeyTemplate, int64, error) {
	fake.creator = creator
	return nil, 0, fake.failure
}
func (fake *keyTemplateRepositoryFake) Save(_ context.Context, item *domain.KeyTemplate) error {
	fake.saved = *item
	return fake.failure
}
func (fake *keyTemplateRepositoryFake) Delete(_ context.Context, creator, id uint64) error {
	fake.creator, fake.deletedID = creator, id
	return fake.failure
}

func TestKeyTemplateServiceAssignsTrustedCreatorAndPreservesPolicy(t *testing.T) {
	repository := &keyTemplateRepositoryFake{}
	service := NewKeyTemplateService(repository)
	zero := int64(0)
	policy := domain.KeyTemplateSettings{OrganizationID: 3, UserID: 2, QuotaTokens: &zero, IPAllowlist: []string{"192.0.2.0/24"}, IPBlocklist: []string{"::1"}}
	item, err := service.Save(context.Background(), 19, 3, 0, "  Standard  ", policy)
	if err != nil || item.Name != "Standard" || repository.saved.CreatedBy != 19 || repository.saved.Settings.UserID != 2 || *repository.saved.Settings.QuotaTokens != 0 {
		t.Fatalf("unexpected saved template: %+v %v", repository.saved, err)
	}
	if _, _, err := service.List(context.Background(), 19, 1, 20); err != nil || repository.creator != 19 {
		t.Fatal("list lost creator scope")
	}
	if err := service.Delete(context.Background(), 19, 7); err != nil || repository.creator != 19 || repository.deletedID != 7 {
		t.Fatal("delete lost creator scope")
	}
}

func TestKeyTemplateServiceRejectsInvalidConfigurationAndScope(t *testing.T) {
	negative := int64(-1)
	overflow := int64(1 << 32)
	for _, test := range []struct {
		name           string
		creator, scope uint64
		title          string
		change         func(*domain.KeyTemplateSettings)
	}{
		{"missing identity", 0, 3, "valid", func(*domain.KeyTemplateSettings) {}},
		{"cross organization", 19, 4, "valid", func(*domain.KeyTemplateSettings) {}},
		{"empty name", 19, 3, " ", func(*domain.KeyTemplateSettings) {}},
		{"long name", 19, 3, strings.Repeat("a", 101), func(*domain.KeyTemplateSettings) {}},
		{"invalid channel", 19, 3, "valid", func(p *domain.KeyTemplateSettings) { p.AllowedProviderIDs = []uint64{0} }},
		{"negative quota", 19, 3, "valid", func(p *domain.KeyTemplateSettings) { p.DailyQuotaTokens = &negative }},
		{"rate overflow", 19, 3, "valid", func(p *domain.KeyTemplateSettings) { p.RateLimit5H = &overflow }},
		{"invalid ip", 19, 3, "valid", func(p *domain.KeyTemplateSettings) { p.IPBlocklist = []string{"not-an-ip"} }},
		{"too many ips", 19, 3, "valid", func(p *domain.KeyTemplateSettings) { p.IPAllowlist = make([]string, 257) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &keyTemplateRepositoryFake{}
			policy := domain.KeyTemplateSettings{OrganizationID: 3, UserID: 2}
			test.change(&policy)
			_, err := NewKeyTemplateService(repository).Save(context.Background(), test.creator, test.scope, 0, test.title, policy)
			if !errors.Is(err, ErrInvalidKeyTemplate) || repository.saved.CreatedBy != 0 {
				t.Fatalf("invalid template reached persistence: %v", err)
			}
		})
	}
}

func TestKeyTemplateServicePropagatesFailuresAndRejectsInvalidPagination(t *testing.T) {
	failure := errors.New("storage unavailable")
	service := NewKeyTemplateService(&keyTemplateRepositoryFake{failure: failure})
	if _, err := service.Save(context.Background(), 19, 0, 1, "valid", domain.KeyTemplateSettings{OrganizationID: 3, UserID: 2}); !errors.Is(err, failure) {
		t.Fatal("save lost repository error")
	}
	for _, test := range []struct {
		creator    uint64
		page, size int
	}{{0, 1, 20}, {19, 0, 20}, {19, 1, 101}, {19, 1, 0}} {
		if _, _, err := service.List(context.Background(), test.creator, test.page, test.size); !errors.Is(err, ErrInvalidKeyTemplate) {
			t.Fatal("invalid list accepted")
		}
	}
	if err := service.Delete(context.Background(), 0, 1); !errors.Is(err, ErrInvalidKeyTemplate) {
		t.Fatal("missing actor accepted")
	}
	if err := service.Delete(context.Background(), 19, 0); !errors.Is(err, ErrInvalidKeyTemplate) {
		t.Fatal("missing id accepted")
	}
}
