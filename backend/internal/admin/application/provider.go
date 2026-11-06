package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/subscription"
)

// ErrInvalidProvider identifies invalid upstream provider configuration.
var ErrInvalidProvider = errors.New("invalid provider")

// ProviderInput contains provider configuration and one-time credential input.
type ProviderInput struct {
	Name                string                    `json:"name" binding:"required,max=128"`
	Platform            string                    `json:"platform" binding:"required"`
	AccountType         string                    `json:"account_type" binding:"required"`
	APIFormat           string                    `json:"api_format" binding:"required"`
	BaseURL             string                    `json:"base_url" binding:"required,max=512"`
	APIKey              string                    `json:"api_key"`
	Priority            int                       `json:"priority"`
	ConcurrencyLimit    *uint32                   `json:"concurrency_limit"`
	Schedulable         *bool                     `json:"schedulable"`
	RateLimitRPM        *uint32                   `json:"rate_limit_rpm"`
	Capabilities        map[string]any            `json:"capabilities"`
	Pricing             map[string]any            `json:"pricing"`
	ModelMapping        map[string]string         `json:"model_mapping"`
	Models              []string                  `json:"models"`
	CreatedByUserID     uint64                    `json:"-"`
	OwnerOrganizationID uint64                    `json:"owner_organization_id"`
	Private             bool                      `json:"is_private"`
	OAuthCredentials    *subscription.Credentials `json:"-"`
}

// ProviderService validates and persists upstream account configuration.
type ProviderService struct {
	repository    domain.ProviderRepository
	models        domain.ProviderModelRepository
	metadata      domain.ModelMetadataRepository
	encrypt       func(string) (string, error)
	decrypt       func(string) (string, error)
	oauthSessions subscription.Sessions
	oauthClient   subscription.Client
}

// NewProviderService constructs provider use cases with injected persistence and encryption.
func NewProviderService(repository domain.ProviderRepository, encrypt func(string) (string, error), models domain.ProviderModelRepository, decrypt func(string) (string, error), metadata ...domain.ModelMetadataRepository) *ProviderService {
	svc := &ProviderService{repository: repository, encrypt: encrypt, models: models, decrypt: decrypt}
	if len(metadata) > 0 {
		svc.metadata = metadata[0]
	}
	return svc
}

// List returns provider metadata without credential ciphertext.
func (service *ProviderService) List(ctx context.Context, filter domain.ProviderFilter, page, pageSize int) ([]domain.Provider, int64, error) {
	items, total, err := service.repository.ListProviders(ctx, filter, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list providers: %w", err)
	}
	return items, total, nil
}

// Find returns a provider's safe public metadata.
func (service *ProviderService) Find(ctx context.Context, id uint64) (domain.Provider, error) {
	return service.repository.FindProvider(ctx, id)
}

// Authorize verifies that an administrator may access a provider without exposing hidden providers.
func (service *ProviderService) Authorize(ctx context.Context, id uint64, actor domain.ProviderActor) error {
	if id == 0 || actor.UserID == 0 || actor.Role == "org_admin" && actor.OrganizationID == 0 || actor.Role != "super_admin" && actor.Role != "org_admin" {
		return ErrInvalidProvider
	}
	return service.repository.AuthorizeProvider(ctx, id, actor)
}

// Create validates the account and encrypts credentials before persistence.
func (service *ProviderService) Create(ctx context.Context, input ProviderInput) (domain.Provider, error) {
	if err := input.Validate(); err != nil {
		return domain.Provider{}, err
	}
	secret := input.APIKey
	if input.AccountType == "oauth" {
		if input.OAuthCredentials == nil || !input.OAuthCredentials.Valid(input.Platform) {
			return domain.Provider{}, ErrInvalidProvider
		}
		raw, err := json.Marshal(input.OAuthCredentials)
		if err != nil {
			return domain.Provider{}, err
		}
		secret = string(raw)
	}
	credential, err := service.encryptCredential(secret)
	if err != nil {
		return domain.Provider{}, err
	}
	if input.AccountType == "oauth" {
		if repository, ok := service.repository.(interface {
			CreateProviderWithModels(context.Context, domain.Provider, string, []domain.ProviderModel) (domain.Provider, error)
		}); ok {
			return repository.CreateProviderWithModels(ctx, input.provider(), credential, configuredProviderModels(input.Models, 0))
		}
	}
	created, err := service.repository.CreateProvider(ctx, input.provider(), credential)
	if err != nil {
		return domain.Provider{}, err
	}
	if input.Models != nil && service.models != nil && created.ID > 0 {
		now := time.Now()
		var pmodels []domain.ProviderModel
		for _, m := range input.Models {
			m = strings.TrimSpace(m)
			if m != "" && len(m) <= 191 {
				pmodels = append(pmodels, domain.ProviderModel{
					ProviderID:   created.ID,
					ModelName:    m,
					Status:       "active",
					DiscoveredAt: &now,
				})
			}
		}
		_ = service.models.UpsertProviderModels(ctx, created.ID, pmodels)
	}
	return created, nil
}

// Update applies allowlisted provider fields and replaces credentials only when supplied.
func (service *ProviderService) Update(ctx context.Context, id uint64, input ProviderInput) (domain.Provider, error) {
	if id == 0 || input.Name == "" && input.Platform == "" && input.AccountType == "" && input.APIFormat == "" && input.BaseURL == "" && input.APIKey == "" && input.Schedulable == nil && input.ConcurrencyLimit == nil && input.RateLimitRPM == nil && input.Capabilities == nil && input.Pricing == nil && input.ModelMapping == nil && input.Models == nil && input.Priority == 0 {
		return domain.Provider{}, ErrInvalidProvider
	}
	existing, err := service.repository.FindProvider(ctx, id)
	if err != nil {
		return domain.Provider{}, err
	}
	if input.AccountType != "" && input.AccountType != existing.AccountType && (input.AccountType == "oauth" || existing.AccountType == "oauth") || existing.AccountType == "oauth" && (input.APIKey != "" || input.Platform != "" && input.Platform != existing.Platform) {
		return domain.Provider{}, ErrInvalidProvider
	}
	merged := ProviderInput{Name: existing.Name, Platform: existing.Platform, AccountType: existing.AccountType, APIFormat: existing.APIFormat, BaseURL: existing.BaseURL, Priority: existing.Priority, ConcurrencyLimit: existing.ConcurrencyLimit, Schedulable: &existing.Schedulable, RateLimitRPM: existing.RateLimitRPM, Capabilities: existing.Capabilities, Pricing: existing.Pricing, ModelMapping: existing.ModelMapping}
	if input.Name != "" {
		merged.Name = input.Name
	}
	if input.Platform != "" {
		merged.Platform = input.Platform
	}
	if input.AccountType != "" {
		merged.AccountType = input.AccountType
	}
	if input.APIFormat != "" {
		merged.APIFormat = input.APIFormat
	}
	if input.BaseURL != "" {
		merged.BaseURL = input.BaseURL
	}
	if input.APIKey != "" {
		merged.APIKey = input.APIKey
	}
	if input.Schedulable != nil {
		merged.Schedulable = input.Schedulable
	}
	if input.ConcurrencyLimit != nil {
		merged.ConcurrencyLimit = input.ConcurrencyLimit
	}
	if input.RateLimitRPM != nil {
		merged.RateLimitRPM = input.RateLimitRPM
	}
	if input.Capabilities != nil {
		merged.Capabilities = input.Capabilities
	}
	if input.Pricing != nil {
		if _, hasModels := input.Pricing["models"]; !hasModels && existing.Pricing != nil {
			if existingModels, ok := existing.Pricing["models"]; ok {
				input.Pricing["models"] = existingModels
			}
		}
		merged.Pricing = input.Pricing
	}
	if input.ModelMapping != nil {
		merged.ModelMapping = input.ModelMapping
	}
	if input.Priority != 0 {
		merged.Priority = input.Priority
	}
	merged.Models = input.Models
	if err := merged.Validate(); err != nil {
		return domain.Provider{}, err
	}
	var credential string
	if input.APIKey != "" {
		credential, err = service.encryptCredential(input.APIKey)
		if err != nil {
			return domain.Provider{}, err
		}
	}
	if input.OAuthCredentials != nil {
		if existing.AccountType != "oauth" || !input.OAuthCredentials.Valid(existing.Platform) {
			return domain.Provider{}, ErrInvalidProvider
		}
		raw, err := json.Marshal(input.OAuthCredentials)
		if err != nil {
			return domain.Provider{}, err
		}
		credential, err = service.encryptCredential(string(raw))
		if err != nil {
			return domain.Provider{}, err
		}
	}
	if existing.AccountType == "oauth" && input.Models != nil {
		if repository, ok := service.repository.(interface {
			UpdateProviderWithModels(context.Context, uint64, map[string]any, string, []domain.ProviderModel) (domain.Provider, error)
		}); ok {
			return repository.UpdateProviderWithModels(ctx, id, input.updates(), credential, configuredProviderModels(input.Models, id))
		}
	}
	updated, err := service.repository.UpdateProvider(ctx, id, input.updates(), credential)
	if err != nil {
		return domain.Provider{}, err
	}
	if input.Models != nil && service.models != nil {
		now := time.Now()
		var pmodels []domain.ProviderModel
		for _, m := range input.Models {
			m = strings.TrimSpace(m)
			if m != "" && len(m) <= 191 {
				pmodels = append(pmodels, domain.ProviderModel{
					ProviderID:   id,
					ModelName:    m,
					Status:       "active",
					DiscoveredAt: &now,
				})
			}
		}
		_ = service.models.UpsertProviderModels(ctx, id, pmodels)
	}
	return updated, nil
}

// Disable makes an upstream account unavailable without deleting associated history.
func (service *ProviderService) Disable(ctx context.Context, id uint64) error {
	if id == 0 {
		return ErrInvalidProvider
	}
	return service.repository.DisableProvider(ctx, id)
}

// Delete permanently removes an upstream account and cleans up associated child resources.
func (service *ProviderService) Delete(ctx context.Context, id uint64) error {
	if id == 0 {
		return ErrInvalidProvider
	}
	return service.repository.DeleteProvider(ctx, id)
}

// SetStatus updates upstream account availability status.
func (service *ProviderService) SetStatus(ctx context.Context, id uint64, status string) error {
	if id == 0 || (status != "active" && status != "disabled") {
		return ErrInvalidProvider
	}
	return service.repository.SetStatus(ctx, id, status)
}

// Validate rejects unsupported adapters, malformed URLs, and unsafe credentials.
func (input ProviderInput) Validate() error {
	if input.Private || input.OwnerOrganizationID != 0 {
		return ErrInvalidProvider
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 128 || !validPlatform(input.Platform) || !validAccountType(input.AccountType) || !validFormat(input.APIFormat) || input.Priority < -10000 || input.Priority > 10000 {
		return ErrInvalidProvider
	}
	if input.AccountType == "oauth" {
		cfg, err := subscription.Configuration(input.Platform)
		if err != nil || input.APIFormat != cfg.APIFormat || input.BaseURL != cfg.BaseURL || input.APIKey != "" {
			return ErrInvalidProvider
		}
	}
	for _, model := range input.Models {
		if strings.TrimSpace(model) == "" || len(model) > 191 {
			return ErrInvalidProvider
		}
	}
	parsed, err := url.ParseRequestURI(input.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ErrInvalidProvider
	}
	host := strings.ToLower(parsed.Hostname())
	ip, parseErr := netip.ParseAddr(host)
	if parseErr == nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()) {
		return ErrInvalidProvider
	}
	if net.ParseIP(host) == nil && (host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local")) {
		return ErrInvalidProvider
	}
	if input.APIKey != "" && (len(input.APIKey) > 4096 || strings.ContainsAny(input.APIKey, "\r\n")) {
		return ErrInvalidProvider
	}
	for k, v := range input.ModelMapping {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" || len(k) > 191 || len(v) > 191 || k == v {
			return ErrInvalidProvider
		}
	}
	return nil
}

func (service *ProviderService) encryptCredential(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if service.encrypt == nil {
		return "", errors.New("provider credential encryption is unavailable")
	}
	return service.encrypt(value)
}

func (input ProviderInput) provider() domain.Provider {
	schedulable := true
	if input.Schedulable != nil {
		schedulable = *input.Schedulable
	}
	createdByUserID := input.CreatedByUserID
	return domain.Provider{Name: strings.TrimSpace(input.Name), Platform: input.Platform, AccountType: input.AccountType, APIFormat: input.APIFormat, BaseURL: input.BaseURL, Status: "active", Priority: input.Priority, ConcurrencyLimit: input.ConcurrencyLimit, Schedulable: schedulable, RateLimitRPM: input.RateLimitRPM, Capabilities: input.Capabilities, Pricing: input.Pricing, ModelMapping: input.ModelMapping, CreatedByUserID: &createdByUserID}
}

func (input ProviderInput) updates() map[string]any {
	updates := make(map[string]any)
	if input.Name != "" {
		updates["name"] = strings.TrimSpace(input.Name)
	}
	if input.Platform != "" {
		updates["platform"] = input.Platform
	}
	if input.AccountType != "" {
		updates["account_type"] = input.AccountType
	}
	if input.APIFormat != "" {
		updates["api_format"] = input.APIFormat
	}
	if input.BaseURL != "" {
		updates["base_url"] = input.BaseURL
	}
	if input.Schedulable != nil {
		updates["schedulable"] = *input.Schedulable
		if *input.Schedulable {
			updates["status"] = "active"
		} else {
			updates["status"] = "disabled"
		}
	}
	if input.Priority != 0 {
		updates["priority"] = input.Priority
	}
	if input.ConcurrencyLimit != nil {
		updates["concurrency_limit"] = *input.ConcurrencyLimit
	}
	if input.RateLimitRPM != nil {
		updates["rate_limit_rpm"] = *input.RateLimitRPM
	}
	if input.Capabilities != nil {
		updates["capabilities_json"] = input.Capabilities
	}
	if input.Pricing != nil {
		updates["pricing_json"] = input.Pricing
	}
	if input.ModelMapping != nil {
		updates["model_mapping_json"] = input.ModelMapping
	}
	return updates
}

func validPlatform(value string) bool {
	switch value {
	case "anthropic", "openai", "gemini", "antigravity", "grok", "kimi", "zhipu", "deepseek", "minimax", "opencode_go", "composite":
		return true
	}
	return false
}
func validAccountType(value string) bool {
	return value == "apikey" || value == "oauth" || value == "upstream"
}
func validFormat(value string) bool {
	switch value {
	case "openai_chat", "openai_responses", "anthropic_messages", "gemini", "realtime", "custom":
		return true
	}
	return false
}
