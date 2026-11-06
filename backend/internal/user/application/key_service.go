package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"bosapi/backend/internal/user/domain"
)

var ErrInvalidKey = errors.New("invalid api key settings")

// KeyRepository provides owner-scoped key persistence operations.
type KeyRepository interface {
	Create(context.Context, *domain.APIKey) error
	List(context.Context, KeyFilter, int, int) ([]domain.APIKey, int64, error)
	FindOwned(context.Context, uint64, uint64, uint64) (domain.APIKey, error)
	UpdateOwned(context.Context, uint64, uint64, uint64, map[string]any) error
}

// KeyFilter scopes a key query to the authenticated identity and optional filters.
type KeyFilter struct {
	UserID         uint64
	OrganizationID uint64
	Status         string
	Search         string
}

// CreateKeyInput contains user-controlled key configuration.
type CreateKeyInput struct {
	AllowedProviderIDs []uint64   `json:"allowed_provider_ids"`
	AllowedModels      []string   `json:"allowed_models"`
	Name               string     `json:"name" binding:"required"`
	ExpiresAt          *time.Time `json:"expires_at"`
	QuotaAmount        *float64   `json:"quota_amount"`
	QuotaTokens        *int64     `json:"quota_tokens"`
	DailyQuotaAmount   *float64   `json:"daily_quota_amount"`
	WeeklyQuotaAmount  *float64   `json:"weekly_quota_amount"`
	MonthlyQuotaAmount *float64   `json:"monthly_quota_amount"`
	DailyQuotaTokens   *int64     `json:"daily_quota_tokens"`
	WeeklyQuotaTokens  *int64     `json:"weekly_quota_tokens"`
	MonthlyQuotaTokens *int64     `json:"monthly_quota_tokens"`
	RateLimit5H        *int64     `json:"rate_limit_5h"`
	RateLimit1D        *int64     `json:"rate_limit_1d"`
	RateLimit7D        *int64     `json:"rate_limit_7d"`
	IPAllowlist        []string   `json:"ip_allowlist"`
	IPBlocklist        []string   `json:"ip_blocklist"`
}

// CreatedKey includes the raw value exactly once after successful creation.
type CreatedKey struct {
	ID             uint64 `json:"id"`
	Key            string `json:"key"`
	KeyPrefix      string `json:"key_prefix"`
	MaskedKey      string `json:"masked_key"`
	OrganizationID uint64 `json:"organization_id"`
	Status         string `json:"status"`
}

// Validate rejects invalid limits and malformed IP allow/deny entries before persistence.
func (input CreateKeyInput) Validate() error {
	if err := domain.ValidateKeyAccessPolicy(input.AllowedProviderIDs, input.AllowedModels); err != nil {
		return ErrInvalidKey
	}
	if err := domain.ValidateKeyName(input.Name); err != nil {
		return ErrInvalidKey
	}
	if err := domain.ValidateKeyQuotaAmounts(input.QuotaAmount, input.DailyQuotaAmount, input.WeeklyQuotaAmount, input.MonthlyQuotaAmount); err != nil {
		return ErrInvalidKey
	}
	if err := domain.ValidateKeyQuotaTokens(input.QuotaTokens, input.DailyQuotaTokens, input.WeeklyQuotaTokens, input.MonthlyQuotaTokens); err != nil {
		return ErrInvalidKey
	}
	if err := domain.ValidateKeyRateLimits(input.RateLimit5H, input.RateLimit1D, input.RateLimit7D); err != nil {
		return ErrInvalidKey
	}
	for _, address := range append(append([]string{}, input.IPAllowlist...), input.IPBlocklist...) {
		if net.ParseIP(address) != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(address); err != nil {
			return ErrInvalidKey
		}
	}
	return nil
}

// KeyCipher defines encryption and decryption for api key values.
type KeyCipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// KeyService owns key generation and user-scoped key operations.
type KeyService struct {
	repository KeyRepository
	keyCipher  KeyCipher
}

// NewKeyService constructs key use cases with injected persistence.
func NewKeyService(repository KeyRepository, _ ...uint64) *KeyService {
	return &KeyService{repository: repository}
}

// WithKeyCipher sets the symmetric cipher used to encrypt key values.
func (service *KeyService) WithKeyCipher(cipher KeyCipher) *KeyService {
	service.keyCipher = cipher
	return service
}

// Create generates a 32-byte secret and stores only its SHA-256 digest.
func (service *KeyService) Create(ctx context.Context, userID, organizationID uint64, input CreateKeyInput) (CreatedKey, error) {
	if organizationID == 0 {
		return CreatedKey{}, ErrInvalidKey
	}
	if err := input.Validate(); err != nil {
		return CreatedKey{}, err
	}
	entropy := make([]byte, 32)
	if _, err := rand.Read(entropy); err != nil {
		return CreatedKey{}, fmt.Errorf("generate api key: %w", err)
	}
	raw := "sk-gw-" + hex.EncodeToString(entropy)
	prefix := raw[:24]
	digest := sha256.Sum256([]byte(raw))
	key := &domain.APIKey{UserID: userID, OrganizationID: organizationID, Name: strings.TrimSpace(input.Name), KeyHash: hex.EncodeToString(digest[:]), KeyPrefix: prefix, Status: "active", SecurityInspectionUpstreamEnabled: true, SecurityInspectionDownstreamEnabled: true, ExpiresAt: input.ExpiresAt, QuotaAmount: input.QuotaAmount, QuotaTokens: input.QuotaTokens, DailyQuotaAmount: input.DailyQuotaAmount, WeeklyQuotaAmount: input.WeeklyQuotaAmount, MonthlyQuotaAmount: input.MonthlyQuotaAmount, DailyQuotaTokens: input.DailyQuotaTokens, WeeklyQuotaTokens: input.WeeklyQuotaTokens, MonthlyQuotaTokens: input.MonthlyQuotaTokens, RateLimit5H: input.RateLimit5H, RateLimit1D: input.RateLimit1D, RateLimit7D: input.RateLimit7D, IPAllowlist: input.IPAllowlist, IPBlocklist: input.IPBlocklist}
	key.KeySuffix = domain.SafeKeySuffix(raw, prefix)
	key.UserID = 0
	key.AllowedProviderIDs, key.AllowedModels = input.AllowedProviderIDs, input.AllowedModels
	if service.keyCipher != nil {
		if encrypted, err := service.keyCipher.Encrypt(raw); err == nil {
			key.KeyEncrypted = encrypted
		}
	}
	if err := service.repository.Create(ctx, key); err != nil {
		return CreatedKey{}, fmt.Errorf("save api key: %w", err)
	}
	return CreatedKey{ID: key.ID, Key: raw, KeyPrefix: prefix, MaskedKey: prefix + "••••••••", OrganizationID: organizationID, Status: key.Status}, nil
}

// List returns redacted keys filtered to the authenticated owner and organization.
func (service *KeyService) List(ctx context.Context, filter KeyFilter, page, pageSize int) ([]domain.SafeAPIKey, int64, error) {
	keys, total, err := service.repository.List(ctx, filter, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list api keys: %w", err)
	}
	items := make([]domain.SafeAPIKey, 0, len(keys))
	for _, key := range keys {
		items = append(items, key.Redacted())
	}
	return items, total, nil
}
