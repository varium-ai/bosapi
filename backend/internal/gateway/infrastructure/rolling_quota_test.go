package infrastructure

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	adminDomain "bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/gateway/domain"
)

type rollingQuotaLimiter interface {
	RecordTokenUsage(context.Context, uint64, string, int64, float64, time.Time) error
	Decide(context.Context, domain.RateLimitKey, time.Time) (domain.RateLimitDecision, error)
}

func rollingQuotaFixtures(t *testing.T, config adminDomain.RateLimitConfig) (map[string]rollingQuotaLimiter, *redis.Client) {
	t.Helper()
	client := startSessionRenewRedis(t)
	repository := &rateLimitTestRepository{config: adminDomain.RateLimitUpdate{Keys: []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: config}}}}
	return map[string]rollingQuotaLimiter{
		"local": NewLocalRateLimiter(repository),
		"redis": NewRedisRateLimiter(client, repository, "test:rolling"),
	}, client
}

func TestRollingQuotaPreservesLedgerAmountPrecision(t *testing.T) {
	limit := .01
	limiters, _ := rollingQuotaFixtures(t, adminDomain.RateLimitConfig{Quota5HAmount: &limit})
	for name, limiter := range limiters {
		t.Run(name, func(t *testing.T) {
			at := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
			for i, amount := range []float64{.009999, .0000002, .0000002, .0000002, .0000002, .0000002, .0000002} {
				if err := limiter.RecordTokenUsage(t.Context(), 3, fmt.Sprint(i), 1, amount, at); err != nil {
					t.Fatal(err)
				}
			}
			decision, err := limiter.Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, at.Add(time.Minute))
			if err != nil || decision.Allowed || !decision.Amount || decision.Limit != 1_000_000 {
				t.Fatalf("ledger total $0.01000020 must exhaust $0.01: %+v err=%v", decision, err)
			}
		})
	}
}

func TestRollingQuotaOutOfOrderAndRetry(t *testing.T) {
	for _, duration := range []time.Duration{5 * time.Hour, 24 * time.Hour, 7 * 24 * time.Hour} {
		t.Run(duration.String(), func(t *testing.T) {
			limit := int64(10)
			config := adminDomain.RateLimitConfig{}
			switch duration {
			case 5 * time.Hour:
				config.Quota5H = &limit
			case 24 * time.Hour:
				config.Quota1D = &limit
			default:
				config.Quota7D = &limit
			}
			limiters, _ := rollingQuotaFixtures(t, config)
			now := time.Now().Truncate(time.Millisecond)
			for name, limiter := range limiters {
				t.Run(name, func(t *testing.T) {
					for _, receipt := range []struct {
						id string
						at time.Time
					}{{"later", now.Add(-time.Hour)}, {"earlier", now.Add(-2 * time.Hour)}, {"earlier", now.Add(-2 * time.Hour)}} {
						if err := limiter.RecordTokenUsage(t.Context(), 3, receipt.id, 5, 0, receipt.at); err != nil {
							t.Fatal(err)
						}
					}
					decision, err := limiter.Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, now)
					if err != nil || decision.Allowed || decision.RetryAfter != duration-2*time.Hour {
						t.Fatalf("out-of-order decision=%+v err=%v", decision, err)
					}
					boundary := now.Add(-2 * time.Hour).Add(duration)
					decision, err = limiter.Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, boundary)
					if err != nil || !decision.Allowed {
						t.Fatalf("window must expire at exclusive upper boundary: %+v err=%v", decision, err)
					}
				})
			}
		})
	}
}

func TestRollingQuotaLateReceiptRebuildsFollowingWindows(t *testing.T) {
	limit := int64(10)
	limiters, _ := rollingQuotaFixtures(t, adminDomain.RateLimitConfig{Quota5H: &limit})
	now := time.Now().Truncate(time.Millisecond)
	for name, limiter := range limiters {
		t.Run(name, func(t *testing.T) {
			for i, offset := range []time.Duration{-6 * time.Hour, -time.Hour, -2 * time.Hour, -7 * time.Hour} {
				if err := limiter.RecordTokenUsage(t.Context(), 3, fmt.Sprint(i), 5, 0, now.Add(offset)); err != nil {
					t.Fatal(err)
				}
				decision, err := limiter.Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, now)
				if err != nil || decision.Allowed != (i < 3) {
					t.Fatalf("receipt %d decision=%+v err=%v", i, decision, err)
				}
			}
		})
	}
}

func TestRollingQuotaRedisAndLocalAgreeForShuffledReceipts(t *testing.T) {
	limit, amount := int64(45), .000001
	limiters, _ := rollingQuotaFixtures(t, adminDomain.RateLimitConfig{Quota5H: &limit, Quota1D: &limit, Quota7D: &limit, Quota5HAmount: &amount, Quota1DAmount: &amount, Quota7DAmount: &amount})
	now := time.Now().Truncate(time.Millisecond)
	order := rand.New(rand.NewSource(17)).Perm(100)
	for _, i := range order {
		at := now.Add(-time.Duration(i) * time.Hour)
		for _, limiter := range limiters {
			if err := limiter.RecordTokenUsage(t.Context(), 3, fmt.Sprint(i), int64(i%7), .00000002, at); err != nil {
				t.Fatal(err)
			}
		}
		local, err := limiters["local"].Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, now)
		if err != nil {
			t.Fatal(err)
		}
		remote, err := limiters["redis"].Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, now)
		if err != nil || remote.Degraded || local.Allowed != remote.Allowed || local.Reason != remote.Reason || !local.Allowed && (local.Amount != remote.Amount || local.Limit != remote.Limit) || local.RetryAfter != remote.RetryAfter {
			t.Fatalf("receipt=%d local=%+v redis=%+v err=%v", i, local, remote, err)
		}
	}
}

func TestRollingQuotaRedisUsesOnlyEnabledHistoryAndExpiresAllData(t *testing.T) {
	limit := int64(10)
	limiters, client := rollingQuotaFixtures(t, adminDomain.RateLimitConfig{Quota5H: &limit})
	limiter := limiters["redis"]
	if err := limiter.RecordTokenUsage(t.Context(), 3, "receipt", 10, 0, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	keys, err := client.Keys(t.Context(), "test:rolling:*").Result()
	if err != nil || len(keys) != 3 {
		t.Fatalf("one configured metric needs one history, one summary and one receipt: keys=%v err=%v", keys, err)
	}
	for _, key := range keys {
		ttl, err := client.PTTL(t.Context(), key).Result()
		if err != nil || ttl <= 0 || ttl > rollingUsageRetention {
			t.Fatalf("unbounded key %s ttl=%v err=%v", key, ttl, err)
		}
	}
	// A warm admission must read only the summary, even if history is unavailable.
	history := "test:rolling:{tokens}:quota_5h:3:v2"
	if err := client.Set(t.Context(), history, "unreadable history", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	decision, err := limiter.Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, time.Now())
	if err != nil || decision.Allowed || decision.Degraded {
		t.Fatalf("admission scanned history instead of using summary: %+v err=%v", decision, err)
	}
}

func TestRollingQuotaMigratesLegacyAmountsWithoutResettingBudget(t *testing.T) {
	limit := .01
	limiters, client := rollingQuotaFixtures(t, adminDomain.RateLimitConfig{Quota5HAmount: &limit})
	key := "test:rolling:{tokens}:quota_5h_amount:3"
	if err := client.ZAdd(t.Context(), key, redis.Z{Score: float64(time.Now().Add(-time.Hour).UnixMilli()), Member: "request:usage:3:10000"}).Err(); err != nil {
		t.Fatal(err)
	}
	decision, err := limiters["redis"].Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, time.Now())
	if err != nil || decision.Allowed || decision.Degraded || decision.Limit != 1_000_000 {
		t.Fatalf("legacy budget reset or used wrong units: %+v err=%v", decision, err)
	}
	if count := client.Exists(t.Context(), key).Val(); count != 0 {
		t.Fatal("migrated unbounded history was not removed")
	}
}

func TestRollingQuotaImportsReceiptsFromOlderGatewayDuringUpgrade(t *testing.T) {
	limit := int64(10)
	limiters, client := rollingQuotaFixtures(t, adminDomain.RateLimitConfig{Quota5H: &limit})
	limiter := limiters["redis"]
	if err := limiter.RecordTokenUsage(t.Context(), 3, "new-gateway", 5, 0, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	legacy := "test:rolling:{tokens}:quota_5h:3"
	if err := client.ZAdd(t.Context(), legacy, redis.Z{Score: float64(time.Now().Add(-time.Minute).UnixMilli()), Member: "old-gateway:usage:5:0"}).Err(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		decision, err := limiter.Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, time.Now())
		if err != nil || decision.Allowed || decision.Degraded {
			t.Fatalf("older gateway's receipt was lost during upgrade: %+v err=%v", decision, err)
		}
	}
}

func TestRollingQuotaIgnoresZeroUsageAndExpiredReceipts(t *testing.T) {
	limit := int64(10)
	limiters, client := rollingQuotaFixtures(t, adminDomain.RateLimitConfig{Quota5H: &limit})
	for name, limiter := range limiters {
		t.Run(name, func(t *testing.T) {
			if err := limiter.RecordTokenUsage(t.Context(), 3, "zero", 0, 0, time.Time{}); err != nil {
				t.Fatal(err)
			}
			if err := limiter.RecordTokenUsage(t.Context(), 3, "expired", 10, 0, time.Now().Add(-rollingUsageRetention-time.Hour)); err != nil {
				t.Fatal(err)
			}
			decision, err := limiter.Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, time.Now())
			if err != nil || !decision.Allowed {
				t.Fatalf("expired or empty usage consumed live budget: %+v err=%v", decision, err)
			}
		})
	}
	if keys := client.Keys(t.Context(), "test:rolling:*").Val(); len(keys) != 0 {
		t.Fatalf("empty or expired usage created retained state: %v", keys)
	}
}

func TestRollingQuotaInt64CountersAndInvalidAmounts(t *testing.T) {
	limit := int64(math.MaxInt64)
	limiters, _ := rollingQuotaFixtures(t, adminDomain.RateLimitConfig{Quota5H: &limit})
	for name, limiter := range limiters {
		t.Run(name, func(t *testing.T) {
			for _, amount := range []float64{math.NaN(), math.Inf(1), -.1} {
				if err := limiter.RecordTokenUsage(t.Context(), 3, "invalid", 1, amount, time.Now()); err == nil {
					t.Fatalf("accepted amount %v", amount)
				}
			}
			at := time.Now().Add(-time.Minute)
			for i, value := range []int64{limit - 1, 2} {
				if err := limiter.RecordTokenUsage(t.Context(), 3, fmt.Sprint(i), value, 0, at); err != nil {
					t.Fatal(err)
				}
				decision, err := limiter.Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, time.Now())
				if err != nil || decision.Allowed != (i == 0) {
					t.Fatalf("counter lost integer precision or overflowed: %+v err=%v", decision, err)
				}
			}
		})
	}
}

func TestRollingQuotaLegacyCleanupIncludesDeletedKeys(t *testing.T) {
	limit := int64(10)
	limiters, client := rollingQuotaFixtures(t, adminDomain.RateLimitConfig{Quota5H: &limit})
	legacy := "test:rolling:{tokens}:quota_7d:999"
	if err := client.ZAdd(t.Context(), legacy, redis.Z{Score: 1, Member: "receipt:1:0"}).Err(); err != nil {
		t.Fatal(err)
	}
	newHistory := legacy + ":v2"
	if err := client.Set(t.Context(), newHistory, "untouched", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	limiter := limiters["redis"].(*RedisRateLimiter)
	if err := limiter.expireLegacyWindows(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ttl := client.PTTL(t.Context(), legacy).Val(); ttl <= 0 || ttl > rollingUsageRetention {
		t.Fatalf("deleted key's legacy data remains unbounded: %v", ttl)
	}
	if ttl := client.PTTL(t.Context(), newHistory).Val(); ttl > time.Hour || ttl < 59*time.Minute {
		t.Fatalf("cleanup changed new data: %v", ttl)
	}
	limiter.StartLegacyCleanup()
	limiter.Close()
}

func TestRollingQuotaPrunesOldHistoryButPreservesWindowAnchors(t *testing.T) {
	limit := int64(10)
	limiters, client := rollingQuotaFixtures(t, adminDomain.RateLimitConfig{Quota7D: &limit})
	limiter := limiters["redis"]
	now := time.Now().Truncate(time.Millisecond)
	// Seed a legacy stream crossing the retention cutoff; the next window is
	// anchored on day seven, not on the first retained record on day two.
	legacy := "test:rolling:{tokens}:quota_7d:3"
	for i, hours := range []int{9 * 24, 7*24 + 1, 2 * 24, 24} {
		if err := client.ZAdd(t.Context(), legacy, redis.Z{Score: float64(now.Add(-time.Duration(hours) * time.Hour).UnixMilli()), Member: fmt.Sprintf("receipt%d:5:0", i)}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	decision, err := limiter.Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, now)
	if err != nil || decision.Allowed || decision.Degraded || decision.RetryAfter != 5*24*time.Hour {
		t.Fatalf("pruning moved the live anchor: %+v err=%v", decision, err)
	}
	entries, err := client.ZRange(t.Context(), legacy+":v2", 0, -1).Result()
	if err != nil || len(entries) != 3 || slices.Contains(entries, "receipt0:5") {
		t.Fatalf("expired history retained: %v err=%v", entries, err)
	}
}
