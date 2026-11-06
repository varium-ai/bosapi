package application

import (
	"context"
	"strings"
	"testing"

	"bosapi/backend/internal/admin/domain"
	"gorm.io/gorm"
)

type organizationRepositoryStub struct {
	organization domain.Organization
}

func TestOrganizationPolicyUpdateRejectsInvalidSyncInputs(t *testing.T) {
	service := NewOrganizationService(organizationRepositoryStub{organization: domain.Organization{ID: 3, Name: "rolle", Status: "active"}})
	for _, updates := range []map[string]any{
		{"allowed_provider_ids": []uint64{0}},
		{"allowed_provider_ids": []uint64{1, 1}},
		{"allowed_provider_ids": "1"},
		{"allowed_models": []string{" "}},
		{"allowed_models": []string{strings.Repeat("a", 192)}},
		{"allowed_models": "model"},
	} {
		if _, err := service.Update(context.Background(), 3, updates); err != ErrInvalidOrganization {
			t.Fatalf("updates=%+v err=%v", updates, err)
		}
	}
}

func (stub organizationRepositoryStub) List(context.Context, domain.OrganizationFilter, int, int) ([]domain.Organization, int64, error) {
	return nil, 0, nil
}
func (stub organizationRepositoryStub) Find(_ context.Context, id uint64) (domain.Organization, error) {
	if id != stub.organization.ID {
		return domain.Organization{}, gorm.ErrRecordNotFound
	}
	return stub.organization, nil
}
func (stub organizationRepositoryStub) Create(context.Context, *domain.Organization) error {
	return nil
}
func (stub organizationRepositoryStub) Update(context.Context, uint64, map[string]any) error {
	return nil
}

func TestOrganizationsHaveNoReservedDefault(t *testing.T) {
	service := NewOrganizationService(organizationRepositoryStub{organization: domain.Organization{ID: 37, Name: "default", Status: "active"}})
	if err := service.Disable(context.Background(), 37); err != nil {
		t.Fatalf("expected an ordinary organization to be disabled, got %v", err)
	}
}
