package interfaces

import (
	"testing"
	"time"
)

func TestParseUsageBoundarySupportsMinutePrecision(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	start, err := parseUsageBoundary("2026-11-11T09:15:00+08:00", location, false)
	if err != nil || !start.Equal(time.Date(2026, 11, 11, 1, 15, 0, 0, time.UTC)) {
		t.Fatalf("start=%v err=%v", start, err)
	}
	end, err := parseUsageBoundary("2026-11-11T09:16:00+08:00", location, true)
	if err != nil || !end.Equal(time.Date(2026, 11, 11, 1, 17, 0, 0, time.UTC)) {
		t.Fatalf("end=%v err=%v", end, err)
	}
	legacyEnd, err := parseUsageBoundary("2026-11-11", location, true)
	if err != nil || !legacyEnd.Equal(time.Date(2026, 11, 11, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("legacy end=%v err=%v", legacyEnd, err)
	}
}
