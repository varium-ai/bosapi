package infrastructure

import (
	adminDomain "bosapi/backend/internal/admin/domain"
	"testing"
	"time"
)

func TestOrganizationQuotaCustomAnchorInactiveBeforeStart(t *testing.T) {
	anchor := time.Date(2026, 11, 25, 15, 55, 0, 0, time.UTC)
	config := quotaTestLimitedOrgConfig(anchor)
	if denial := EvaluateOrganizationQuota(config, quotaTestUsage(0, 0), anchor.Add(-time.Second)); denial != nil {
		t.Fatalf("future cycle should not apply: %+v", denial)
	}
	denial := EvaluateOrganizationQuota(config, quotaTestUsage(100, 0), anchor.Add(23*time.Hour))
	if denial == nil || denial.RetryAfter != time.Hour {
		t.Fatalf("denial=%+v", denial)
	}
}
func quotaTestLimitedOrgConfig(anchor time.Time) adminDomain.OrganizationRateLimit {
	return adminDomain.OrganizationRateLimit{OrganizationID: 1, QuotaResetStartAt: &anchor, DailyQuotaTokens: quotaTestPtr(int64(100))}
}
