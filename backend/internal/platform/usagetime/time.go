// Package usagetime defines the UTC+8 business calendar used by usage and quotas.
package usagetime

import "time"

// SQLTimestamp converts UTC DATETIME values without MySQL time zone tables.
const SQLTimestamp = "CONVERT_TZ(created_at, '+00:00', '+08:00')"

var location = time.FixedZone("Asia/Shanghai", 8*60*60)

// Location returns the business calendar's fixed UTC+8 time zone.
func Location() *time.Location { return location }

// DayStart returns the start of the current business day as a UTC instant.
func DayStart(now time.Time) time.Time {
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location).UTC()
}

// PeriodStarts returns UTC boundaries for the business day, Monday-based week,
// and calendar month containing now.
func PeriodStarts(now time.Time) (day, week, month time.Time) {
	day = DayStart(now)
	local := day.In(location)
	week = day.AddDate(0, 0, -(int(local.Weekday())+6)%7)
	month = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location).UTC()
	return
}
