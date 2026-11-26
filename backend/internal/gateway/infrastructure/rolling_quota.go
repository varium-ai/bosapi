package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	adminDomain "bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/gateway/domain"
)

const rollingUsageRetention = 8 * 24 * time.Hour

func validRollingUsage(tokens int64, amount float64) bool {
	return tokens >= 0 && amount >= 0 && !math.IsNaN(amount) && !math.IsInf(amount, 0)
}

func saturatingUsageAdd(left, right int64) int64 {
	if right > math.MaxInt64-left {
		return math.MaxInt64
	}
	return left + right
}

// RecordTokenUsage keeps billing retries idempotent while retaining only enabled
// quota histories. A deleted key has no live rolling budget left to maintain.
func (limiter *RedisRateLimiter) RecordTokenUsage(ctx context.Context, keyID uint64, usageID string, tokens int64, amount float64, at time.Time) error {
	if limiter.client == nil || limiter.repository == nil || keyID == 0 || usageID == "" || !validRollingUsage(tokens, amount) {
		return errors.New("invalid token usage")
	}
	if tokens == 0 && amountInt(amount) == 0 {
		return nil
	}
	config, err := limiter.repository.ReadRateLimits(ctx, 0, keyID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if at.IsZero() {
		at = time.Now()
	}
	keys, args := limiter.rollingUsageArguments(keyID, config, tokens, amount)
	if len(keys) == 1 {
		return nil
	}
	digest := sha256.Sum256([]byte(usageID))
	identity := hex.EncodeToString(digest[:])
	keys[len(keys)-1] += identity
	args = append(args, identity, at.UnixMilli(), time.Now().UnixMilli(), rollingUsageRetention.Milliseconds())
	return recordTokenWindowUsageScript.Run(ctx, limiter.client, keys, args...).Err()
}

func (limiter *RedisRateLimiter) rollingUsageArguments(keyID uint64, config adminDomain.RateLimitUpdate, tokens int64, amount float64) ([]string, []any) {
	key := domain.RateLimitKey{APIKeyID: keyID}
	var keys []string
	var args []any
	for _, rule := range configuredRateLimitRules(config, key) {
		if rule.limit <= 0 {
			continue
		}
		bucket := rateLimitBucket(limiter.prefix, rule.name, key)
		keys = append(keys, bucket+":v2", bucket+":state", bucket)
		value := tokens
		if strings.HasSuffix(rule.name, "_amount") {
			value = amountInt(amount)
		}
		args = append(args, value, rule.window.Milliseconds())
	}
	keys = append(keys, limiter.prefix+":{tokens}:seen:"+strconv.FormatUint(keyID, 10)+":")
	return keys, args
}

// Redis Lua numbers cannot represent all int64 values. Counters therefore use
// decimal strings; only millisecond timestamps and single digits use doubles.
const rollingQuotaLua = `
local retention = 691200000
local maximum = '9223372036854775807'
local function normalize(value)
  local result = string.gsub(value or '0', '^0+', '')
  return result == '' and '0' or result
end
local function less(left, right)
  left, right = normalize(left), normalize(right)
  if #left ~= #right then return #left < #right end
  return left < right
end
local function add(left, right)
  local result, carry = '', 0
  local i, j = #left, #right
  while i > 0 or j > 0 or carry > 0 do
    local a = i > 0 and tonumber(string.sub(left, i, i)) or 0
    local b = j > 0 and tonumber(string.sub(right, j, j)) or 0
    local total = a + b + carry
    result = tostring(total % 10) .. result
    carry = math.floor(total / 10)
    i, j = i - 1, j - 1
  end
  if less(maximum, result) then return maximum end
  return result
end
local function subtract(left, right)
  local result, borrow, j = '', 0, #right
  for i = #left, 1, -1 do
    local b = j > 0 and tonumber(string.sub(right, j, j)) or 0
    local digit = tonumber(string.sub(left, i, i)) - b - borrow
    borrow = digit < 0 and 1 or 0
    result = tostring((digit + 10) % 10) .. result
    j = j - 1
  end
  return normalize(result)
end
local function advance(anchor, at, window)
  if anchor == 0 or at >= anchor + window then return at end
  return anchor
end
local function summarize(key, state, window, untilTime)
  local anchor = tonumber(redis.call('HGET', state, 'base')) or 0
  local current, last = '0', 0
  local entries = redis.call('ZRANGEBYSCORE', key, '-inf', untilTime, 'WITHSCORES')
  for i = 1, #entries, 2 do
    local at = tonumber(entries[i + 1])
    local nextAnchor = advance(anchor, at, window)
    if nextAnchor ~= anchor then current = '0' end
    anchor, last = nextAnchor, at
    current = add(current, string.match(entries[i], ':(%d+)$'))
  end
  return anchor, current, last
end
local function rebuild(key, state, window)
  local anchor, current, last = summarize(key, state, window, '+inf')
  redis.call('HSET', state, 'anchor', anchor, 'current', current, 'last', last)
end
local function prune(key, state, window, now)
  local expired = redis.call('ZRANGEBYSCORE', key, '-inf', now - retention, 'WITHSCORES')
  if #expired == 0 then return false end
  local base = tonumber(redis.call('HGET', state, 'base')) or 0
  for i = 1, #expired, 2 do
    base = advance(base, tonumber(expired[i + 1]), window)
    redis.call('ZREM', key, expired[i])
  end
  redis.call('HSET', state, 'base', base)
  return true
end
local function prepare(key, state, legacy, window, now)
  if redis.call('HEXISTS', state, 'version') == 1 and redis.call('EXISTS', legacy) == 0 then return end
  -- Import existing six-decimal records once, before serving the first decision.
  -- Preserve the old deduplication key so ambiguous commits remain idempotent.
  local entries = redis.call('ZRANGE', legacy, 0, -1, 'WITHSCORES')
  local amountMetric = string.find(legacy, '_amount:') ~= nil
  for i = 1, #entries, 2 do
    local identity, tokens, amount = string.match(entries[i], '^(.*):(%d+):(%d+)$')
    if identity then
      local value = amountMetric and normalize(amount .. '00') or tokens
      redis.call('ZADD', key, entries[i + 1], identity .. ':' .. value)
    end
  end
  if #entries == 0 and redis.call('EXISTS', key) == 0 then return end
  prune(key, state, window, now)
  rebuild(key, state, window)
  redis.call('HSET', state, 'version', '2')
  redis.call('PEXPIRE', key, retention)
  redis.call('PEXPIRE', state, retention)
  redis.call('DEL', legacy, legacy .. ':anchor')
end
`

var rateLimitScript = redis.NewScript(rollingQuotaLua + `
local deniedIndex, retryAfter, tightestIndex = 0, 0, 0
local remaining = '0'
local now = tonumber(ARGV[#ARGV])
for index = 1, #KEYS / 3 do
  local offset = (index - 1) * 3
  local key, state, legacy = KEYS[offset + 1], KEYS[offset + 2], KEYS[offset + 3]
  local limit = ARGV[(index - 1) * 2 + 1]
  local window = tonumber(ARGV[(index - 1) * 2 + 2])
  prepare(key, state, legacy, window, now)
  local anchor = tonumber(redis.call('HGET', state, 'anchor')) or 0
  local current = redis.call('HGET', state, 'current') or '0'
  local last = tonumber(redis.call('HGET', state, 'last')) or 0
  if last > now then anchor, current = summarize(key, state, window, now) end
  if anchor == 0 or now >= anchor + window then current = '0' end
  if not less(current, limit) then
    local retry = math.max(1, anchor + window - now)
    if deniedIndex == 0 or retry > retryAfter then
      deniedIndex, retryAfter = index, retry
    end
  else
    local available = subtract(limit, current)
    if tightestIndex == 0 or less(available, remaining) then
      tightestIndex, remaining = index, available
    end
  end
end
if deniedIndex > 0 then return {0, deniedIndex, '0', retryAfter} end
return {1, tightestIndex, remaining, 0}
`)

var recordTokenWindowUsageScript = redis.NewScript(rollingQuotaLua + `
if redis.call('EXISTS', KEYS[#KEYS]) == 1 then return 0 end
local identity = ARGV[#ARGV - 3]
local at, now = tonumber(ARGV[#ARGV - 2]), tonumber(ARGV[#ARGV - 1])
local ttl = tonumber(ARGV[#ARGV])
local reference = math.max(at, now)
-- Both backends ignore receipts outside the retained replay horizon.
if at <= reference - retention then return 0 end
for index = 1, (#KEYS - 1) / 3 do
  local offset = (index - 1) * 3
  local key, state, legacy = KEYS[offset + 1], KEYS[offset + 2], KEYS[offset + 3]
  local value = ARGV[(index - 1) * 2 + 1]
  local window = tonumber(ARGV[(index - 1) * 2 + 2])
  prepare(key, state, legacy, window, reference)
  local pruned = prune(key, state, window, reference)
  local last = tonumber(redis.call('HGET', state, 'last')) or 0
  redis.call('ZADD', key, at, identity .. ':' .. value)
  if pruned or at < last then
    rebuild(key, state, window)
  else
    local anchor = tonumber(redis.call('HGET', state, 'anchor')) or 0
    local current = redis.call('HGET', state, 'current') or '0'
    local nextAnchor = advance(anchor, at, window)
    if nextAnchor ~= anchor then current = '0' end
    redis.call('HSET', state, 'anchor', nextAnchor, 'current', add(current, value), 'last', at)
  end
  redis.call('HSET', state, 'version', '2')
  redis.call('PEXPIRE', key, ttl)
  redis.call('PEXPIRE', state, ttl)
end
redis.call('SET', KEYS[#KEYS], '1', 'PX', ttl)
return 1
`)
