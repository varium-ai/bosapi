package infrastructure

import (
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
)

func TestUsageRollupsRequireWholeBusinessDaysAndNoIPFilter(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(48 * time.Hour)
	partial := from.Add(time.Millisecond)
	for _, test := range []struct {
		name   string
		filter domain.AdminUsageFilter
		want   bool
	}{
		{"all history", domain.AdminUsageFilter{}, true},
		{"business dates", domain.AdminUsageFilter{From: &from, To: &to}, true},
		{"open end", domain.AdminUsageFilter{From: &from}, true},
		{"open start", domain.AdminUsageFilter{To: &to}, true},
		{"key and organization and model", domain.AdminUsageFilter{From: &from, To: &to, OrganizationID: 9, APIKeyID: 7, Model: "model"}, true},
		{"partial start", domain.AdminUsageFilter{From: &partial, To: &to}, false},
		{"partial end", domain.AdminUsageFilter{From: &from, To: &partial}, false},
		{"client IP", domain.AdminUsageFilter{From: &from, To: &to, ClientIP: "192.0.2.1"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := supportsUsageRollups(test.filter); got != test.want {
				t.Fatalf("supported=%v want=%v", got, test.want)
			}
		})
	}
}
