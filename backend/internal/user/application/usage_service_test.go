package application

import (
	"testing"
	"time"
)

func TestNormalizeDateRangeUsesInclusiveShanghaiDays(t *testing.T) {
	for _, test := range []struct{ day, start, end string }{
		{"2026-10-13", "2026-10-12T16:00:00Z", "2026-10-13T16:00:00Z"},
		{"2026-10-01", "2026-09-30T16:00:00Z", "2026-10-01T16:00:00Z"},
		{"2027-01-01", "2026-12-31T16:00:00Z", "2027-01-01T16:00:00Z"},
	} {
		t.Run(test.day, func(t *testing.T) {
			start, end, err := NormalizeDateRange(test.day, test.day, time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if start == nil || end == nil || start.Format(time.RFC3339) != test.start || end.Format(time.RFC3339) != test.end {
				t.Fatalf("range=[%v,%v), want [%s,%s)", start, end, test.start, test.end)
			}
		})
	}
}
