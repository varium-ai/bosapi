package infrastructure

import (
	"context"
	"strconv"
	"strings"
	"time"

	"bosapi/backend/internal/platform/logging"
)

// StartLegacyCleanup adds expiry to old unbounded ZSETs, including deleted keys
// that will never be touched by lazy migration. SCAN keeps startup nonblocking.
func (limiter *RedisRateLimiter) StartLegacyCleanup() {
	limiter.cleanupOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		limiter.cleanupCancel, limiter.cleanupDone = cancel, make(chan struct{})
		go func() {
			defer close(limiter.cleanupDone)
			for {
				if err := limiter.expireLegacyWindows(ctx); err == nil || ctx.Err() != nil {
					return
				} else {
					logging.Printf(ctx, "WARN", "gateway.rate_limit.legacy_cleanup_failed", "err=%v", err)
				}
				timer := time.NewTimer(5 * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	})
}

// Close stops the migration worker before the shared Redis client is closed.
func (limiter *RedisRateLimiter) Close() {
	if limiter.cleanupCancel != nil {
		limiter.cleanupCancel()
		<-limiter.cleanupDone
	}
}

func (limiter *RedisRateLimiter) expireLegacyWindows(ctx context.Context) error {
	var cursor uint64
	for {
		var keys []string
		var err error
		op, cancel := context.WithTimeout(ctx, 2*time.Second)
		keys, cursor, err = limiter.client.Scan(op, cursor, limiter.prefix+":{tokens}:quota_*", 128).Result()
		cancel()
		if err != nil {
			return err
		}
		for _, key := range keys {
			if !limiter.isLegacyWindow(key) {
				continue
			}
			op, cancel := context.WithTimeout(ctx, time.Second)
			ttl, err := limiter.client.PTTL(op, key).Result()
			if err == nil && ttl == -1 {
				err = limiter.client.PExpire(op, key, rollingUsageRetention).Err()
			}
			cancel()
			if err != nil {
				return err
			}
		}
		if cursor == 0 {
			return nil
		}
	}
}

func (limiter *RedisRateLimiter) isLegacyWindow(key string) bool {
	rest := strings.TrimPrefix(key, limiter.prefix+":{tokens}:")
	parts := strings.Split(rest, ":")
	if len(parts) != 2 {
		return false
	}
	switch parts[0] {
	case "quota_5h", "quota_1d", "quota_7d", "quota_5h_amount", "quota_1d_amount", "quota_7d_amount":
		_, err := strconv.ParseUint(parts[1], 10, 64)
		return err == nil
	default:
		return false
	}
}
