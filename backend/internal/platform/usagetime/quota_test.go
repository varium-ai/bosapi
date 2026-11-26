package usagetime

import (
	"testing"
	"time"
)

func TestQuotaPeriodsCustomFixedCycles(t *testing.T) {
	anchor := time.Date(2026, 11, 25, 15, 55, 0, 0, time.UTC)
	now := anchor.Add(24*time.Hour + time.Hour)
	starts, resets := QuotaPeriods(now, &anchor)
	if !starts[0].Equal(anchor.Add(24*time.Hour)) || !resets[0].Equal(anchor.Add(48*time.Hour)) {
		t.Fatalf("daily: %v %v", starts[0], resets[0])
	}
	if !starts[1].Equal(anchor) || !resets[1].Equal(anchor.Add(7*24*time.Hour)) {
		t.Fatalf("weekly: %v %v", starts[1], resets[1])
	}
	if !starts[2].Equal(anchor) || !resets[2].Equal(anchor.Add(30*24*time.Hour)) {
		t.Fatalf("monthly: %v %v", starts[2], resets[2])
	}
	future := anchor.Add(time.Hour)
	starts, resets = QuotaPeriods(anchor.Add(-time.Hour), &future)
	for i := range starts {
		if !starts[i].Equal(future) || !resets[i].After(future) {
			t.Fatalf("future period %d: %v %v", i, starts[i], resets[i])
		}
	}
}
