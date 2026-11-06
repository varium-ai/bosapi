package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"bosapi/backend/internal/admin/domain"
	userdomain "bosapi/backend/internal/user/domain"
)

type adminKeySuffixRepositoryStub struct {
	keyRepositoryStub
	listed []userdomain.APIKey
}

func (stub *adminKeySuffixRepositoryStub) ListAdminKeys(context.Context, domain.AdminKeyFilter, int, int) ([]userdomain.APIKey, int64, error) {
	return stub.listed, int64(len(stub.listed)), nil
}

type adminKeySuffixCipherStub struct {
	plaintext    string
	encrypted    string
	encryptErr   error
	decryptErr   error
	decryptCalls int
}

func (stub *adminKeySuffixCipherStub) Encrypt(string) (string, error) {
	return stub.encrypted, stub.encryptErr
}

func (stub *adminKeySuffixCipherStub) Decrypt(string) (string, error) {
	stub.decryptCalls++
	return stub.plaintext, stub.decryptErr
}

func TestListAdminKeysReturnsSafeSuffix(t *testing.T) {
	for _, test := range []struct {
		name          string
		prefix        string
		plaintext     string
		hashPlaintext string
		storedSuffix  string
		wantSuffix    string
		missingCipher bool
		missingSecret bool
		decryptErr    error
	}{
		{name: "recoverable key", prefix: "vend", plaintext: "vendor-token-secret-ABCD", wantSuffix: "ABCD"},
		{name: "stored suffix without cipher", prefix: "vend", plaintext: "vendor-token-secret-ABCD", storedSuffix: "ABCD", wantSuffix: "ABCD", missingCipher: true, missingSecret: true},
		{name: "stored suffix skips unavailable ciphertext", prefix: "vend", plaintext: "vendor-token-secret-ABCD", storedSuffix: "ABCD", wantSuffix: "ABCD", decryptErr: errors.New("invalid ciphertext")},
		{name: "stored suffix takes priority over stale ciphertext", prefix: "vend", plaintext: "vendor-previous-token-ABCD", hashPlaintext: "vendor-current-token-EFGH", storedSuffix: "EFGH", wantSuffix: "EFGH"},
		{name: "missing cipher", prefix: "vend", plaintext: "vendor-token-secret-ABCD", missingCipher: true},
		{name: "legacy key without ciphertext", prefix: "vend", plaintext: "vendor-token-secret-ABCD", missingSecret: true},
		{name: "decryption failure", prefix: "vend", plaintext: "vendor-token-secret-ABCD", decryptErr: errors.New("invalid ciphertext")},
		{name: "stale ciphertext does not match current hash", prefix: "vend", plaintext: "vendor-previous-token-ABCD", hashPlaintext: "vendor-current-token-EFGH"},
		{name: "short key", prefix: "abc", plaintext: "abcd"},
		{name: "prefix and suffix cover entire key", prefix: "abcd", plaintext: "abcdWXYZ"},
		{name: "one character remains hidden", prefix: "abcd", plaintext: "abcdeWXYZ", wantSuffix: "WXYZ"},
		{name: "unicode suffix", prefix: "sk-", plaintext: "sk-甲乙丙丁戊己庚辛壬癸", wantSuffix: "庚辛壬癸"},
		{name: "unicode short key", prefix: "甲乙丙丁", plaintext: "甲乙丙丁戊己庚辛"},
		{name: "empty decrypted value", prefix: "vend"},
	} {
		t.Run(test.name, func(t *testing.T) {
			hashPlaintext := test.plaintext
			if test.hashPlaintext != "" {
				hashPlaintext = test.hashPlaintext
			}
			digest := sha256.Sum256([]byte(hashPlaintext))
			key := userdomain.APIKey{ID: 7, Name: "production", KeyPrefix: test.prefix, KeySuffix: test.storedSuffix, KeyHash: hex.EncodeToString(digest[:]), KeyEncrypted: "__encrypted-secret-value__"}
			if test.missingSecret {
				key.KeyEncrypted = ""
			}
			repository := &adminKeySuffixRepositoryStub{listed: []userdomain.APIKey{key}}
			service := NewAdminKeyService(repository)
			cipher := &adminKeySuffixCipherStub{plaintext: test.plaintext, decryptErr: test.decryptErr}
			if !test.missingCipher {
				service.WithKeyCipher(cipher)
			}

			items, total, err := service.List(context.Background(), domain.AdminKeyFilter{}, 1, 100)
			if err != nil || total != 1 || len(items) != 1 {
				t.Fatalf("list result: items=%d total=%d err=%v", len(items), total, err)
			}
			item := items[0]
			if item.KeySuffix != test.wantSuffix {
				t.Fatalf("key suffix = %q, want %q", item.KeySuffix, test.wantSuffix)
			}
			if item.ID != key.ID || item.KeyPrefix != key.KeyPrefix || item.MaskedKey != key.Redacted().MaskedKey {
				t.Fatalf("list changed the existing redacted fields: %+v", item)
			}
			wantDecryptCalls := 1
			if test.missingCipher || test.missingSecret || test.storedSuffix != "" {
				wantDecryptCalls = 0
			}
			if cipher.decryptCalls != wantDecryptCalls {
				t.Fatalf("decryption calls = %d, want %d", cipher.decryptCalls, wantDecryptCalls)
			}

			encoded, err := json.Marshal(item)
			if err != nil {
				t.Fatalf("marshal safe key: %v", err)
			}
			for _, secret := range []string{test.plaintext, key.KeyHash, key.KeyEncrypted} {
				if secret != "" && strings.Contains(string(encoded), secret) {
					t.Fatalf("safe response contains a full credential, digest, or ciphertext")
				}
			}
			var response map[string]any
			if err := json.Unmarshal(encoded, &response); err != nil {
				t.Fatalf("unmarshal safe key: %v", err)
			}
			for _, field := range []string{"key", "api_key", "key_hash", "key_encrypted"} {
				if _, exists := response[field]; exists {
					t.Fatalf("safe response contains secret field %q", field)
				}
			}
			suffix, hasSuffix := response["key_suffix"]
			if test.wantSuffix == "" && hasSuffix || test.wantSuffix != "" && (!hasSuffix || suffix != test.wantSuffix) {
				t.Fatalf("serialized suffix = %v, present=%t, want %q", suffix, hasSuffix, test.wantSuffix)
			}
		})
	}
}

func TestAdminKeySuffixIsSavedWithoutRecoverableCiphertext(t *testing.T) {
	for _, test := range []struct {
		name   string
		cipher KeyCipher
	}{
		{name: "no cipher"},
		{name: "encryption failure", cipher: &adminKeySuffixCipherStub{encrypted: "invalid-result", encryptErr: errors.New("encryption unavailable")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &keyRepositoryStub{}
			service := NewAdminKeyService(repository).WithKeyCipher(test.cipher)
			created, err := service.Create(context.Background(), AdminKeyInput{Name: "generated", UserID: 2, OrganizationID: 3})
			if err != nil {
				t.Fatalf("generate key: %v", err)
			}
			if repository.key.KeySuffix != created.Key[len(created.Key)-4:] || repository.key.KeyEncrypted != "" {
				t.Fatalf("generated key did not preserve only valid display metadata")
			}
			inputs := []AdminKeyInput{
				{Name: "production", APIKey: "provider-token-secret-ABCD", UserID: 2, OrganizationID: 3},
				{Name: "unicode", APIKey: "provider-token-甲乙丙丁戊己庚辛", UserID: 2, OrganizationID: 3},
			}
			if _, err := service.Import(context.Background(), inputs); err != nil {
				t.Fatalf("import keys: %v", err)
			}
			for index, want := range []string{"ABCD", "戊己庚辛"} {
				if repository.keys[index].KeySuffix != want || repository.keys[index].KeyEncrypted != "" {
					t.Fatalf("imported key %d: suffix=%q ciphertext present=%t", index, repository.keys[index].KeySuffix, repository.keys[index].KeyEncrypted != "")
				}
			}
		})
	}
}

func TestResetAdminKeyReplacesSuffixWithNewSecret(t *testing.T) {
	for _, test := range []struct {
		name          string
		cipher        KeyCipher
		wantEncrypted string
	}{
		{name: "no cipher"},
		{name: "encryption failure", cipher: &adminKeySuffixCipherStub{encrypted: "invalid-result", encryptErr: errors.New("encryption unavailable")}},
		{name: "recoverable ciphertext", cipher: &adminKeySuffixCipherStub{encrypted: "new-ciphertext"}, wantEncrypted: "new-ciphertext"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &keyRepositoryStub{key: userdomain.APIKey{ID: 7, KeySuffix: "OLD!", KeyEncrypted: "previous-ciphertext"}}
			created, err := NewAdminKeyService(repository).WithKeyCipher(test.cipher).Reset(context.Background(), 7, 3)
			if err != nil {
				t.Fatalf("reset key: %v", err)
			}
			digest := sha256.Sum256([]byte(created.Key))
			if repository.key.KeyHash != hex.EncodeToString(digest[:]) || repository.key.KeyPrefix != created.KeyPrefix || repository.key.KeySuffix != created.Key[len(created.Key)-4:] {
				t.Fatalf("reset did not persist the new secret and its display metadata together")
			}
			if repository.key.KeyEncrypted != test.wantEncrypted {
				t.Fatalf("reset retained previous or invalid ciphertext")
			}
			if created.ID != 7 || created.OrganizationID != 3 {
				t.Fatalf("reset response lost key scope: id=%d organization=%d", created.ID, created.OrganizationID)
			}
		})
	}
}
