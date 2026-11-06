package domain

import "testing"

func TestAppendTriggerSignalDeduplicatesAndKeepsOrder(t *testing.T) {
	signals := []string{TriggerObserveHit}
	signals = AppendTriggerSignal(signals, TriggerEncodedFragment)
	signals = AppendTriggerSignal(signals, TriggerEncodedFragment)
	signals = AppendTriggerSignal(signals, TriggerUntrustedUpstream)
	if len(signals) != 3 || signals[0] != TriggerObserveHit || signals[1] != TriggerEncodedFragment || signals[2] != TriggerUntrustedUpstream {
		t.Fatalf("signals=%+v", signals)
	}
}
