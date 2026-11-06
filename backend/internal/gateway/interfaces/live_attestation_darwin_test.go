//go:build darwin

package interfaces

import (
	"context"
	"testing"
)

func TestLocalLiveAttestation(t *testing.T) {
	value, err := generateLiveAttestation(context.Background())
	if err != nil {
		t.Skipf("local DeviceCheck unavailable: %v", err)
	}
	if len(value) < 20 {
		t.Fatal("attestation too short")
	}
}
