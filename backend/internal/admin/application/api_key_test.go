package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	userdomain "bosapi/backend/internal/user/domain"
)

type keyRepositoryStub struct {
	key  userdomain.APIKey
	keys []*userdomain.APIKey
}

func (stub *keyRepositoryStub) ListAdminKeys(context.Context, domain.AdminKeyFilter, int, int) ([]userdomain.APIKey, int64, error) {
	return nil, 0, nil
}

func TestCreateAdminKeyValidatesRateLimitStorageRange(t *testing.T) {
	for _, test := range []struct {
		name  string
		limit int64
		valid bool
	}{
		{"minimum", 1, true},
		{"maximum", math.MaxUint32, true},
		{"zero", 0, false},
		{"negative", -1, false},
		{"overflow", math.MaxUint32 + 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &keyRepositoryStub{}
			service := NewAdminKeyService(repository)
			_, err := service.Create(context.Background(), AdminKeyInput{Name: "limited", UserID: 2, OrganizationID: 3, RateLimit5H: &test.limit, RateLimit1D: &test.limit, RateLimit7D: &test.limit})
			if !test.valid {
				if !errors.Is(err, ErrInvalidOrganization) || repository.key.UserID != 0 {
					t.Fatalf("invalid limit persisted: key = %+v, err = %v", repository.key, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("create limited key: %v", err)
			}
			for _, limit := range []*int64{repository.key.RateLimit5H, repository.key.RateLimit1D, repository.key.RateLimit7D} {
				if limit == nil || *limit != test.limit {
					t.Fatalf("persisted limit = %v, want %d", limit, test.limit)
				}
			}
		})
	}
}

func TestCreateAdminKeyDefaultsSecurityInspectionToEnabled(t *testing.T) {
	repository := &keyRepositoryStub{}
	service := NewAdminKeyService(repository)
	if _, err := service.Create(context.Background(), AdminKeyInput{Name: "checked", OrganizationID: 3}); err != nil {
		t.Fatalf("create checked key: %v", err)
	}
	if !repository.key.SecurityInspectionUpstreamEnabled || !repository.key.SecurityInspectionDownstreamEnabled {
		t.Fatalf("security inspection defaults = %+v, want both enabled", repository.key)
	}
}

func TestSetAdminKeyStatusDoesNotAcceptDeletedState(t *testing.T) {
	err := NewAdminKeyService(&keyRepositoryStub{}).SetStatus(context.Background(), 7, 3, "deleted")
	if !errors.Is(err, ErrInvalidOrganization) {
		t.Fatalf("set deleted status error = %v, want invalid status", err)
	}
}

func (stub *keyRepositoryStub) FindAdminKey(context.Context, uint64, uint64) (userdomain.APIKey, error) {
	return userdomain.APIKey{}, nil
}

func (stub *keyRepositoryStub) SetAdminKeyStatus(context.Context, uint64, uint64, string) error {
	return nil
}

func (stub *keyRepositoryStub) UpdateAdminKey(context.Context, uint64, uint64, map[string]any) error {
	return nil
}

func (stub *keyRepositoryStub) CreateAdminKey(_ context.Context, key *userdomain.APIKey) error {
	stub.key = *key
	key.ID = 17
	return nil
}

func (stub *keyRepositoryStub) CreateAdminKeys(_ context.Context, keys []*userdomain.APIKey) error {
	stub.keys = keys
	for index, key := range keys {
		key.ID = uint64(index + 17)
	}
	return nil
}

func (stub *keyRepositoryStub) ResetAdminKey(_ context.Context, id, organizationID uint64, hash, prefix, keyEncrypted, keySuffix string) (uint64, error) {
	stub.key = userdomain.APIKey{ID: id, OrganizationID: organizationID, KeyHash: hash, KeyPrefix: prefix, KeyEncrypted: keyEncrypted, KeySuffix: keySuffix}
	return organizationID, nil
}

func (stub *keyRepositoryStub) DeleteAdminKeys(context.Context, []uint64, uint64) (int64, error) {
	return 0, nil
}

func (stub *keyRepositoryStub) FindUserTOTP(context.Context, uint64) (bool, *string, error) {
	return false, nil, nil
}

func TestCreateUsesExistingAPIKeyVerbatim(t *testing.T) {
	const raw = "vendor-token:\u4efb\u610f\u683c\u5f0f/secret"
	repository := &keyRepositoryStub{}
	service := NewAdminKeyService(repository)
	created, err := service.Create(context.Background(), AdminKeyInput{Name: "imported", APIKey: raw, UserID: 2, OrganizationID: 3})
	if err != nil {
		t.Fatalf("create imported key: %v", err)
	}
	digest := sha256.Sum256([]byte(raw))
	if got := repository.key.KeyHash; got != hex.EncodeToString(digest[:]) {
		t.Fatalf("stored digest = %q, want digest of original key", got)
	}
	if created.Key != "" || created.KeyPrefix != "vend" || created.ID != 17 {
		t.Fatalf("unexpected created key response: %+v", created)
	}
}

func TestImportAdminKeysHashesSecretsAndReturnsNoRawValues(t *testing.T) {
	repository := &keyRepositoryStub{}
	inputs := []AdminKeyInput{
		{Name: "production", APIKey: "provider-key-very-long-001", UserID: 2, OrganizationID: 3},
		{Name: "staging", APIKey: "provider-key-very-long-002", UserID: 2, OrganizationID: 3},
	}
	created, err := NewAdminKeyService(repository).Import(context.Background(), inputs)
	if err != nil {
		t.Fatalf("import keys: %v", err)
	}
	if len(created) != 2 || len(repository.keys) != 2 {
		t.Fatalf("created=%+v stored=%d, want two", created, len(repository.keys))
	}
	for index, input := range inputs {
		digest := sha256.Sum256([]byte(input.APIKey))
		if repository.keys[index].KeyHash != hex.EncodeToString(digest[:]) {
			t.Fatalf("stored key %d does not contain the supplied key digest", index)
		}
		if created[index].Key != "" || created[index].ID != uint64(index+17) {
			t.Fatalf("import response exposes a key or missed its ID: %+v", created[index])
		}
	}
}

func TestImportAdminKeysRejectsDuplicatesBeforePersistence(t *testing.T) {
	repository := &keyRepositoryStub{}
	inputs := []AdminKeyInput{
		{Name: "one", APIKey: "same-key-value", UserID: 2, OrganizationID: 3},
		{Name: "two", APIKey: " same-key-value ", UserID: 2, OrganizationID: 3},
	}
	if _, err := NewAdminKeyService(repository).Import(context.Background(), inputs); !errors.Is(err, ErrInvalidOrganization) {
		t.Fatalf("duplicate import error = %v, want invalid settings", err)
	}
	if len(repository.keys) != 0 {
		t.Fatalf("persisted %d keys for rejected duplicate batch", len(repository.keys))
	}
}

// duplicateKeyRepository records the requested scope to verify tenant isolation.
type duplicateKeyRepository struct {
	keyRepositoryStub
	source    userdomain.APIKey
	scope     uint64
	findError error
}

func (stub *duplicateKeyRepository) FindAdminKey(_ context.Context, _ uint64, scope uint64) (userdomain.APIKey, error) {
	stub.scope = scope
	return stub.source, stub.findError
}

func TestDuplicateAdminKeyPreservesPolicyAndResetsUsage(t *testing.T) {
	limit := int64(100)
	repository := &duplicateKeyRepository{source: userdomain.APIKey{
		ID: 9, Name: "production", UserID: 2, OrganizationID: 3, Status: "disabled",
		AllowedProviderIDs: []uint64{4}, AllowedModels: []string{"model-one"},
		KeyHash: "original", QuotaTokens: &limit, DailyQuotaTokens: &limit, RateLimit5H: &limit,
		IPAllowlist: []string{"127.0.0.1"}, IPBlocklist: []string{"192.0.2.1"}, QuotaUsedTokens: 50,
	}}
	created, err := NewAdminKeyService(repository).Duplicate(context.Background(), 9, 3)
	if err != nil {
		t.Fatal(err)
	}
	key := repository.key
	if repository.scope != 3 || key.Name != "production - Copy" || key.UserID != 0 || key.OrganizationID != 3 || key.Status != "disabled" {
		t.Fatalf("source policy or scope was not preserved: %+v", key)
	}
	if key.QuotaTokens == nil || *key.QuotaTokens != limit || key.DailyQuotaTokens == nil || *key.DailyQuotaTokens != limit || key.RateLimit5H == nil || *key.RateLimit5H != limit || len(key.IPAllowlist) != 1 || len(key.IPBlocklist) != 1 {
		t.Fatalf("source limits were not preserved: %+v", key)
	}
	if len(key.AllowedProviderIDs) != 1 || key.AllowedProviderIDs[0] != 4 || len(key.AllowedModels) != 1 || key.AllowedModels[0] != "model-one" {
		t.Fatal("duplicate lost channel/model restrictions")
	}
	if created.Key == "" || key.KeyHash == "original" || key.QuotaUsedTokens != 0 || key.LastUsedAt != nil {
		t.Fatal("duplicate must have a new credential and fresh usage")
	}
}

func TestDuplicateAdminKeyPropagatesLookupFailure(t *testing.T) {
	failure := errors.New("source unavailable")
	repository := &duplicateKeyRepository{findError: failure}
	_, err := NewAdminKeyService(repository).Duplicate(context.Background(), 9, 3)
	if !errors.Is(err, failure) || repository.key.UserID != 0 {
		t.Fatalf("lookup failure must prevent creation: %v", err)
	}
}

func TestImportAdminKeysPreservesCredentialsAndSharedPolicy(t *testing.T) {
	repository := &keyRepositoryStub{}
	tokens, rate := int64(100), int64(200)
	expires := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	inputs := []AdminKeyInput{
		{Name: "first", APIKey: "first-imported-fixture-secret", UserID: 4, OrganizationID: 3, QuotaTokens: &tokens, DailyQuotaTokens: &tokens, RateLimit5H: &rate, ExpiresAt: &expires, IPAllowlist: []string{"192.0.2.0/24"}},
		{Name: "second", APIKey: "second-imported-fixture-secret", UserID: 4, OrganizationID: 3, QuotaTokens: &tokens, DailyQuotaTokens: &tokens, RateLimit5H: &rate, ExpiresAt: &expires, IPAllowlist: []string{"192.0.2.0/24"}},
	}
	created, err := NewAdminKeyService(repository).Import(context.Background(), inputs)
	if err != nil || len(repository.keys) != 2 {
		t.Fatalf("import failed: %v", err)
	}
	for index, key := range repository.keys {
		digest := sha256.Sum256([]byte(inputs[index].APIKey))
		if key.Name != inputs[index].Name || key.KeyHash != hex.EncodeToString(digest[:]) || key.QuotaTokens == nil || *key.QuotaTokens != tokens || key.RateLimit5H == nil || *key.RateLimit5H != rate || key.ExpiresAt == nil || !key.ExpiresAt.Equal(expires) || len(key.IPAllowlist) != 1 { // gitleaks:allow -- Synthetic test credential.
			t.Fatalf("policy or credential changed on row %d", index)
		}
		if key.Status != "disabled" || created[index].Status != "disabled" || created[index].Key != "" {
			t.Fatal("expired imports must be disabled and must not return raw credentials")
		}
	}
	inputs[1].QuotaTokens = new(int64)
	*inputs[1].QuotaTokens = -1
	repository.keys = nil
	if _, err := NewAdminKeyService(repository).Import(context.Background(), inputs); !errors.Is(err, ErrInvalidOrganization) || repository.keys != nil {
		t.Fatal("invalid policy must prevent the whole batch from being stored")
	}
}

func TestCreateBatchGeneratesIndependentKeysAtomically(t *testing.T) {
	repository := &keyRepositoryStub{}
	service := NewAdminKeyService(repository)
	tokens := int64(100)
	inputs := []AdminKeyInput{{Name: "first", UserID: 2, OrganizationID: 3, QuotaTokens: &tokens}, {Name: "second", UserID: 2, OrganizationID: 3, QuotaTokens: &tokens}}
	created, err := service.CreateBatch(context.Background(), inputs)
	if err != nil || len(created) != 2 || len(repository.keys) != 2 {
		t.Fatalf("batch failed: %v", err)
	}
	if created[0].Key == "" || created[0].Key == created[1].Key || repository.keys[0].KeyHash == repository.keys[1].KeyHash {
		t.Fatal("generated credentials must be independent")
	}
	for index, key := range repository.keys {
		digest := sha256.Sum256([]byte(created[index].Key))
		if key.KeyHash != hex.EncodeToString(digest[:]) || created[index].Name != inputs[index].Name || key.QuotaTokens == nil || *key.QuotaTokens != tokens {
			t.Fatal("returned credential or shared policy mismatch")
		}
	}
	repository.keys = nil
	inputs[1].Name = " "
	if _, err := service.CreateBatch(context.Background(), inputs); !errors.Is(err, ErrInvalidOrganization) || repository.keys != nil {
		t.Fatal("invalid batch must not persist any keys")
	}
}

func TestCreateBatchRejectsInvalidCountAndExistingCredentials(t *testing.T) {
	service := NewAdminKeyService(&keyRepositoryStub{})
	for _, inputs := range [][]AdminKeyInput{nil, make([]AdminKeyInput, 501), {{Name: "existing", UserID: 2, OrganizationID: 3, APIKey: "supplied-fixture-secret"}}} {
		if _, err := service.CreateBatch(context.Background(), inputs); !errors.Is(err, ErrInvalidOrganization) {
			t.Fatal("invalid generated batch accepted")
		}
	}
}

func TestAdminKeyNameValidationMatchesCreateAndUpdate(t *testing.T) {
	service := NewAdminKeyService(&keyRepositoryStub{})
	for _, test := range []struct {
		name  string
		valid bool
	}{
		{name: strings.Repeat("密", 100), valid: true},
		{name: "  " + strings.Repeat("密", 100) + "  ", valid: true},
		{name: strings.Repeat("密", 101)},
		{name: "   "},
	} {
		_, createErr := service.Create(context.Background(), AdminKeyInput{Name: test.name, OrganizationID: 3})
		updates := map[string]any{"name": test.name}
		updateErr := service.Update(context.Background(), 7, 3, updates)
		if (createErr == nil) != test.valid || (updateErr == nil) != test.valid {
			t.Fatalf("name length %d: create=%v update=%v want valid=%v", len(test.name), createErr, updateErr, test.valid)
		}
		if test.valid && updates["name"] != strings.TrimSpace(test.name) {
			t.Fatalf("updated name was not trimmed: %q", updates["name"])
		}
	}
}

func TestAdminKeyValidatesEveryQuota(t *testing.T) {
	service := NewAdminKeyService(&keyRepositoryStub{})
	input := AdminKeyInput{Name: "production", OrganizationID: 3}
	for name, field := range map[string]**float64{
		"quota_amount": &input.QuotaAmount, "daily_quota_amount": &input.DailyQuotaAmount,
		"weekly_quota_amount": &input.WeeklyQuotaAmount, "monthly_quota_amount": &input.MonthlyQuotaAmount,
	} {
		for _, value := range []float64{-1, 0, 1.5, math.NaN(), math.Inf(1)} {
			*field = &value
			_, createErr := service.Create(context.Background(), input)
			updateErr := service.Update(context.Background(), 7, 3, map[string]any{name: &value})
			valid := value >= 0 && !math.IsInf(value, 0)
			if (createErr == nil) != valid || (updateErr == nil) != valid {
				t.Fatalf("%s %v: create=%v update=%v", name, value, createErr, updateErr)
			}
		}
		*field = nil
		if err := service.Update(context.Background(), 7, 3, map[string]any{name: (*float64)(nil)}); err != nil {
			t.Fatalf("clear %s: %v", name, err)
		}
	}
	for name, field := range map[string]**int64{
		"quota_tokens": &input.QuotaTokens, "daily_quota_tokens": &input.DailyQuotaTokens,
		"weekly_quota_tokens": &input.WeeklyQuotaTokens, "monthly_quota_tokens": &input.MonthlyQuotaTokens,
	} {
		for _, value := range []int64{-1, 0, 10} {
			*field = &value
			_, createErr := service.Create(context.Background(), input)
			updateErr := service.Update(context.Background(), 7, 3, map[string]any{name: &value})
			if (createErr == nil) != (value >= 0) || (updateErr == nil) != (value >= 0) {
				t.Fatalf("%s %d: create=%v update=%v", name, value, createErr, updateErr)
			}
		}
		*field = nil
		if err := service.Update(context.Background(), 7, 3, map[string]any{name: (*int64)(nil)}); err != nil {
			t.Fatalf("clear %s: %v", name, err)
		}
	}
}
