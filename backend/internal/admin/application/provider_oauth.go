package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/subscription"
)

func configuredProviderModels(names []string, id uint64) []domain.ProviderModel {
	now := time.Now()
	models := make([]domain.ProviderModel, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			models = append(models, domain.ProviderModel{ProviderID: id, ModelName: name, Status: "active", DiscoveredAt: &now})
			seen[name] = true
		}
	}
	return models
}

func (service *ProviderService) WithSubscriptionClient(client subscription.Client) *ProviderService {
	service.oauthClient = client
	return service
}

// StartOAuth binds PKCE state to the current administrator and organization.
func (service *ProviderService) StartOAuth(actor domain.ProviderActor, platform string) (subscription.Authorization, error) {
	if actor.Role != "super_admin" {
		return subscription.Authorization{}, ErrInvalidProvider
	}
	if _, err := subscription.Configuration(platform); err != nil {
		return subscription.Authorization{}, ErrInvalidProvider
	}
	return service.oauthSessions.Start(subscription.Owner(actor.UserID, actor.OrganizationID, actor.Role), platform)
}

func (service *ProviderService) CompleteOAuth(ctx context.Context, actor domain.ProviderActor, platform, sessionID, callback, state, project string, input ProviderInput, target ...uint64) (domain.Provider, error) {
	if actor.Role != "super_admin" {
		return domain.Provider{}, ErrInvalidProvider
	}
	cfg, err := subscription.Configuration(platform)
	if err != nil {
		return domain.Provider{}, ErrInvalidProvider
	}
	input.Platform, input.AccountType, input.APIFormat, input.BaseURL = platform, "oauth", cfg.APIFormat, cfg.BaseURL
	input.APIKey = ""
	input.CreatedByUserID = actor.UserID
	if err := input.Validate(); err != nil {
		return domain.Provider{}, err
	}
	if len(target) > 0 && target[0] > 0 {
		existing, err := service.repository.FindProvider(ctx, target[0])
		if err != nil {
			return domain.Provider{}, err
		}
		if existing.AccountType != "oauth" || existing.Platform != platform {
			return domain.Provider{}, ErrInvalidProvider
		}
	}
	code, verifier, state, err := service.oauthSessions.Consume(subscription.Owner(actor.UserID, actor.OrganizationID, actor.Role), platform, sessionID, callback, state)
	if err != nil {
		return domain.Provider{}, err
	}
	credentials, err := service.oauthClient.Exchange(ctx, platform, code, verifier, state, project)
	if err != nil {
		return domain.Provider{}, err
	}
	input.OAuthCredentials = &credentials
	if len(target) > 0 && target[0] > 0 {
		return service.Update(ctx, target[0], input)
	}
	return service.Create(ctx, input)
}

// credentialLocker holds the database row across refresh-token rotation, including
// across application replicas sharing the same database.
type credentialLocker interface {
	UpdateCredentialLocked(context.Context, uint64, func(domain.Provider, string) (string, error)) (domain.Provider, string, error)
}

// Credential resolves an internal credential and persists rotated OAuth tokens.
func (service *ProviderService) Credential(ctx context.Context, id uint64) (domain.Provider, string, error) {
	provider, ciphertext, err := service.repository.ProviderCredential(ctx, id)
	if err != nil {
		return domain.Provider{}, "", err
	}
	if service.decrypt == nil {
		return domain.Provider{}, "", subscription.ErrToken
	}
	plain, err := service.decrypt(ciphertext)
	if err != nil {
		return domain.Provider{}, "", subscription.ErrToken
	}
	if provider.AccountType != "oauth" {
		return provider, plain, nil
	}
	credentials, err := subscription.Decode(plain, provider.Platform)
	if err != nil {
		return domain.Provider{}, "", err
	}
	if time.Until(credentials.ExpiresAt) > time.Minute {
		return provider, plain, nil
	}
	locker, ok := service.repository.(credentialLocker)
	if !ok {
		return domain.Provider{}, "", errors.New("subscription credential locking is unavailable")
	}
	provider, ciphertext, err = locker.UpdateCredentialLocked(ctx, id, func(current domain.Provider, encrypted string) (string, error) {
		if current.AccountType != "oauth" || current.Platform != provider.Platform {
			return "", subscription.ErrToken
		}
		plain, err := service.decrypt(encrypted)
		if err != nil {
			return "", subscription.ErrToken
		}
		latest, err := subscription.Decode(plain, current.Platform)
		if err != nil {
			return "", err
		}
		if time.Until(latest.ExpiresAt) > time.Minute {
			return encrypted, nil
		}
		next, err := service.oauthClient.Refresh(ctx, current.Platform, latest)
		if err != nil {
			return "", err
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return "", err
		}
		return service.encryptCredential(string(raw))
	})
	if err != nil {
		return domain.Provider{}, "", err
	}
	plain, err = service.decrypt(ciphertext)
	return provider, plain, err
}
