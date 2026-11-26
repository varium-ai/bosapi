package usagetime

import "time"

// QuotaPeriods defaults to the UTC calendar. Custom cycles use fixed 1/7/30 days.
// Before a future anchor, starts equal the anchor and no periodic budget applies.
func QuotaPeriods(now time.Time, anchor *time.Time) (starts, resets [3]time.Time) {
	if anchor == nil {
		starts[0], starts[1], starts[2] = PeriodStarts(now)
		resets = [3]time.Time{starts[0].AddDate(0, 0, 1), starts[1].AddDate(0, 0, 7), starts[2].AddDate(0, 1, 0)}
		return
	}
	for i, duration := range []time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour} {
		start := anchor.UTC()
		if !now.Before(start) {
			start = start.Add(now.Sub(start) / duration * duration)
		}
		starts[i], resets[i] = start, start.Add(duration)
	}
	return
}
