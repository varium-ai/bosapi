package interfaces

import (
	"net/http/httptest"
	"testing"
	"time"

	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/usagetime"
)

func TestGatewayQuotaResetAndReasons(t *testing.T) {
	// Sunday just before the day, week and month reset together in UTC.
	now := time.Date(2026, 5, 31, 23, 59, 59, 500000000, time.UTC)
	for _, test := range []struct {
		reason string
		key    apiKey
		retry  string
		limit  string
	}{
		{"total_tokens", apiKey{QuotaTokens: int64Ptr(10), QuotaUsedTokens: 10}, "", "10"},
		{"total_amount", apiKey{QuotaAmount: float64Ptr(0.5), QuotaUsedAmount: 0.5}, "", "0.5"},
		{"daily_tokens", apiKey{DailyQuotaTokens: int64Ptr(10), DailyUsedTokens: 10}, "1", "10"},
		{"daily_amount", apiKey{DailyQuotaAmount: float64Ptr(0.5), DailyUsedAmount: 0.5}, "1", "0.5"},
		{"weekly_tokens", apiKey{WeeklyQuotaTokens: int64Ptr(10), WeeklyUsedTokens: 10}, "1", "10"},
		{"weekly_amount", apiKey{WeeklyQuotaAmount: float64Ptr(0.5), WeeklyUsedAmount: 0.5}, "1", "0.5"},
		{"monthly_tokens", apiKey{MonthlyQuotaTokens: int64Ptr(10), MonthlyUsedTokens: 10}, "1", "10"},
		{"monthly_amount", apiKey{MonthlyQuotaAmount: float64Ptr(0.5), MonthlyUsedAmount: 0.5}, "1", "0.5"},
		{"daily_tokens", apiKey{DailyQuotaTokens: int64Ptr(0)}, "", "0"},
		{"weekly_amount", apiKey{WeeklyQuotaAmount: float64Ptr(0)}, "", "0"},
	} {
		t.Run(test.reason+"/"+test.limit, func(t *testing.T) {
			denial := gatewayQuotaDenial(test.key, now)
			if denial == nil {
				t.Fatal("exhausted quota allowed")
			}
			response := httptest.NewRecorder()
			response.Header().Set("X-Request-ID", "quota-retry-test")
			writeGatewayLimitError(response, errorcode.QuotaExceeded, denial.reason, denial.retryAfter, denial.limit)
			assertGatewayLimitError(t, response, "quota_exceeded", test.reason, test.retry, test.limit)
			if test.retry != "" && denial.retryAfter != 500*time.Millisecond {
				t.Fatalf("retryAfter=%s, want 500ms", denial.retryAfter)
			}
		})
	}
	if gatewayQuotaDenial(apiKey{DailyQuotaTokens: int64Ptr(10), DailyUsedTokens: 9}, now) != nil {
		t.Fatal("available quota rejected")
	}
}

func TestGatewayQuotaReportsLongestBlocker(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, usagetime.Location())
	key := apiKey{
		DailyQuotaTokens: int64Ptr(10), DailyUsedTokens: 10,
		WeeklyQuotaTokens: int64Ptr(20), WeeklyUsedTokens: 20,
		MonthlyQuotaAmount: float64Ptr(0.5), MonthlyUsedAmount: 0.5,
	}
	denial := gatewayQuotaDenial(key, now)
	wantReset := time.Date(2026, 11, 1, 0, 0, 0, 0, usagetime.Location())
	if denial.reason != "monthly_amount" || denial.retryAfter != wantReset.Sub(now) {
		t.Fatalf("denial=%+v, want reset=%s", denial, wantReset)
	}
	key.QuotaTokens, key.QuotaUsedTokens = int64Ptr(100), 100
	denial = gatewayQuotaDenial(key, now)
	if denial.reason != "total_tokens" || denial.retryAfter != 0 {
		t.Fatalf("total budget should require manual change: %+v", denial)
	}
	key.QuotaTokens = nil
	key.WeeklyQuotaTokens = int64Ptr(0)
	denial = gatewayQuotaDenial(key, now)
	if denial.reason != "weekly_tokens" || denial.retryAfter != 0 {
		t.Fatalf("zero budget should require manual change: %+v", denial)
	}
}

func TestGatewayQuotaHTTPReasons(t *testing.T) {
	for _, period := range periodicQuotaTestCases() {
		t.Run(period.name, func(t *testing.T) {
			a, repository, secret := newQuotaTestApp(t, true, nil, nil)
			period.configure(&repository.key, 10, 10)
			response := httptest.NewRecorder()
			a.routes().ServeHTTP(response, quotaTestRequest(t.Context(), secret, "quota_reason", false))
			assertQuotaResponse(t, response, 429)
			retry := response.Header().Get("Retry-After")
			if retry == "" {
				t.Fatal("periodic quota missing Retry-After")
			}
			assertGatewayLimitError(t, response, "quota_exceeded", period.name, retry, "10")
		})
	}
	a, _, secret := newQuotaTestApp(t, false, int64Ptr(0), nil)
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, quotaTestRequest(t.Context(), secret, "total_quota_reason", false))
	assertGatewayLimitError(t, response, "quota_exceeded", "total_tokens", "", "0")
}
