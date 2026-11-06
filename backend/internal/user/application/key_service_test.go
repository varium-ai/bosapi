package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"testing"

	"bosapi/backend/internal/user/domain"
)

func TestCreateKeyInputValidate(t *testing.T) {
	valid := CreateKeyInput{Name: "production", IPAllowlist: []string{"192.0.2.0/24"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	invalid := []CreateKeyInput{
		{Name: " "},
		{Name: "key", IPBlocklist: []string{"not-an-ip"}},
	}
	for _, input := range invalid {
		if err := input.Validate(); err == nil {
			t.Fatalf("invalid input accepted: %+v", input)
		}
	}
}

type suffixKeyRepositoryStub struct {
	key domain.APIKey
}

func (stub *suffixKeyRepositoryStub) Create(_ context.Context, key *domain.APIKey) error {
	key.ID = 17
	stub.key = *key
	return nil
}

func (stub *suffixKeyRepositoryStub) List(context.Context, KeyFilter, int, int) ([]domain.APIKey, int64, error) {
	return []domain.APIKey{stub.key}, 1, nil
}

func (stub *suffixKeyRepositoryStub) FindOwned(context.Context, uint64, uint64, uint64) (domain.APIKey, error) {
	return stub.key, nil
}

func (stub *suffixKeyRepositoryStub) UpdateOwned(context.Context, uint64, uint64, uint64, map[string]any) error {
	return nil
}

type failedKeyCipherStub struct{}

func (failedKeyCipherStub) Encrypt(string) (string, error) {
	return "invalid-result", errors.New("encryption unavailable")
}

func (failedKeyCipherStub) Decrypt(string) (string, error) {
	return "", errors.New("decryption unavailable")
}

func TestCreateKeySavesSuffixWithoutRecoverableCiphertext(t *testing.T) {
	for _, test := range []struct {
		name   string
		cipher KeyCipher
	}{
		{name: "no cipher"},
		{name: "encryption failure", cipher: failedKeyCipherStub{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &suffixKeyRepositoryStub{}
			service := NewKeyService(repository).WithKeyCipher(test.cipher)
			created, err := service.Create(context.Background(), 2, 3, CreateKeyInput{Name: "production"})
			if err != nil {
				t.Fatalf("create key: %v", err)
			}
			digest := sha256.Sum256([]byte(created.Key))
			wantSuffix := created.Key[len(created.Key)-4:]
			if repository.key.KeyHash != hex.EncodeToString(digest[:]) || repository.key.KeySuffix != wantSuffix || repository.key.KeyEncrypted != "" {
				t.Fatalf("key did not persist the correct digest and display metadata")
			}
			items, total, err := service.List(context.Background(), KeyFilter{UserID: 2, OrganizationID: 3}, 1, 10)
			if err != nil || total != 1 || len(items) != 1 {
				t.Fatalf("list key: count=%d total=%d err=%v", len(items), total, err)
			}
			if items[0].KeySuffix != wantSuffix {
				t.Fatalf("listed key suffix=%q, want %q", items[0].KeySuffix, wantSuffix)
			}
		})
	}
}

func TestCreateKeyInputValidatesRateLimitStorageRange(t *testing.T) {
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
			input := CreateKeyInput{Name: "limited", RateLimit5H: &test.limit, RateLimit1D: &test.limit, RateLimit7D: &test.limit}
			if err := input.Validate(); (err == nil) != test.valid {
				t.Fatalf("validate limit %d: err = %v, want valid = %v", test.limit, err, test.valid)
			}
		})
	}
}

func TestCreateKeyNameUsesUnicodeCharacters(t *testing.T) {
	repository := &suffixKeyRepositoryStub{}
	name := strings.Repeat("密", 100)
	if _, err := NewKeyService(repository).Create(context.Background(), 2, 3, CreateKeyInput{Name: name}); err != nil {
		t.Fatalf("create key with 100 Unicode characters: %v", err)
	}
	if repository.key.Name != name {
		t.Fatalf("stored name=%q", repository.key.Name)
	}
	if err := (CreateKeyInput{Name: name + "钥"}).Validate(); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("101 character name: %v", err)
	}
}

func TestCreateKeyInputValidatesEveryQuota(t *testing.T) {
	input := CreateKeyInput{Name: "production"}
	for name, field := range map[string]**float64{
		"total": &input.QuotaAmount, "daily": &input.DailyQuotaAmount,
		"weekly": &input.WeeklyQuotaAmount, "monthly": &input.MonthlyQuotaAmount,
	} {
		for _, value := range []float64{-1, 0, 1.5, math.NaN(), math.Inf(1)} {
			*field = &value
			valid := value >= 0 && !math.IsInf(value, 0)
			if err := input.Validate(); (err == nil) != valid {
				t.Fatalf("%s amount %v: err=%v", name, value, err)
			}
		}
		*field = nil
	}
	for name, field := range map[string]**int64{
		"total": &input.QuotaTokens, "daily": &input.DailyQuotaTokens,
		"weekly": &input.WeeklyQuotaTokens, "monthly": &input.MonthlyQuotaTokens,
	} {
		for _, value := range []int64{-1, 0, 10} {
			*field = &value
			if err := input.Validate(); (err == nil) != (value >= 0) {
				t.Fatalf("%s tokens %d: err=%v", name, value, err)
			}
		}
		*field = nil
	}
}
