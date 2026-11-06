package application

import (
	"context"
	"testing"

	"bosapi/backend/internal/admin/domain"
)

type mockProviderRepository struct {
	deleteCalled  bool
	deletedID     uint64
	statusCalled  bool
	statusID      uint64
	statusValue   string
	disableCalled bool
	disabledID    uint64
}

func (m *mockProviderRepository) ListProviders(ctx context.Context, filter domain.ProviderFilter, page, pageSize int) ([]domain.Provider, int64, error) {
	return nil, 0, nil
}

func (m *mockProviderRepository) FindProvider(ctx context.Context, id uint64) (domain.Provider, error) {
	return domain.Provider{ID: id, Name: "mock", Status: "active"}, nil
}

func (m *mockProviderRepository) CreateProvider(ctx context.Context, p domain.Provider, cred string) (domain.Provider, error) {
	return p, nil
}

func (m *mockProviderRepository) UpdateProvider(ctx context.Context, id uint64, updates map[string]any, cred string) (domain.Provider, error) {
	return domain.Provider{ID: id}, nil
}

func (m *mockProviderRepository) DisableProvider(ctx context.Context, id uint64) error {
	m.disableCalled = true
	m.disabledID = id
	return nil
}

func (m *mockProviderRepository) DeleteProvider(ctx context.Context, id uint64) error {
	m.deleteCalled = true
	m.deletedID = id
	return nil
}

func (m *mockProviderRepository) SetStatus(ctx context.Context, id uint64, status string) error {
	m.statusCalled = true
	m.statusID = id
	m.statusValue = status
	return nil
}

func (m *mockProviderRepository) ProviderCredential(ctx context.Context, id uint64) (domain.Provider, string, error) {
	return domain.Provider{}, "", nil
}

func (m *mockProviderRepository) AuthorizeProvider(context.Context, uint64, domain.ProviderActor) error {
	return nil
}

func TestProviderCreateURLValidation(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		wantErr bool
	}{
		{name: "http public IP", baseURL: "http://160.30.1.248"},
		{name: "http domain with port", baseURL: "http://example.com:8080/v1"},
		{name: "https", baseURL: "https://example.com/v1"},
		{name: "unsupported scheme", baseURL: "ftp://example.com", wantErr: true},
		{name: "missing scheme", baseURL: "example.com/v1", wantErr: true},
		{name: "user info", baseURL: "http://user:password@example.com", wantErr: true},
		{name: "query", baseURL: "http://example.com?key=secret", wantErr: true},
		{name: "fragment", baseURL: "http://example.com#models", wantErr: true},
		{name: "private IP", baseURL: "http://192.168.1.1", wantErr: true},
		{name: "loopback", baseURL: "http://127.0.0.1", wantErr: true},
		{name: "link local", baseURL: "http://169.254.169.254", wantErr: true},
		{name: "localhost", baseURL: "http://localhost", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := NewProviderService(&mockProviderRepository{}, func(value string) (string, error) {
				return "encrypted", nil
			}, nil, nil)
			provider, err := svc.Create(context.Background(), ProviderInput{
				Name: "ds", Platform: "deepseek", AccountType: "apikey", APIFormat: "openai_chat",
				BaseURL: test.baseURL, APIKey: "test-key", Pricing: map[string]any{"input": 1, "output": 1},
			})
			if test.wantErr {
				if err != ErrInvalidProvider {
					t.Fatalf("Create() error = %v, want %v", err, ErrInvalidProvider)
				}
				return
			}
			if err != nil || provider.BaseURL != test.baseURL {
				t.Fatalf("Create() base URL = %q, error = %v; want %q", provider.BaseURL, err, test.baseURL)
			}
		})
	}
}

func TestProviderDeleteAndSetStatus(t *testing.T) {
	repo := &mockProviderRepository{}
	svc := NewProviderService(repo, nil, nil, nil)
	ctx := context.Background()

	// 1. Delete with id=0 should fail
	if err := svc.Delete(ctx, 0); err != ErrInvalidProvider {
		t.Fatalf("expected ErrInvalidProvider, got %v", err)
	}

	// 2. Valid delete
	if err := svc.Delete(ctx, 42); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.deleteCalled || repo.deletedID != 42 {
		t.Fatalf("expected delete called for id 42, got %v, %v", repo.deleteCalled, repo.deletedID)
	}

	// 3. SetStatus with invalid status
	if err := svc.SetStatus(ctx, 42, "unknown"); err != ErrInvalidProvider {
		t.Fatalf("expected ErrInvalidProvider for invalid status, got %v", err)
	}

	// 4. Valid SetStatus
	if err := svc.SetStatus(ctx, 42, "disabled"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.statusCalled || repo.statusID != 42 || repo.statusValue != "disabled" {
		t.Fatalf("expected set status called, got %v, %v, %v", repo.statusCalled, repo.statusID, repo.statusValue)
	}
}

func TestProviderModelMappingValidation(t *testing.T) {
	repo := &mockProviderRepository{}
	svc := NewProviderService(repo, nil, nil, nil)
	ctx := context.Background()

	validInput := ProviderInput{
		Name:        "Test Provider",
		Platform:    "openai",
		AccountType: "apikey",
		APIFormat:   "openai_chat",
		BaseURL:     "https://api.openai.com/v1",
		ModelMapping: map[string]string{
			"glm-4": "zai-org/GLM-5.3-Flash",
		},
	}
	res, err := svc.Create(ctx, validInput)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if res.ModelMapping["glm-4"] != "zai-org/GLM-5.3-Flash" {
		t.Fatalf("expected mapping glm-4 -> zai-org/GLM-5.3-Flash, got %v", res.ModelMapping)
	}

	// Self mapping error
	invalidInput := validInput
	invalidInput.ModelMapping = map[string]string{
		"glm-4": "glm-4",
	}
	if _, err := svc.Create(ctx, invalidInput); err != ErrInvalidProvider {
		t.Fatalf("expected ErrInvalidProvider for self-mapping, got %v", err)
	}

	// Empty target error
	invalidInput.ModelMapping = map[string]string{
		"glm-4": "",
	}
	if _, err := svc.Create(ctx, invalidInput); err != ErrInvalidProvider {
		t.Fatalf("expected ErrInvalidProvider for empty target, got %v", err)
	}
}
