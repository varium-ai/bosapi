//go:build !darwin

package interfaces

import (
	"context"
	"errors"
)

func generateLiveAttestation(context.Context) (string, error) {
	return "", errors.New("live attestation requires macos")
}
