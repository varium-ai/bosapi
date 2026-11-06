package usagetime

import (
	"testing"
	"time"
)

func TestPeriodStartsAtShanghaiCalendarBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, now, day, week, month string
	}{
		{"before midnight", "2026-10-12T15:59:59Z", "2026-10-11T16:00:00Z", "2026-10-11T16:00:00Z", "2026-09-30T16:00:00Z"},
		{"after midnight", "2026-10-12T16:00:00Z", "2026-10-12T16:00:00Z", "2026-10-11T16:00:00Z", "2026-09-30T16:00:00Z"},
		{"before Monday", "2026-10-11T15:59:59Z", "2026-10-10T16:00:00Z", "2026-10-04T16:00:00Z", "2026-09-30T16:00:00Z"},
		{"Monday", "2026-10-11T16:00:00Z", "2026-10-11T16:00:00Z", "2026-10-11T16:00:00Z", "2026-09-30T16:00:00Z"},
		{"month rollover", "2026-09-30T16:00:00Z", "2026-09-30T16:00:00Z", "2026-09-27T16:00:00Z", "2026-09-30T16:00:00Z"},
		{"year rollover", "2026-12-31T16:00:00Z", "2026-12-31T16:00:00Z", "2026-12-27T16:00:00Z", "2026-12-31T16:00:00Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, test.now)
			if err != nil {
				t.Fatal(err)
			}
			// Host location must not change the business date.
			now = now.In(time.FixedZone("other", -7*60*60))
			day, week, month := PeriodStarts(now)
			for name, pair := range map[string][2]string{
				"day":   {day.Format(time.RFC3339), test.day},
				"week":  {week.Format(time.RFC3339), test.week},
				"month": {month.Format(time.RFC3339), test.month},
			} {
				if pair[0] != pair[1] {
					t.Errorf("%s start=%s, want %s", name, pair[0], pair[1])
				}
			}
		})
	}
}
