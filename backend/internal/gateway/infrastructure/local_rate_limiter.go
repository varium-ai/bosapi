package infrastructure

import (
	"context"
	"strconv"
	"sync"
	"time"

	adminDomain "bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/gateway/domain"
)

type localRateCounter struct {
	count   int64
	expires time.Time
}

// LocalRateLimiter enforces persisted limits in a single gateway process.
type LocalRateLimiter struct {
	repository adminDomain.RateLimitRepository
	mu         sync.Mutex
	counters   map[string]localRateCounter
	lastSweep  time.Time
}

func NewLocalRateLimiter(repository adminDomain.RateLimitRepository) *LocalRateLimiter {
	return &LocalRateLimiter{repository: repository, counters: make(map[string]localRateCounter)}
}

func (limiter *LocalRateLimiter) Decide(ctx context.Context, key domain.RateLimitKey, now time.Time) (domain.RateLimitDecision, error) {
	config, err := readGatewayRateLimits(ctx, limiter.repository, key)
	if err != nil {
		return domain.RateLimitDecision{}, err
	}
	rules := configuredRateLimitRules(config, key)
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.lastSweep.IsZero() || now.Sub(limiter.lastSweep) >= time.Minute {
		for bucket, counter := range limiter.counters {
			if !now.Before(counter.expires) {
				delete(limiter.counters, bucket)
			}
		}
		limiter.lastSweep = now
	}
	decision := domain.RateLimitDecision{Allowed: true, Degraded: true}
	for _, rule := range rules {
		if rule.limit <= 0 {
			continue
		}
		bucket := rateLimitBucket("", rule.name, key)
		counter := limiter.counters[bucket]
		if !now.Before(counter.expires) {
			counter = localRateCounter{}
		}
		if counter.count >= rule.limit {
			retry := counter.expires.Sub(now)
			// Wait until every exhausted rule can admit a request.
			if !decision.Allowed && retry <= decision.RetryAfter {
				continue
			}
			decision.Allowed = false
			decision.Reason = rule.reason()
			decision.Limit = rule.limit
			decision.Remaining = 0
			decision.RetryAfter = retry
		}
	}
	if !decision.Allowed {
		return decision, nil
	}
	for _, rule := range rules {
		if rule.limit <= 0 {
			continue
		}
		bucket := rateLimitBucket("", rule.name, key)
		counter := limiter.counters[bucket]
		if !now.Before(counter.expires) {
			counter = localRateCounter{expires: now.Add(rule.window)}
		}
		counter.count++
		limiter.counters[bucket] = counter
		if decision.Limit == 0 || rule.limit-counter.count < decision.Remaining {
			decision.Limit = rule.limit
			decision.Remaining = rule.limit - counter.count
		}
	}
	return decision, nil
}

func (limiter *LocalRateLimiter) Allow(ctx context.Context, key domain.RateLimitKey, now time.Time) (bool, error) {
	decision, err := limiter.Decide(ctx, key, now)
	return decision.Allowed, err
}

func (*LocalRateLimiter) Record(context.Context, domain.RateLimitKey, time.Time) error { return nil }

func rateLimitBucket(prefix, name string, key domain.RateLimitKey) string {
	switch name {
	case "global":
		return prefix + ":global"
	case "organization":
		return prefix + ":organization:" + strconv.FormatUint(key.OrganizationID, 10)
	default:
		return prefix + ":" + name + ":" + strconv.FormatUint(key.APIKeyID, 10)
	}
}
