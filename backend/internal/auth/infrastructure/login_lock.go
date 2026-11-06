package infrastructure

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	authDomain "bosapi/backend/internal/auth/domain"
	"github.com/redis/go-redis/v9"
)

const defaultLoginLockPrefix = "bos:auth:login-lock"

var loginLockScript = redis.NewScript(`
local lockKey = KEYS[1]
local failuresKey = KEYS[2]
local threshold = tonumber(ARGV[1])
local windowMs = tonumber(ARGV[2])
local lockMs = tonumber(ARGV[3])
local member = ARGV[4]
local now = tonumber(ARGV[5])

redis.call('ZREMRANGEBYSCORE', failuresKey, '-inf', now - windowMs)
local lockTTL = redis.call('PTTL', lockKey)
if lockTTL > 0 then
  return {1, lockTTL}
end

redis.call('ZADD', failuresKey, now, member)
redis.call('PEXPIRE', failuresKey, windowMs)
local failures = redis.call('ZCARD', failuresKey)
if failures >= threshold then
  redis.call('SET', lockKey, '1', 'PX', lockMs)
  redis.call('DEL', failuresKey)
  return {1, lockMs}
end
return {0, 0}
`)

// LoginLockStore maintains temporary login lockouts in shared Redis state.
type LoginLockStore struct {
	client    redis.UniversalClient
	prefix    string
	threshold int
	window    time.Duration
	lockTTL   time.Duration
}

// NewLoginLockStore constructs a shared login lockout adapter.
func NewLoginLockStore(client redis.UniversalClient, prefix string, threshold int, window, lockTTL time.Duration) *LoginLockStore {
	if prefix == "" {
		prefix = defaultLoginLockPrefix
	}
	return &LoginLockStore{
		client:    client,
		prefix:    prefix,
		threshold: threshold,
		window:    window,
		lockTTL:   lockTTL,
	}
}

// Check reports whether the normalized account is currently locked.
func (store *LoginLockStore) Check(ctx context.Context, key string, at time.Time) (authDomain.LoginLockDecision, error) {
	if store == nil || store.client == nil || key == "" {
		return authDomain.LoginLockDecision{}, nil
	}
	_ = at
	ttl, err := store.client.PTTL(ctx, store.lockKey(key)).Result()
	if err != nil {
		return authDomain.LoginLockDecision{}, err
	}
	if ttl > 0 {
		return authDomain.LoginLockDecision{Locked: true, RetryAfter: ttl}, nil
	}
	return authDomain.LoginLockDecision{}, nil
}

// RecordFailure adds one failed attempt and locks the account at the threshold.
func (store *LoginLockStore) RecordFailure(ctx context.Context, key string, at time.Time) (authDomain.LoginLockDecision, error) {
	if store == nil || store.client == nil || key == "" {
		return authDomain.LoginLockDecision{}, nil
	}
	entropy := make([]byte, 16)
	if _, err := rand.Read(entropy); err != nil {
		return authDomain.LoginLockDecision{}, err
	}
	member := fmt.Sprintf("%d:%s", at.UTC().UnixNano(), hex.EncodeToString(entropy))
	result, err := loginLockScript.Run(
		ctx,
		store.client,
		[]string{store.lockKey(key), store.failuresKey(key)},
		store.threshold,
		store.window.Milliseconds(),
		store.lockTTL.Milliseconds(),
		member,
		at.UTC().UnixMilli(),
	).Int64Slice()
	if err != nil {
		return authDomain.LoginLockDecision{}, err
	}
	if len(result) != 2 || result[0] < 0 || result[0] > 1 {
		return authDomain.LoginLockDecision{}, fmt.Errorf("invalid login lock decision")
	}
	if result[0] == 1 {
		return authDomain.LoginLockDecision{Locked: true, RetryAfter: time.Duration(result[1]) * time.Millisecond}, nil
	}
	return authDomain.LoginLockDecision{}, nil
}

// Unlock removes lock and failure state for the normalized account.
func (store *LoginLockStore) Unlock(ctx context.Context, key string) error {
	if store == nil || store.client == nil || key == "" {
		return nil
	}
	return store.client.Del(ctx, store.lockKey(key), store.failuresKey(key)).Err()
}

func (store *LoginLockStore) lockKey(key string) string {
	return store.prefix + ":lock:" + loginLockDigest(key)
}

func (store *LoginLockStore) failuresKey(key string) string {
	return store.prefix + ":failures:" + loginLockDigest(key)
}

func loginLockDigest(key string) string {
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:])
}
