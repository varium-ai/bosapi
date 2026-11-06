package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSafeKeySuffixKeepsSecretCharactersHidden(t *testing.T) {
	for _, test := range []struct{ name, raw, prefix, want string }{
		{"normal", "vendor-secret-ABCD", "vend", "ABCD"},
		{"empty", "", "", ""},
		{"short", "abcd", "abc", ""},
		{"entire key would be visible", "abcdWXYZ", "abcd", ""},
		{"one hidden character", "abcdeWXYZ", "abcd", "WXYZ"},
		{"unicode suffix", "sk-甲乙丙丁戊己庚辛壬癸", "sk-", "庚辛壬癸"},
		{"unicode prefix", "甲乙丙丁戊己庚辛壬", "甲乙丙丁", "己庚辛壬"},
		{"short unicode", "甲乙丙丁戊己庚辛", "甲乙丙丁", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := SafeKeySuffix(test.raw, test.prefix); got != test.want {
				t.Fatalf("suffix = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRedactedIncludesStoredSuffixWithoutCredentials(t *testing.T) {
	key := APIKey{ID: 7, Name: "example", KeyPrefix: "vend", KeySuffix: "ABCD", KeyHash: "private-digest", KeyEncrypted: "private-ciphertext"}
	safe := key.Redacted()
	if safe.KeySuffix != "ABCD" || safe.MaskedKey != "vend••••••••" {
		t.Fatalf("unexpected redacted key: %+v", safe)
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{key.KeyHash, key.KeyEncrypted, "key_hash", "key_encrypted"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("redacted response contains credential material")
		}
	}
}
