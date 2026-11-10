package interfaces

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	adminDomain "bosapi/backend/internal/admin/domain"
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
)

type gatewayRateLimitRepository struct{ config adminDomain.RateLimitUpdate }

func (repository gatewayRateLimitRepository) ReadRateLimits(context.Context, uint64, uint64) (adminDomain.RateLimitUpdate, error) {
	return repository.config, nil
}

func (gatewayRateLimitRepository) WriteRateLimits(context.Context, adminDomain.RateLimitUpdate, uint64) error {
	return nil
}

func TestGatewayReturns429ForConfiguredQuotas(t *testing.T) {
	for _, test := range []struct {
		reason, code, retry string
		config              adminDomain.RateLimitUpdate
	}{
		{"quota_5h", "quota_exceeded", "18000", adminDomain.RateLimitUpdate{Keys: []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: adminDomain.RateLimitConfig{Quota5H: int64Ptr(1)}}}}},
		{"quota_1d", "quota_exceeded", "86400", adminDomain.RateLimitUpdate{Keys: []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: adminDomain.RateLimitConfig{Quota1D: int64Ptr(1)}}}}},
		{"quota_7d", "quota_exceeded", "604800", adminDomain.RateLimitUpdate{Keys: []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: adminDomain.RateLimitConfig{Quota7D: int64Ptr(1)}}}}},
	} {
		t.Run(test.reason, func(t *testing.T) {
			testGatewayConfiguredLimit(t, test.config, test.code, test.reason, test.retry)
		})
	}
}

func TestGatewayReleasesSessionAfterResponse(t *testing.T) {
	a := testApp(t)
	secret := "sk-gw-" + random(32)
	a.data.Providers = []provider{{ID: 1, Status: "active", Type: "unconfigured"}}
	a.data.Keys = []apiKey{{ID: 3, OrgID: 1, Hash: digest(secret), Status: "active", ProviderID: 1}}
	a.background.RateLimiter = gatewayinfra.NewLocalRateLimiter(gatewayRateLimitRepository{config: adminDomain.RateLimitUpdate{Keys: []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: adminDomain.RateLimitConfig{QPS: 1}}}}})
	handler := a.routes()
	for i := 0; i < 2; i++ {
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("completed request %d was not released: status=%d body=%s", i, response.Code, response.Body.String())
		}
	}
}

func testGatewayConfiguredLimit(t *testing.T, config adminDomain.RateLimitUpdate, code, reason, retry string) {
	t.Helper()
	a := testApp(t)
	secret := "sk-gw-" + random(32)
	a.data.Providers = []provider{{ID: 1, Status: "active", Type: "unconfigured"}}
	a.data.Keys = []apiKey{{ID: 3, OrgID: 1, Hash: digest(secret), Status: "active", ProviderID: 1}}
	a.background.RateLimiter = gatewayinfra.NewLocalRateLimiter(gatewayRateLimitRepository{config: config})
	handler := a.routes()
	for index, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("request %d status=%d, want=%d, body=%s", index, response.Code, want, response.Body.String())
		}
		if response.Header().Get("X-RateLimit-Degraded") != "true" {
			t.Fatalf("request %d did not report local enforcement", index)
		}
		if index == 1 {
			assertGatewayLimitError(t, response, code, reason, retry, "1")
		}
	}
}

func assertGatewayLimitError(t *testing.T, response *httptest.ResponseRecorder, code, reason, retry, limit string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code, Reason string
			Retryable    bool
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusTooManyRequests || envelope.Error.Code != code || envelope.Error.Reason != reason || envelope.Error.Retryable != (retry != "") {
		t.Fatalf("unexpected rejection: status=%d body=%s", response.Code, response.Body.String())
	}
	for name, want := range map[string]string{"Retry-After": retry, "X-RateLimit-Reason": reason, "X-RateLimit-Limit": limit, "X-RateLimit-Remaining": "0"} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s=%q, want %q", name, got, want)
		}
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Error("missing request identifier")
	}
}
