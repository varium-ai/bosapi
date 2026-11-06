package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/subscription"
)

type oauthRepository struct {
	mockProviderRepository
	mu         sync.Mutex
	current    domain.Provider
	ciphertext string
}

func (r *oauthRepository) FindProvider(context.Context, uint64) (domain.Provider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current, nil
}
func (r *oauthRepository) ProviderCredential(context.Context, uint64) (domain.Provider, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current, r.ciphertext, nil
}
func (r *oauthRepository) UpdateCredentialLocked(_ context.Context, _ uint64, update func(domain.Provider, string) (string, error)) (domain.Provider, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next, err := update(r.current, r.ciphertext)
	if err != nil {
		return domain.Provider{}, "", err
	}
	r.ciphertext = next
	return r.current, r.ciphertext, nil
}

type oauthTransport func(*http.Request) (*http.Response, error)

func (f oauthTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSubscriptionConcurrentRefreshRotatesOnceAndKeepsMetadata(t *testing.T) {
	initial := subscription.Credentials{AccessToken: "expired", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Hour)}
	raw, _ := json.Marshal(initial)
	repo := &oauthRepository{current: domain.Provider{ID: 7, Name: "Claude", Platform: "anthropic", AccountType: "oauth", APIFormat: "anthropic_messages", BaseURL: "https://api.anthropic.com"}, ciphertext: "encrypted:" + string(raw)}
	encrypt := func(value string) (string, error) { return "encrypted:" + value, nil }
	decrypt := func(value string) (string, error) {
		if !strings.HasPrefix(value, "encrypted:") {
			return "", errors.New("bad ciphertext")
		}
		return strings.TrimPrefix(value, "encrypted:"), nil
	}
	var calls atomic.Int32
	service := NewProviderService(repo, encrypt, nil, decrypt).WithSubscriptionClient(subscription.Client{HTTP: &http.Client{Transport: oauthTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"renewed","refresh_token":"rotated","expires_in":3600}`))}, nil
	})}})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, plain, err := service.Credential(context.Background(), 7)
			if err != nil {
				t.Error(err)
				return
			}
			credentials, err := subscription.Decode(plain, "anthropic")
			if err != nil || credentials.AccessToken != "renewed" || credentials.RefreshToken != "rotated" || p.Name != "Claude" {
				t.Error("credential or metadata lost")
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 || !strings.HasPrefix(repo.ciphertext, "encrypted:") {
		t.Fatalf("refresh calls=%d", calls.Load())
	}
}

func TestSubscriptionRefreshFailurePreservesStoredCredential(t *testing.T) {
	raw, _ := json.Marshal(subscription.Credentials{AccessToken: "old", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Hour)})
	repo := &oauthRepository{current: domain.Provider{ID: 7, Platform: "anthropic", AccountType: "oauth"}, ciphertext: string(raw)}
	identity := func(s string) (string, error) { return s, nil }
	service := NewProviderService(repo, identity, nil, identity).WithSubscriptionClient(subscription.Client{HTTP: &http.Client{Transport: oauthTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("private-upstream-detail"))}, nil
	})}})
	if _, _, err := service.Credential(context.Background(), 7); err != subscription.ErrToken || strings.Contains(err.Error(), "private") {
		t.Fatalf("error=%v", err)
	}
	if repo.ciphertext != string(raw) {
		t.Fatal("failed refresh overwrote credentials")
	}
}

func TestOAuthProviderMustUseVerifiedCredentialsAndOfficialEndpoint(t *testing.T) {
	service := NewProviderService(&mockProviderRepository{}, func(s string) (string, error) { return s, nil }, nil, nil)
	input := ProviderInput{Name: "Claude", Platform: "anthropic", AccountType: "oauth", APIFormat: "anthropic_messages", BaseURL: "https://api.anthropic.com"}
	if _, err := service.Create(context.Background(), input); err != ErrInvalidProvider {
		t.Fatal("unverified OAuth account accepted")
	}
	input.APIKey = "not-an-oauth-token"
	if err := input.Validate(); err != ErrInvalidProvider {
		t.Fatal("API key accepted as OAuth credential")
	}
	input.APIKey = ""
	input.BaseURL = "https://attacker.example.com"
	if err := input.Validate(); err != ErrInvalidProvider {
		t.Fatal("OAuth credential can escape to a custom endpoint")
	}
}

type deferredOAuthModelRepository struct {
	oauthRepository
	mockProviderModelRepository
	catalog []domain.ProviderModel
}

func (r *deferredOAuthModelRepository) CreateProviderWithModels(_ context.Context, provider domain.Provider, ciphertext string, models []domain.ProviderModel) (domain.Provider, error) {
	provider.ID = 7
	r.current, r.ciphertext, r.catalog = provider, ciphertext, models
	return provider, nil
}

func (r *deferredOAuthModelRepository) UpsertProviderModels(_ context.Context, _ uint64, models []domain.ProviderModel) error {
	r.catalog = models
	return nil
}

func TestOAuthAccountCanBeSavedBeforeModelDiscovery(t *testing.T) {
	for _, failDiscovery := range []bool{false, true} {
		t.Run(map[bool]string{false: "discovered", true: "discovery_failed"}[failDiscovery], func(t *testing.T) {
			repo := &deferredOAuthModelRepository{}
			calls := 0
			service := NewProviderService(repo, func(value string) (string, error) { return "encrypted:" + value, nil }, repo, func(value string) (string, error) { return strings.TrimPrefix(value, "encrypted:"), nil }).WithSubscriptionClient(subscription.Client{HTTP: &http.Client{Transport: oauthTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"saved-access","refresh_token":"saved-refresh","expires_in":3600}`))}, nil
				}
				if r.Method != http.MethodGet || r.URL.String() != "https://api.anthropic.com/v1/models" || r.Header.Get("Authorization") != "Bearer saved-access" {
					t.Fatal("discovery did not use the saved official account")
				}
				if failDiscovery {
					return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"claude-account-model"}]}`))}, nil
			})}})
			actor := domain.ProviderActor{UserID: 7, OrganizationID: 11, Role: "super_admin"}
			auth, err := service.StartOAuth(actor, "anthropic")
			if err != nil {
				t.Fatal(err)
			}
			provider, err := service.CompleteOAuth(context.Background(), actor, "anthropic", auth.SessionID, "code#"+auth.State, auth.State, "", ProviderInput{Name: "Claude", Models: []string{}})
			if err != nil || provider.ID != 7 || calls != 1 || len(repo.catalog) != 0 {
				t.Fatalf("saving without models failed: provider=%+v calls=%d error=%v", provider, calls, err)
			}
			ciphertext := repo.ciphertext
			items, err := service.Discover(context.Background(), provider.ID)
			if failDiscovery {
				if err == nil || len(repo.catalog) != 0 {
					t.Fatal("failed discovery must leave the model catalog empty")
				}
			} else if err != nil || len(items) != 1 || len(repo.catalog) != 1 || repo.catalog[0].ModelName != "claude-account-model" {
				t.Fatalf("discovery did not populate the saved channel: %v %v", items, err)
			}
			if repo.current.ID != provider.ID || repo.ciphertext != ciphertext || !strings.HasPrefix(ciphertext, "encrypted:") {
				t.Fatal("model discovery changed the saved account or credentials")
			}
		})
	}
}
