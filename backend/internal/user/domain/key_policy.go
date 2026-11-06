package domain

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

// ValidateKeyName bounds the trimmed name to 1–100 Unicode characters.
func ValidateKeyName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 {
		return errors.New("api key name must contain 1 to 100 characters")
	}
	return nil
}

// ValidateKeyQuotaAmounts accepts unlimited (nil), zero, or finite positive budgets.
func ValidateKeyQuotaAmounts(amounts ...*float64) error {
	for _, amount := range amounts {
		if amount != nil && (*amount < 0 || math.IsNaN(*amount) || math.IsInf(*amount, 0)) {
			return errors.New("api key quota amount must be finite and non-negative")
		}
	}
	return nil
}

// ValidateKeyQuotaTokens accepts unlimited (nil), zero, or positive token budgets.
func ValidateKeyQuotaTokens(limits ...*int64) error {
	for _, limit := range limits {
		if limit != nil && *limit < 0 {
			return errors.New("api key quota tokens must be non-negative")
		}
	}
	return nil
}

// ValidateKeyRateLimits rejects values that cannot be preserved in key storage.
func ValidateKeyRateLimits(limits ...*int64) error {
	for _, limit := range limits {
		if limit != nil && (*limit <= 0 || *limit > math.MaxUint32) {
			return errors.New("api key rate limit must be between 1 and 4294967295")
		}
	}
	return nil
}
