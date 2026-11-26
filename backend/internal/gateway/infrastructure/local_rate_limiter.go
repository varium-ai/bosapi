package infrastructure

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	adminDomain "bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/gateway/domain"
)

type localTokenUsage struct {
	at           time.Time
	tokens       int64
	amountMicros int64
}

// LocalRateLimiter enforces persisted limits in a single gateway process.
type LocalRateLimiter struct {
	repository adminDomain.RateLimitRepository
	mu         sync.Mutex
	tokens     map[uint64][]localTokenUsage
	tokenSeen  map[string]time.Time
	lastSweep  time.Time
	active     map[string]int64
}

func NewLocalRateLimiter(repository adminDomain.RateLimitRepository) *LocalRateLimiter {
	return &LocalRateLimiter{repository: repository, tokens: make(map[uint64][]localTokenUsage), tokenSeen: make(map[string]time.Time), active: make(map[string]int64)}
}

func (limiter *LocalRateLimiter) AcquireSession(ctx context.Context, key domain.RateLimitKey) (domain.RateLimitDecision, func(), error) {
	config, err := readGatewayRateLimits(ctx, limiter.repository, key)
	if err != nil {
		return domain.RateLimitDecision{Allowed: true, Degraded: true}, func() {}, nil
	}
	rules := configuredConcurrencyRules(config, key)
	limiter.mu.Lock()
	for _, rule := range rules {
		if rule.limit > 0 && limiter.active[rateLimitBucket("", rule.name, key)] >= rule.limit {
			limiter.mu.Unlock()
			return domain.RateLimitDecision{Allowed: false, Reason: rule.reason(), Limit: rule.limit, RetryAfter: time.Second, Degraded: true}, func() {}, nil
		}
	}
	var buckets []string
	for _, rule := range rules {
		if rule.limit > 0 {
			b := rateLimitBucket("", rule.name, key)
			limiter.active[b]++
			buckets = append(buckets, b)
		}
	}
	limiter.mu.Unlock()
	var once sync.Once
	return domain.RateLimitDecision{Allowed: true, Degraded: true}, func() {
		once.Do(func() {
			limiter.mu.Lock()
			defer limiter.mu.Unlock()
			for _, b := range buckets {
				if limiter.active[b] <= 1 {
					delete(limiter.active, b)
				} else {
					limiter.active[b]--
				}
			}
		})
	}, nil
}

func (limiter *LocalRateLimiter) Decide(ctx context.Context, key domain.RateLimitKey, now time.Time) (domain.RateLimitDecision, error) {
	config, err := readGatewayRateLimits(ctx, limiter.repository, key)
	if err != nil {
		return domain.RateLimitDecision{}, err
	}
	tokenRules := configuredRateLimitRules(config, key)
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.lastSweep.IsZero() || now.Sub(limiter.lastSweep) >= time.Minute {
		cutoff := now.Add(-8 * 24 * time.Hour)
		for id, events := range limiter.tokens {
			kept := events[:0]
			for _, event := range events {
				if event.at.After(cutoff) {
					kept = append(kept, event)
				}
			}
			limiter.tokens[id] = kept
		}
		for id, expires := range limiter.tokenSeen {
			if !now.Before(expires) {
				delete(limiter.tokenSeen, id)
			}
		}
		limiter.lastSweep = now
	}
	decision := domain.RateLimitDecision{Allowed: true, Degraded: true}
	considerDenial := func(reason domain.RateLimitReason, limit, remaining int64, retry time.Duration) {
		if decision.Allowed || retry > decision.RetryAfter {
			decision.Allowed, decision.Reason, decision.Limit, decision.Remaining, decision.RetryAfter = false, reason, limit, remaining, retry
		}
	}
	for _, rule := range tokenRules {
		if rule.limit <= 0 {
			continue
		}
		events := append([]localTokenUsage(nil), limiter.tokens[key.APIKeyID]...)
		sort.Slice(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })
		current := int64(0)
		var anchor time.Time
		for _, event := range events {
			if event.at.After(now) {
				continue
			}
			if anchor.IsZero() || !event.at.Before(anchor.Add(rule.window)) {
				anchor, current = event.at, 0
			}
			if strings.HasSuffix(rule.name, "_amount") {
				current += event.amountMicros
			} else {
				current += event.tokens
			}
		}
		if !anchor.IsZero() && !now.Before(anchor.Add(rule.window)) {
			current = 0
		}
		if current >= rule.limit {
			considerDenial(rule.reason(), rule.limit, 0, anchor.Add(rule.window).Sub(now))
			if !decision.Allowed && decision.Reason == rule.reason() {
				decision.Amount = strings.HasSuffix(rule.name, "_amount")
			}
		}
	}
	if !decision.Allowed {
		return decision, nil
	}
	return decision, nil
}

// RecordTokenUsage adds settled token usage once to the local rolling windows.
func (limiter *LocalRateLimiter) RecordTokenUsage(ctx context.Context, keyID uint64, usageID string, tokens int64, amount float64, at time.Time) error {
	if keyID == 0 || usageID == "" || tokens < 0 || amount < 0 {
		return errors.New("invalid token usage")
	}
	config, err := readGatewayRateLimits(ctx, limiter.repository, domain.RateLimitKey{APIKeyID: keyID})
	if err != nil {
		return err
	}
	if len(configuredRateLimitRules(config, domain.RateLimitKey{APIKeyID: keyID})) == 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	seenKey := strconv.FormatUint(keyID, 10) + ":" + usageID
	if expires, seen := limiter.tokenSeen[seenKey]; seen && at.Before(expires) {
		return nil
	}
	limiter.tokenSeen[seenKey] = at.Add(8 * 24 * time.Hour)
	limiter.tokens[keyID] = append(limiter.tokens[keyID], localTokenUsage{at: at, tokens: tokens, amountMicros: int64(math.Round(amount * 1_000_000))})
	return nil
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
	case "quota_5h", "quota_1d", "quota_7d", "quota_5h_amount", "quota_1d_amount", "quota_7d_amount":
		return prefix + ":{tokens}:" + name + ":" + strconv.FormatUint(key.APIKeyID, 10)
	default:
		return prefix + ":" + name + ":" + strconv.FormatUint(key.APIKeyID, 10)
	}
}
