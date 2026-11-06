package domain

import (
	"errors"
	"strings"
)

// ValidateKeyAccessPolicy bounds optional filters; empty lists inherit channel policy.
func ValidateKeyAccessPolicy(providers []uint64, models []string) error {
	if len(providers) > 500 || len(models) > 500 {
		return errors.New("too many channel or model restrictions")
	}
	seen := make(map[uint64]bool, len(providers))
	for _, id := range providers {
		if id == 0 || seen[id] {
			return errors.New("channel ids must be positive and distinct")
		}
		seen[id] = true
	}
	seenModels := make(map[string]bool, len(models))
	for _, model := range models {
		if model != strings.TrimSpace(model) || model == "" || len(model) > 191 || model == "*" || seenModels[model] {
			return errors.New("models must be distinct explicit model ids")
		}
		seenModels[model] = true
	}
	return nil
}
