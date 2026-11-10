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

func TestLocalRateLimiterEnforcesSessionConcurrencyAndReleases(t *testing.T) {
	repository := &rateLimitTestRepository{config: adminDomain.RateLimitUpdate{Keys: []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: adminDomain.RateLimitConfig{QPS: 1}}}}}
	limiter := NewLocalRateLimiter(repository)
	key := domain.RateLimitKey{OrganizationID: 1, APIKeyID: 3}
	first, release, err := limiter.AcquireSession(t.Context(), key)
	if err != nil || !first.Allowed || !first.Degraded {
		t.Fatalf("first acquire=%+v err=%v", first, err)
	}
	denied, _, err := limiter.AcquireSession(t.Context(), key)
	if err != nil || denied.Allowed || denied.Reason != domain.RateLimitKeyQPS {
		t.Fatalf("second acquire=%+v err=%v", denied, err)
	}
	release()
	third, releaseThird, err := limiter.AcquireSession(t.Context(), key)
	if err != nil || !third.Allowed {
		t.Fatalf("acquire after release=%+v err=%v", third, err)
	}
	releaseThird()
}

func TestLocalRateLimiterSharesGlobalAndOrganizationSessions(t *testing.T) {
	repository := &rateLimitTestRepository{config: adminDomain.RateLimitUpdate{Global: &adminDomain.RateLimitConfig{QPS: 2}, Organizations: []adminDomain.OrganizationRateLimit{{OrganizationID: 1, QPS: 1}}}}
	limiter := NewLocalRateLimiter(repository)
	_, release, err := limiter.AcquireSession(t.Context(), domain.RateLimitKey{OrganizationID: 1, APIKeyID: 3})
	if err != nil {
		t.Fatal(err)
	}
	denied, _, err := limiter.AcquireSession(t.Context(), domain.RateLimitKey{OrganizationID: 1, APIKeyID: 4})
	if err != nil || denied.Allowed || denied.Reason != domain.RateLimitOrganizationQPS {
		t.Fatalf("organization decision=%+v err=%v", denied, err)
	}
	allowed, releaseOther, err := limiter.AcquireSession(t.Context(), domain.RateLimitKey{OrganizationID: 2, APIKeyID: 5})
	if err != nil || !allowed.Allowed {
		t.Fatalf("other organization=%+v err=%v", allowed, err)
	}
	_, _, err = limiter.AcquireSession(t.Context(), domain.RateLimitKey{OrganizationID: 2, APIKeyID: 6})
	if err != nil {
		t.Fatal(err)
	}
	release()
	releaseOther()
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
	repository := &rateLimitTestRepository{missingKeyRow: true, config: adminDomain.RateLimitUpdate{Global: &adminDomain.RateLimitConfig{QPS: 1}}}
	limiter := NewLocalRateLimiter(repository)
	key := domain.RateLimitKey{OrganizationID: 1, APIKeyID: 3}
	first, release, err := limiter.AcquireSession(t.Context(), key)
	if err != nil || !first.Allowed {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, _, err := limiter.AcquireSession(t.Context(), key)
	if err != nil || second.Allowed || second.Reason != domain.RateLimitGlobalQPS {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	release()
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
