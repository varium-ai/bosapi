package infrastructure

import (
	"context"
	"testing"
	"time"

	adminDomain "bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/gateway/domain"
	"gorm.io/gorm"
)

type rateLimitTestRepository struct {
	config        adminDomain.RateLimitUpdate
	missingKeyRow bool
}

func (repository *rateLimitTestRepository) ReadRateLimits(_ context.Context, _ uint64, keyID uint64) (adminDomain.RateLimitUpdate, error) {
	if repository.missingKeyRow && keyID != 0 {
		return adminDomain.RateLimitUpdate{}, gorm.ErrRecordNotFound
	}
	return repository.config, nil
}

func (*rateLimitTestRepository) WriteRateLimits(context.Context, adminDomain.RateLimitUpdate, uint64) error {
	return nil
}

func TestLocalRateLimiterEnforcesSavedKeyLimit(t *testing.T) {
	repository := &rateLimitTestRepository{config: adminDomain.RateLimitUpdate{
		Keys: []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: adminDomain.RateLimitConfig{QPS: 1}}},
	}}
	limiter := NewLocalRateLimiter(repository)
	now := time.Unix(1_800_000_000, 0)
	key := domain.RateLimitKey{OrganizationID: 1, APIKeyID: 3}
	for index, want := range []bool{true, false} {
		decision, err := limiter.Decide(context.Background(), key, now.Add(time.Duration(index)*time.Millisecond))
		if err != nil || decision.Allowed != want || !decision.Degraded {
			t.Fatalf("request %d: decision=%+v err=%v", index, decision, err)
		}
		if !decision.Allowed && (decision.Reason != domain.RateLimitKeyQPS || decision.RetryAfter != 999*time.Millisecond) {
			t.Fatalf("missing key QPS reason or reset: %+v", decision)
		}
	}
	decision, err := limiter.Decide(context.Background(), domain.RateLimitKey{OrganizationID: 1, APIKeyID: 4}, now)
	if err != nil || !decision.Allowed {
		t.Fatalf("other key: decision=%+v err=%v", decision, err)
	}
	decision, err = limiter.Decide(context.Background(), key, now.Add(time.Second))
	if err != nil || !decision.Allowed {
		t.Fatalf("after window: decision=%+v err=%v", decision, err)
	}
}

func TestLocalRateLimiterSharesOrganizationAndGlobalCounters(t *testing.T) {
	repository := &rateLimitTestRepository{config: adminDomain.RateLimitUpdate{
		Global:        &adminDomain.RateLimitConfig{QPS: 3},
		Organizations: []adminDomain.OrganizationRateLimit{{OrganizationID: 1, QPS: 2}},
	}}
	limiter := NewLocalRateLimiter(repository)
	now := time.Unix(1_800_000_000, 0)
	check := func(key domain.RateLimitKey, want bool) {
		t.Helper()
		decision, err := limiter.Decide(context.Background(), key, now)
		if err != nil || decision.Allowed != want {
			t.Fatalf("key=%+v decision=%+v err=%v, want allowed=%t", key, decision, err, want)
		}
	}
	check(domain.RateLimitKey{OrganizationID: 1, APIKeyID: 3}, true)
	check(domain.RateLimitKey{OrganizationID: 1, APIKeyID: 4}, true)
	check(domain.RateLimitKey{OrganizationID: 1, APIKeyID: 5}, false)
	check(domain.RateLimitKey{OrganizationID: 2, APIKeyID: 6}, true)
	check(domain.RateLimitKey{OrganizationID: 2, APIKeyID: 7}, false)
}

func TestLocalRateLimiterUsesUpdatedConfiguration(t *testing.T) {
	repository := &rateLimitTestRepository{config: adminDomain.RateLimitUpdate{
		Keys: []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: adminDomain.RateLimitConfig{QPS: 1}}},
	}}
	limiter := NewLocalRateLimiter(repository)
	now := time.Unix(1_800_000_000, 0)
	key := domain.RateLimitKey{OrganizationID: 1, APIKeyID: 3}
	_, _ = limiter.Decide(context.Background(), key, now)
	repository.config.Keys[0].QPS = 2
	decision, err := limiter.Decide(context.Background(), key, now)
	if err != nil || !decision.Allowed {
		t.Fatalf("updated limit: decision=%+v err=%v", decision, err)
	}
}

func TestLocalRateLimiterAppliesGlobalLimitToLegacyKey(t *testing.T) {
	repository := &rateLimitTestRepository{missingKeyRow: true, config: adminDomain.RateLimitUpdate{
		Global: &adminDomain.RateLimitConfig{QPS: 1},
	}}
	limiter := NewLocalRateLimiter(repository)
	now := time.Unix(1_800_000_000, 0)
	for index, want := range []bool{true, false} {
		decision, err := limiter.Decide(context.Background(), domain.RateLimitKey{OrganizationID: 1, APIKeyID: 3}, now)
		if err != nil || decision.Allowed != want {
			t.Fatalf("request %d: decision=%+v err=%v", index, decision, err)
		}
	}
}

func TestRateLimitBucketsMatchTheirScope(t *testing.T) {
	first := domain.RateLimitKey{OrganizationID: 1, APIKeyID: 3}
	second := domain.RateLimitKey{OrganizationID: 1, APIKeyID: 4}
	third := domain.RateLimitKey{OrganizationID: 2, APIKeyID: 5}
	if rateLimitBucket("rate", "global", first) != rateLimitBucket("rate", "global", third) {
		t.Fatal("global counter must be shared across organizations")
	}
	if rateLimitBucket("rate", "organization", first) != rateLimitBucket("rate", "organization", second) || rateLimitBucket("rate", "organization", first) == rateLimitBucket("rate", "organization", third) {
		t.Fatal("organization counter must be shared only within an organization")
	}
	if rateLimitBucket("rate", "key", first) == rateLimitBucket("rate", "key", second) {
		t.Fatal("key counters must remain separate")
	}
}

func TestLocalRateLimiterReportsLongestExhaustedWindow(t *testing.T) {
	quota := int64(1)
	repository := &rateLimitTestRepository{config: adminDomain.RateLimitUpdate{
		Global: &adminDomain.RateLimitConfig{QPS: 1},
		Keys: []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: adminDomain.RateLimitConfig{
			QPS: 1, Quota5H: &quota, Quota1D: &quota, Quota7D: &quota,
		}}},
	}}
	limiter := NewLocalRateLimiter(repository)
	now := time.Unix(1_800_000_000, 0)
	key := domain.RateLimitKey{OrganizationID: 1, APIKeyID: 3}
	first, err := limiter.Decide(t.Context(), key, now)
	if err != nil || !first.Allowed {
		t.Fatalf("first request: %+v err=%v", first, err)
	}
	denied, err := limiter.Decide(t.Context(), key, now.Add(time.Millisecond))
	if err != nil || denied.Allowed || denied.Reason != domain.RateLimitQuota7D || denied.RetryAfter != 7*24*time.Hour-time.Millisecond {
		t.Fatalf("exhausted windows: %+v err=%v", denied, err)
	}
	other, err := limiter.Decide(t.Context(), domain.RateLimitKey{OrganizationID: 2, APIKeyID: 4}, now.Add(time.Second))
	if err != nil || !other.Allowed {
		t.Fatalf("unrelated key blocked after QPS reset: %+v err=%v", other, err)
	}
	reset, err := limiter.Decide(t.Context(), key, now.Add(7*24*time.Hour))
	if err != nil || !reset.Allowed {
		t.Fatalf("request after all resets: %+v err=%v", reset, err)
	}
}
