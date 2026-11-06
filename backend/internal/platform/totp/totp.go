package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// GenerateSecret generates a 20-byte random secret encoded as unpadded Base32.
func GenerateSecret() (string, error) {
	entropy := make([]byte, 20)
	if _, err := rand.Read(entropy); err != nil {
		return "", fmt.Errorf("generate totp entropy: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(entropy), nil
}

// GenerateCode calculates the 6-digit TOTP code for the secret at time t.
func GenerateCode(secret string, t time.Time) (string, error) {
	key, err := decodeBase32Secret(secret)
	if err != nil {
		return "", err
	}

	counter := uint64(t.Unix() / 30)
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(message[:])
	hash := mac.Sum(nil)

	offset := hash[len(hash)-1] & 0x0f
	binaryCode := (int(hash[offset])&0x7f)<<24 |
		(int(hash[offset+1])&0xff)<<16 |
		(int(hash[offset+2])&0xff)<<8 |
		(int(hash[offset+3]) & 0xff)

	code := binaryCode % 1000000
	return fmt.Sprintf("%06d", code), nil
}

// Validate checks whether a provided 6-digit code matches the secret within the given skew (number of 30s steps).
func Validate(code, secret string, t time.Time, skew int) bool {
	cleanCode := strings.TrimSpace(code)
	if len(cleanCode) != 6 {
		return false
	}
	for i := -skew; i <= skew; i++ {
		targetTime := t.Add(time.Duration(i*30) * time.Second)
		expected, err := GenerateCode(secret, targetTime)
		if err == nil && subtle.ConstantTimeCompare([]byte(expected), []byte(cleanCode)) == 1 {
			return true
		}
	}
	return false
}

// BuildURI creates a standard otpauth URI compatible with Google Authenticator, 1Password, etc.
func BuildURI(issuer, account, secret string) string {
	cleanSecret := strings.ToUpper(strings.TrimSpace(secret))
	cleanIssuer := strings.TrimSpace(issuer)
	cleanAccount := strings.TrimSpace(account)
	label := url.PathEscape(cleanIssuer) + ":" + url.PathEscape(cleanAccount)

	params := url.Values{}
	params.Set("secret", cleanSecret)
	params.Set("issuer", cleanIssuer)
	params.Set("algorithm", "SHA1")
	params.Set("digits", "6")
	params.Set("period", "30")

	return fmt.Sprintf("otpauth://totp/%s?%s", label, params.Encode())
}

func decodeBase32Secret(secret string) ([]byte, error) {
	clean := strings.ToUpper(strings.TrimSpace(secret))
	if clean == "" {
		return nil, errors.New("empty totp secret")
	}
	// Try without padding first
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(clean)
	if err == nil && len(key) > 0 {
		return key, nil
	}
	// Try standard padding
	key, err = base32.StdEncoding.DecodeString(clean)
	if err == nil && len(key) > 0 {
		return key, nil
	}
	return nil, errors.New("invalid base32 secret")
}
