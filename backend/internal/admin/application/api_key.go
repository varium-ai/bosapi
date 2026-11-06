package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/totp"
	userdomain "bosapi/backend/internal/user/domain"
)

var (
	ErrTOTPNotEnabled    = errors.New("2fa is not enabled on account")
	ErrInvalidTOTPCode   = errors.New("invalid 2fa code")
	ErrKeyNotRecoverable = errors.New("key plaintext was not saved and cannot be viewed")
)

const MaxAdminKeyImportCount = 500

// KeyCipher defines authenticated encryption and decryption for sensitive credentials.
type KeyCipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(encoded string) (string, error)
}

// AdminKeyService provides organization-scoped administrative key operations.
type AdminKeyService struct {
	repository domain.AdminKeyRepository
	keyCipher  KeyCipher
	authCipher KeyCipher
}

// WithKeyCipher sets the cipher used to encrypt and decrypt API keys.
func (service *AdminKeyService) WithKeyCipher(c KeyCipher) *AdminKeyService {
	service.keyCipher = c
	return service
}

// WithAuthCipher sets the cipher used to decrypt user 2FA secrets.
func (service *AdminKeyService) WithAuthCipher(c KeyCipher) *AdminKeyService {
	service.authCipher = c
	return service
}

// AdminKeyInput contains the owner and policy fields for an administrator-created key.
type AdminKeyInput struct {
	Status                                                  string
	Name                                                    string
	APIKey                                                  string
	UserID                                                  uint64
	OrganizationID                                          uint64
	AllowedProviderIDs                                      []uint64
	AllowedModels                                           []string
	ExpiresAt                                               *time.Time
	QuotaAmount                                             *float64
	QuotaTokens                                             *int64
	DailyQuotaAmount, WeeklyQuotaAmount, MonthlyQuotaAmount *float64
	DailyQuotaTokens, WeeklyQuotaTokens, MonthlyQuotaTokens *int64
	RateLimit5H                                             *int64
	RateLimit1D                                             *int64
	RateLimit7D                                             *int64
	IPAllowlist                                             []string
	IPBlocklist                                             []string
}

// CreatedAdminKey returns a newly generated secret once to the caller.
type CreatedAdminKey struct {
	Name           string `json:"name,omitempty"`
	ID             uint64 `json:"id"`
	Key            string `json:"key"`
	KeyPrefix      string `json:"key_prefix"`
	MaskedKey      string `json:"masked_key"`
	OrganizationID uint64 `json:"organization_id"`
	Status         string `json:"status"`
}

// NewAdminKeyService constructs admin key use cases.
func NewAdminKeyService(repository domain.AdminKeyRepository) *AdminKeyService {
	return &AdminKeyService{repository: repository}
}

// Get returns a single safe key by ID and organization.
func (service *AdminKeyService) Get(ctx context.Context, id, organizationID uint64) (userdomain.SafeAPIKey, error) {
	if id == 0 {
		return userdomain.SafeAPIKey{}, ErrInvalidOrganization
	}
	key, err := service.repository.FindAdminKey(ctx, id, organizationID)
	if err != nil {
		return userdomain.SafeAPIKey{}, err
	}
	return key.Redacted(), nil
}

// BatchDelete permanently removes a batch of keys within an optional organization boundary.
func (service *AdminKeyService) BatchDelete(ctx context.Context, ids []uint64, organizationID uint64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	return service.repository.DeleteAdminKeys(ctx, ids, organizationID)
}

// Reveal decrypts and returns an API key secret after successfully verifying the caller's 2FA TOTP code.
func (service *AdminKeyService) Reveal(ctx context.Context, keyID, callerUserID, callerOrgID uint64, isSuperAdmin bool, code string) (string, error) {
	if keyID == 0 || callerUserID == 0 {
		return "", ErrInvalidOrganization
	}
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return "", ErrInvalidTOTPCode
	}
	enabled, encryptedSecret, err := service.repository.FindUserTOTP(ctx, callerUserID)
	if err != nil || !enabled || encryptedSecret == nil || *encryptedSecret == "" {
		return "", ErrTOTPNotEnabled
	}
	if service.authCipher == nil {
		return "", errors.New("cipher not configured")
	}
	totpSecret, err := service.authCipher.Decrypt(*encryptedSecret)
	if err != nil {
		return "", fmt.Errorf("decrypt totp secret: %w", err)
	}
	if !totp.Validate(code, totpSecret, time.Now().UTC(), 1) {
		return "", ErrInvalidTOTPCode
	}
	orgID := uint64(0)
	if !isSuperAdmin {
		orgID = callerOrgID
	}
	key, err := service.repository.FindAdminKey(ctx, keyID, orgID)
	if err != nil {
		return "", err
	}
	if key.KeyEncrypted == "" {
		return "", ErrKeyNotRecoverable
	}
	if service.keyCipher == nil {
		return "", errors.New("cipher not configured")
	}
	return service.keyCipher.Decrypt(key.KeyEncrypted)
}

// List returns a page of keys constrained to an organization when supplied.
func (service *AdminKeyService) List(ctx context.Context, filter domain.AdminKeyFilter, page, pageSize int) ([]userdomain.SafeAPIKey, int64, error) {
	keys, total, err := service.repository.ListAdminKeys(ctx, filter, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list admin api keys: %w", err)
	}
	items := make([]userdomain.SafeAPIKey, 0, len(keys))
	for _, key := range keys {
		item := key.Redacted()
		if item.KeySuffix == "" && service.keyCipher != nil && key.KeyEncrypted != "" {
			if raw, err := service.keyCipher.Decrypt(key.KeyEncrypted); err == nil {
				digest := sha256.Sum256([]byte(raw))
				if hex.EncodeToString(digest[:]) == key.KeyHash {
					item.KeySuffix = userdomain.SafeKeySuffix(raw, key.KeyPrefix)
				}
			}
		}
		items = append(items, item)
	}
	return items, total, nil
}

// SetStatus changes the state of an organization-scoped key.
func (service *AdminKeyService) SetStatus(ctx context.Context, id, organizationID uint64, status string) error {
	if id == 0 || status != "active" && status != "disabled" {
		return ErrInvalidOrganization
	}
	if err := service.repository.SetAdminKeyStatus(ctx, id, organizationID, status); err != nil {
		return fmt.Errorf("set api key status: %w", err)
	}
	return nil
}

// Update edits key policy while preserving its organization and historical attribution.
func (service *AdminKeyService) Update(ctx context.Context, id, organizationID uint64, updates map[string]any) error {
	if id == 0 || len(updates) == 0 {
		return fmt.Errorf("%w: key id and at least one editable field are required", ErrInvalidOrganization)
	}
	if name, ok := updates["name"].(string); ok {
		if err := userdomain.ValidateKeyName(name); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidOrganization, err)
		}
		updates["name"] = strings.TrimSpace(name)
	}
	for _, field := range []string{"user_id"} {
		if _, supplied := updates[field]; supplied {
			return fmt.Errorf("%w: keys do not have a user owner", ErrInvalidOrganization)
		}
	}
	if organizationID, ok := updates["organization_id"]; ok {
		targetOrganizationID, valid := organizationID.(uint64)
		if !valid || targetOrganizationID == 0 {
			return fmt.Errorf("%w: organization_id must be a positive organization ID", ErrInvalidOrganization)
		}
	}
	providers, _ := updates["allowed_provider_ids"].([]uint64)
	models, _ := updates["allowed_models"].([]string)
	if err := userdomain.ValidateKeyAccessPolicy(providers, models); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidOrganization, err)
	}
	for _, field := range []string{"quota_amount", "daily_quota_amount", "weekly_quota_amount", "monthly_quota_amount"} {
		if value, ok := updates[field].(*float64); ok {
			if err := userdomain.ValidateKeyQuotaAmounts(value); err != nil {
				return fmt.Errorf("%w: %s: %v", ErrInvalidOrganization, field, err)
			}
		}
	}
	for _, field := range []string{"quota_tokens", "daily_quota_tokens", "weekly_quota_tokens", "monthly_quota_tokens"} {
		if value, ok := updates[field].(*int64); ok {
			if err := userdomain.ValidateKeyQuotaTokens(value); err != nil {
				return fmt.Errorf("%w: %s: %v", ErrInvalidOrganization, field, err)
			}
		}
	}
	if allowlist, ok := updates["ip_allowlist"].([]string); ok && len(allowlist) > 256 {
		return fmt.Errorf("%w: ip_allowlist supports at most 256 entries", ErrInvalidOrganization)
	}
	now := time.Now().UTC()
	if value, present := updates["expires_at"]; present {
		expiresAt, ok := value.(*time.Time)
		if !ok {
			return ErrInvalidOrganization
		}
		if expiresAt != nil && !expiresAt.After(now) {
			updates["status"] = "disabled"
		}
	}
	updates["updated_at"] = now
	if err := service.repository.UpdateAdminKey(ctx, id, organizationID, updates); err != nil {
		return fmt.Errorf("update admin api key: %w", err)
	}
	return nil
}

// Create validates scope and persists a key containing only a SHA-256 digest.
func (service *AdminKeyService) Create(ctx context.Context, input AdminKeyInput) (CreatedAdminKey, error) {
	raw, key, err := service.prepare(input)
	if err != nil {
		return CreatedAdminKey{}, err
	}
	if err := service.repository.CreateAdminKey(ctx, key); err != nil {
		return CreatedAdminKey{}, fmt.Errorf("create admin api key: %w", err)
	}
	created := CreatedAdminKey{ID: key.ID, KeyPrefix: key.KeyPrefix, MaskedKey: key.KeyPrefix + "••••••••", OrganizationID: input.OrganizationID, Status: key.Status}
	if input.APIKey == "" {
		created.Key = raw
	}
	return created, nil
}

// CreateBatch validates every generated key before atomically persisting the batch.
func (service *AdminKeyService) CreateBatch(ctx context.Context, inputs []AdminKeyInput) ([]CreatedAdminKey, error) {
	if len(inputs) == 0 || len(inputs) > MaxAdminKeyImportCount {
		return nil, ErrInvalidOrganization
	}
	keys := make([]*userdomain.APIKey, 0, len(inputs))
	created := make([]CreatedAdminKey, 0, len(inputs))
	for _, input := range inputs {
		if input.APIKey != "" {
			return nil, ErrInvalidOrganization
		}
		raw, key, err := service.prepare(input)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
		created = append(created, CreatedAdminKey{Name: key.Name, Key: raw, KeyPrefix: key.KeyPrefix, MaskedKey: key.KeyPrefix + "••••••••", OrganizationID: key.OrganizationID, Status: key.Status})
	}
	if err := service.repository.CreateAdminKeys(ctx, keys); err != nil {
		return nil, fmt.Errorf("create api key batch: %w", err)
	}
	for index, key := range keys {
		created[index].ID = key.ID
	}
	return created, nil
}

// Duplicate preserves the source policy and ownership while issuing an independent credential.
func (service *AdminKeyService) Duplicate(ctx context.Context, id, organizationID uint64) (CreatedAdminKey, error) {
	if id == 0 {
		return CreatedAdminKey{}, ErrInvalidOrganization
	}
	source, err := service.repository.FindAdminKey(ctx, id, organizationID)
	if err != nil {
		return CreatedAdminKey{}, fmt.Errorf("find source api key: %w", err)
	}
	raw, key, err := service.prepare(AdminKeyInput{
		Name: source.Name + " - Copy", OrganizationID: source.OrganizationID, AllowedProviderIDs: source.AllowedProviderIDs, AllowedModels: source.AllowedModels,
		ExpiresAt: source.ExpiresAt, QuotaAmount: source.QuotaAmount, QuotaTokens: source.QuotaTokens,
		DailyQuotaAmount: source.DailyQuotaAmount, WeeklyQuotaAmount: source.WeeklyQuotaAmount, MonthlyQuotaAmount: source.MonthlyQuotaAmount,
		DailyQuotaTokens: source.DailyQuotaTokens, WeeklyQuotaTokens: source.WeeklyQuotaTokens, MonthlyQuotaTokens: source.MonthlyQuotaTokens,
		RateLimit5H: source.RateLimit5H, RateLimit1D: source.RateLimit1D, RateLimit7D: source.RateLimit7D,
		IPAllowlist: source.IPAllowlist, IPBlocklist: source.IPBlocklist,
	})
	if err != nil {
		return CreatedAdminKey{}, err
	}
	key.Status = source.Status
	if err := service.repository.CreateAdminKey(ctx, key); err != nil {
		return CreatedAdminKey{}, fmt.Errorf("duplicate admin api key: %w", err)
	}
	return CreatedAdminKey{ID: key.ID, Key: raw, KeyPrefix: key.KeyPrefix, MaskedKey: key.KeyPrefix + "••••••••", OrganizationID: key.OrganizationID, Status: key.Status}, nil
}

// Import atomically stores a bounded batch of existing API keys without returning their values.
func (service *AdminKeyService) Import(ctx context.Context, inputs []AdminKeyInput) ([]CreatedAdminKey, error) {
	if len(inputs) == 0 || len(inputs) > MaxAdminKeyImportCount {
		return nil, fmt.Errorf("%w: import must contain 1 to %d keys", ErrInvalidOrganization, MaxAdminKeyImportCount)
	}
	keys := make([]*userdomain.APIKey, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for index, input := range inputs {
		input.APIKey = strings.TrimSpace(input.APIKey)
		if input.APIKey == "" {
			return nil, fmt.Errorf("%w: import row %d has no api key", ErrInvalidOrganization, index+1)
		}
		_, key, err := service.prepare(input)
		if err != nil {
			return nil, fmt.Errorf("import row %d: %w", index+1, err)
		}
		if _, exists := seen[key.KeyHash]; exists {
			return nil, fmt.Errorf("%w: duplicate api key in import", ErrInvalidOrganization)
		}
		seen[key.KeyHash] = struct{}{}
		keys = append(keys, key)
	}
	if err := service.repository.CreateAdminKeys(ctx, keys); err != nil {
		return nil, fmt.Errorf("import admin api keys: %w", err)
	}
	created := make([]CreatedAdminKey, 0, len(keys))
	for _, key := range keys {
		created = append(created, CreatedAdminKey{ID: key.ID, KeyPrefix: key.KeyPrefix, MaskedKey: key.KeyPrefix + "••••••••", OrganizationID: key.OrganizationID, Status: key.Status})
	}
	return created, nil
}

func (service *AdminKeyService) prepare(input AdminKeyInput) (string, *userdomain.APIKey, error) {
	status := input.Status
	if status == "" {
		status = "active"
	}
	if status != "active" && status != "disabled" {
		return "", nil, ErrInvalidOrganization
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now()) {
		status = "disabled"
	}
	name := strings.TrimSpace(input.Name)
	if err := userdomain.ValidateKeyName(name); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrInvalidOrganization, err)
	}
	if input.OrganizationID == 0 {
		return "", nil, fmt.Errorf("%w: organization is required", ErrInvalidOrganization)
	}
	if err := userdomain.ValidateKeyAccessPolicy(input.AllowedProviderIDs, input.AllowedModels); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrInvalidOrganization, err)
	}
	if len(input.APIKey) > 4096 || strings.ContainsAny(input.APIKey, "\r\n") {
		return "", nil, fmt.Errorf("%w: supplied API key is invalid", ErrInvalidOrganization)
	}
	if err := userdomain.ValidateKeyQuotaAmounts(input.QuotaAmount, input.DailyQuotaAmount, input.WeeklyQuotaAmount, input.MonthlyQuotaAmount); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrInvalidOrganization, err)
	}
	if err := userdomain.ValidateKeyQuotaTokens(input.QuotaTokens, input.DailyQuotaTokens, input.WeeklyQuotaTokens, input.MonthlyQuotaTokens); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrInvalidOrganization, err)
	}
	if err := userdomain.ValidateKeyRateLimits(input.RateLimit5H, input.RateLimit1D, input.RateLimit7D); err != nil {
		return "", nil, fmt.Errorf("%w: invalid rate limit: %v", ErrInvalidOrganization, err)
	}
	raw := input.APIKey
	if raw == "" {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return "", nil, fmt.Errorf("generate api key: %w", err)
		}
		raw = "sk-gw-" + hex.EncodeToString(secret)
	}
	prefix := raw
	if input.APIKey != "" {
		prefix = safeSuppliedKeyPrefix(raw)
	} else if len(prefix) > 24 {
		prefix = prefix[:24]
	}
	digest := sha256.Sum256([]byte(raw))
	key := &userdomain.APIKey{UserID: input.UserID, OrganizationID: input.OrganizationID, Name: name, KeyHash: hex.EncodeToString(digest[:]), KeyPrefix: prefix, Status: status, SecurityInspectionUpstreamEnabled: true, SecurityInspectionDownstreamEnabled: true, ExpiresAt: input.ExpiresAt, QuotaAmount: input.QuotaAmount, QuotaTokens: input.QuotaTokens, DailyQuotaAmount: input.DailyQuotaAmount, WeeklyQuotaAmount: input.WeeklyQuotaAmount, MonthlyQuotaAmount: input.MonthlyQuotaAmount, DailyQuotaTokens: input.DailyQuotaTokens, WeeklyQuotaTokens: input.WeeklyQuotaTokens, MonthlyQuotaTokens: input.MonthlyQuotaTokens, RateLimit5H: input.RateLimit5H, RateLimit1D: input.RateLimit1D, RateLimit7D: input.RateLimit7D, IPAllowlist: input.IPAllowlist, IPBlocklist: input.IPBlocklist}
	key.KeySuffix = userdomain.SafeKeySuffix(raw, prefix)
	key.UserID = 0
	key.AllowedProviderIDs, key.AllowedModels = input.AllowedProviderIDs, input.AllowedModels
	if service.keyCipher != nil {
		if encrypted, err := service.keyCipher.Encrypt(raw); err == nil {
			key.KeyEncrypted = encrypted
		}
	}
	return raw, key, nil
}

func safeSuppliedKeyPrefix(raw string) string {
	runes := []rune(raw)
	prefixLength := min(4, len(runes))
	if prefixLength == len(runes) {
		prefixLength--
	}
	if prefixLength <= 0 {
		return ""
	}
	return string(runes[:prefixLength])
}

// Reset rotates a key's secret and immediately invalidates its previous digest.
func (service *AdminKeyService) Reset(ctx context.Context, id, organizationID uint64) (CreatedAdminKey, error) {
	if id == 0 {
		return CreatedAdminKey{}, ErrInvalidOrganization
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return CreatedAdminKey{}, fmt.Errorf("generate api key: %w", err)
	}
	raw := "sk-gw-" + hex.EncodeToString(secret)
	prefix := raw[:24]
	digest := sha256.Sum256([]byte(raw))
	var keyEncrypted string
	if service.keyCipher != nil {
		if encrypted, err := service.keyCipher.Encrypt(raw); err == nil {
			keyEncrypted = encrypted
		}
	}
	actualOrganizationID, err := service.repository.ResetAdminKey(ctx, id, organizationID, hex.EncodeToString(digest[:]), prefix, keyEncrypted, userdomain.SafeKeySuffix(raw, prefix))
	if err != nil {
		return CreatedAdminKey{}, fmt.Errorf("reset admin api key: %w", err)
	}
	return CreatedAdminKey{ID: id, Key: raw, KeyPrefix: prefix, MaskedKey: prefix + "••••••••", OrganizationID: actualOrganizationID, Status: "active"}, nil
}
