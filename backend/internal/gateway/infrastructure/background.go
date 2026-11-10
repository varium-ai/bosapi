package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	adminDomain "bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/gateway/domain"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/securitycontract"
	"bosapi/backend/internal/platform/securityevidence"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// NoopRateLimiter is the local fallback used when Redis is not configured.
type NoopRateLimiter struct{}

// Allow permits requests while distributed rate limiting is unavailable.
func (NoopRateLimiter) Allow(context.Context, domain.RateLimitKey, time.Time) (bool, error) {
	return true, nil
}

// Record ignores counters until a distributed backend is configured.
func (NoopRateLimiter) Record(context.Context, domain.RateLimitKey, time.Time) error { return nil }

// NoopSecurityEventSink drops events until an asynchronous sink is configured.
type NoopSecurityEventSink struct{}

// Publish ignores events until an asynchronous sink is configured.
func (NoopSecurityEventSink) Publish(context.Context, domain.SecurityEvent) error { return nil }

// Inspect permits requests when no security inspection backend is configured.
func (NoopSecurityEventSink) Inspect(context.Context, domain.SecurityInspection) (bool, error) {
	return true, nil
}

// DatabaseSecurityEventSink queues security metadata for best-effort local persistence.
type DatabaseSecurityEventSink struct {
	database *gorm.DB
	queue    chan domain.SecurityEvent
	done     chan struct{}
}

func NewDatabaseSecurityEventSink(database *gorm.DB, buffer int) *DatabaseSecurityEventSink {
	if buffer < 1 {
		buffer = 256
	}
	sink := &DatabaseSecurityEventSink{database: database, queue: make(chan domain.SecurityEvent, buffer), done: make(chan struct{})}
	go sink.run()
	return sink
}

func (sink *DatabaseSecurityEventSink) Publish(ctx context.Context, event domain.SecurityEvent) error {
	// No caller-owned slices or caller-provided snippets enter the async queue.
	event.Evidence = securityevidence.Normalize(event.Evidence)
	event.ClientIP = append([]byte(nil), event.ClientIP...)
	if event.ToolCall != nil {
		metadata := *event.ToolCall
		event.ToolCall = &metadata
	}
	select {
	case sink.queue <- event:
	default:
		ctx = httpapi.WithRequestID(ctx, event.RequestID)
		ctx = httpapi.WithLogMetadata(ctx, httpapi.LogMetadata{RequestID: event.RequestID, OrganizationID: event.OrganizationID})
		logging.Printf(ctx, "WARN", "gateway.security_event.dropped", "rule_id=%s", event.RuleID)
	}
	return nil
}

func (sink *DatabaseSecurityEventSink) run() {
	defer close(sink.done)
	ticker := time.NewTicker(securityevidence.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			sink.cleanupEvidence()
		case event, open := <-sink.queue:
			if !open {
				return
			}
			sink.persist(event)
		}
	}
}

func (sink *DatabaseSecurityEventSink) cleanupEvidence() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Drain the expiry backlog in indexed batches, with a bound on the whole
	// cleanup run so queued event persistence can resume promptly.
	cutoff := time.Now().UTC()
	for {
		result := sink.database.WithContext(ctx).Exec("DELETE FROM security_event_evidence WHERE expires_at <= ? ORDER BY expires_at LIMIT 10000", cutoff)
		if result.Error != nil {
			logging.Printf(ctx, "ERROR", "gateway.security_evidence.cleanup_failed", "evidence cleanup failed")
			return
		}
		if result.RowsAffected < 10000 {
			return
		}
	}
}

func (sink *DatabaseSecurityEventSink) persist(event domain.SecurityEvent) {
	record := struct {
		ID               uint64
		EventType        string
		Severity         string
		RequestID        string
		OrganizationID   *uint64
		APIKeyID         *uint64
		ClientIP         []byte
		RuleID           string
		Action           string
		HitCount         uint32
		HTTPStatus       *uint16
		CreatedAt        time.Time
		ToolName         string
		ToolProtocol     string
		ToolDirection    string
		CallIDHash       string
		RuleVersion      uint64
		RuleScope        string
		RuleSensitivity  string
		RuleNameSnapshot string
		ProcessingResult string
		EvidenceCount    uint8
	}{RequestID: event.RequestID, RuleID: event.RuleID, Action: event.Action, HitCount: event.HitCount, CreatedAt: event.CreatedAt, RuleVersion: event.RuleVersion, RuleScope: event.RuleScope, RuleSensitivity: event.RuleSensitivity, RuleNameSnapshot: event.RuleName, ProcessingResult: event.ProcessingResult, EvidenceCount: uint8(len(event.Evidence))}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	record.CreatedAt = record.CreatedAt.UTC()
	if record.ProcessingResult == "" {
		record.ProcessingResult = "observed"
		if event.Action == "block" {
			record.ProcessingResult = "blocked"
		}
	}
	if record.RuleSensitivity == "" {
		record.RuleSensitivity = "normal"
	}
	if event.ToolCall != nil {
		record.ToolName, record.ToolProtocol, record.ToolDirection, record.CallIDHash = event.ToolCall.Name, string(event.ToolCall.Protocol), string(event.ToolCall.Direction), event.ToolCall.CallIDHash
	}
	record.EventType, record.Severity = adminDomain.ClassifySecurityEvent(record.RuleID, record.Action, record.ToolName)
	if record.HitCount == 0 {
		record.HitCount = 1
	}
	if event.OrganizationID != 0 {
		value := event.OrganizationID
		record.OrganizationID = &value
	}
	if event.APIKeyID != 0 {
		value := event.APIKeyID
		record.APIKeyID = &value
	}
	if len(event.ClientIP) > 0 {
		record.ClientIP = event.ClientIP
	}
	if event.HTTPStatus != 0 {
		value := uint16(event.HTTPStatus)
		record.HTTPStatus = &value
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := sink.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("security_events").Create(&record).Error; err != nil {
			return err
		}
		if len(event.Evidence) > 0 {
			rows := make([]securityevidence.Record, 0, len(event.Evidence))
			for index, evidence := range event.Evidence {
				rows = append(rows, securityevidence.Record{EventID: record.ID, Ordinal: index, Evidence: evidence, CreatedAt: record.CreatedAt, ExpiresAt: record.CreatedAt.Add(securityevidence.Retention)})
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		return nil
	})
	cancel()
	if err != nil {
		ctx := httpapi.WithRequestID(context.Background(), event.RequestID)
		ctx = httpapi.WithLogMetadata(ctx, httpapi.LogMetadata{RequestID: event.RequestID, OrganizationID: event.OrganizationID})
		logging.Printf(ctx, "ERROR", "gateway.security_event.persist_failed", "rule_id=%s", event.RuleID)
	}
}

func (sink *DatabaseSecurityEventSink) Close() { close(sink.queue); <-sink.done }

// RuleSecurityEventSink applies local secret-pattern checks and publishes events
// through the configured sink. Deployments can replace it with a remote policy engine.
type RuleSecurityEventSink struct {
	Sink     domain.SecurityEventSink
	Advanced securitycontract.AdvancedDetector
}

// WithAdvancedSecurity attaches the optional proprietary detector to the local
// rule sink without making the open-source core depend on its package.
func WithAdvancedSecurity(sink domain.SecurityEventSink, advanced securitycontract.AdvancedDetector) domain.SecurityEventSink {
	switch sink := sink.(type) {
	case RuleSecurityEventSink:
		sink.Advanced = advanced
		return sink
	case *RuleSecurityEventSink:
		copy := *sink
		copy.Advanced = advanced
		return &copy
	default:
		return sink
	}
}

var outputCodeMarkers = []string{"<script", "javascript:", "data:text/html", "powershell -enc", "invoke-expression", "curl | sh", "wget | sh", "rm -rf /", "__import__(", "subprocess.", "os.system("}

var promptSecretPatterns = securityevidence.CredentialPatterns
var credentialFieldName = securityevidence.CredentialFieldName

const passwordHide = "[Password Hide]"

// SanitizeContent masks credential-like values while preserving the rest of the payload.
func (sink RuleSecurityEventSink) SanitizeContent(_ context.Context, kind string, payload []byte) ([]byte, bool, error) {
	if kind != "prompt" || len(payload) == 0 {
		return payload, false, nil
	}
	if json.Valid(payload) {
		// Match decoded strings just like inspection, including escaped field
		// names. Preserve JSON number precision when a redacted body is encoded.
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, false, err
		}
		value, changed := mapCurrentPrompt(value, func(selected any, path string) (any, bool) {
			return redactJSONCredentials(selected, path == "")
		})
		if !changed {
			return payload, false, nil
		}
		masked, err := json.Marshal(value)
		return masked, true, err
	}
	masked, changed := redactCredentialText(payload)
	return masked, changed, nil
}

func redactJSONCredentials(value any, root bool) (any, bool) {
	changed := false
	switch value := value.(type) {
	case string:
		masked, changed := redactCredentialText([]byte(value))
		return string(masked), changed
	case []any:
		for index, item := range value {
			redacted, itemChanged := redactJSONCredentials(item, false)
			value[index], changed = redacted, changed || itemChanged
		}
		return value, changed
	case map[string]any:
		for key, item := range value {
			if root && isPromptSchemaField(key, item) {
				continue
			}
			if text, ok := item.(string); ok && credentialFieldName.MatchString(key) && len(strings.TrimSpace(text)) >= 4 && !securityevidence.IsCredentialPlaceholder(text) {
				value[key], changed = passwordHide, true
				continue
			}
			redacted, itemChanged := redactJSONCredentials(item, false)
			value[key], changed = redacted, changed || itemChanged
		}
		return value, changed
	}
	return value, false
}

func redactCredentialText(payload []byte) ([]byte, bool) {
	masked := append([]byte(nil), payload...)
	changed := false
	if indexes := securityevidence.CredentialAssignments(masked, -1); len(indexes) > 0 {
		var out bytes.Buffer
		last := 0
		for _, index := range indexes {
			out.Write(masked[last:index[4]])
			out.WriteString(passwordHide)
			last = index[5]
		}
		out.Write(masked[last:])
		masked = out.Bytes()
		changed = true
	}
	for _, pattern := range promptSecretPatterns {
		if !securityevidence.CredentialPatternCanMatch(pattern, masked) {
			continue
		}
		next := pattern.Regex.ReplaceAll(masked, []byte(passwordHide))
		if !bytes.Equal(next, masked) {
			changed = true
			masked = next
		}
	}
	return masked, changed
}

func (sink RuleSecurityEventSink) Inspect(_ context.Context, request domain.SecurityInspection) (bool, error) {
	return true, nil
}

func (sink RuleSecurityEventSink) InspectResult(ctx context.Context, request domain.SecurityInspection) (domain.SecurityInspectionResult, error) {
	return sink.InspectContent(ctx, "prompt", request.Body)
}

func (sink RuleSecurityEventSink) InspectContent(ctx context.Context, kind string, payload []byte) (domain.SecurityInspectionResult, error) {
	return inspectDocumentWithAdvanced(ctx, kind, contentInspectionDocument(kind, payload), sink.Advanced), nil
}

func (sink RuleSecurityEventSink) Publish(ctx context.Context, event domain.SecurityEvent) error {
	if sink.Sink == nil {
		return nil
	}
	return sink.Sink.Publish(ctx, event)
}

func (sink RuleSecurityEventSink) Close() {
	if closer, ok := sink.Sink.(interface{ Close() }); ok {
		closer.Close()
	}
}

// NoopNotificationPublisher drops notifications until a queue is configured.
type NoopNotificationPublisher struct{}

// Publish ignores notifications until a queue is configured.
func (NoopNotificationPublisher) Publish(context.Context, domain.Notification) error { return nil }

// DefaultBackgroundServices returns safe local implementations for optional infrastructure.
func DefaultBackgroundServices() domain.BackgroundServices {
	advanced := securitycontract.NoopAdvancedDetector{}
	return domain.BackgroundServices{
		RateLimiter:      NoopRateLimiter{},
		SecurityEvents:   RuleSecurityEventSink{Sink: NoopSecurityEventSink{}, Advanced: advanced},
		AdvancedSecurity: advanced,
		Notifications:    NoopNotificationPublisher{},
	}
}

// RedisRateLimiter enforces Admin-configured limits in a shared Redis instance.
type RedisRateLimiter struct {
	client     redis.UniversalClient
	repository adminDomain.RateLimitRepository
	prefix     string
}

func (limiter *RedisRateLimiter) Decide(ctx context.Context, key domain.RateLimitKey, now time.Time) (domain.RateLimitDecision, error) {
	decision, err := limiter.decideRedis(ctx, key)
	if err != nil {
		return domain.RateLimitDecision{Allowed: true, Degraded: true}, nil
	}
	return decision, nil
}

func NewRedisRateLimiter(client redis.UniversalClient, repository adminDomain.RateLimitRepository, prefix string) *RedisRateLimiter {
	if prefix == "" {
		prefix = "bos:gateway:rate"
	}
	return &RedisRateLimiter{client: client, repository: repository, prefix: prefix}
}

var acquireSessionScript = redis.NewScript(`
local time = redis.call('TIME')
local now = tonumber(time[1]) * 1000 + math.floor(tonumber(time[2]) / 1000)
local expiry = now + tonumber(ARGV[2])
local token = ARGV[1]
for i, key in ipairs(KEYS) do
  redis.call('ZREMRANGEBYSCORE', key, '-inf', now)
  local limit = tonumber(ARGV[2 + i])
  if limit > 0 and redis.call('ZCARD', key) >= limit then return {0, i} end
end
for _, key in ipairs(KEYS) do redis.call('ZADD', key, expiry, token); redis.call('PEXPIRE', key, 7200000) end
return {1, 0}
`)

var releaseSessionScript = redis.NewScript(`for _, key in ipairs(KEYS) do redis.call('ZREM', key, ARGV[1]) end return 1`)

// Renew only an existing lease. If any scope lost the token while Redis was
// unavailable, return 0 without recreating it; another request may own the slot.
var renewSessionScript = redis.NewScript(`
local time=redis.call('TIME')
local expiry=tonumber(time[1])*1000+math.floor(tonumber(time[2])/1000)+tonumber(ARGV[2])
local now=tonumber(time[1])*1000+math.floor(tonumber(time[2])/1000)
for _,key in ipairs(KEYS) do
  local score=redis.call('ZSCORE', key, ARGV[1])
  if not score or tonumber(score) <= now then return 0 end
end
for _,key in ipairs(KEYS) do
  redis.call('ZADD', key, expiry, ARGV[1])
  redis.call('PEXPIRE', key, 7200000)
end
return 1
`)

// AcquireSession atomically reserves global, organization and key slots. Leases
// expire after an hour as crash protection and are renewed while the request lives.
func (limiter *RedisRateLimiter) AcquireSession(ctx context.Context, key domain.RateLimitKey) (domain.RateLimitDecision, func(), error) {
	if limiter.client == nil || limiter.repository == nil {
		return domain.RateLimitDecision{Allowed: true, Degraded: true}, func() {}, nil
	}
	config, err := readGatewayRateLimits(ctx, limiter.repository, key)
	if err != nil {
		return domain.RateLimitDecision{Allowed: true, Degraded: true}, func() {}, nil
	}
	rules := configuredConcurrencyRules(config, key)
	var keys []string
	var limits []int64
	var activeRules []redisLimitRule
	for _, rule := range rules {
		if rule.limit > 0 {
			keys = append(keys, concurrencyBucket(limiter.prefix, rule.name, key))
			limits = append(limits, rule.limit)
			activeRules = append(activeRules, rule)
		}
	}
	if len(keys) == 0 {
		return domain.RateLimitDecision{Allowed: true}, func() {}, nil
	}
	token := strconv.FormatInt(time.Now().UnixNano(), 36) + ":" + strconv.FormatUint(sessionSequence.Add(1), 36)
	lease := time.Hour
	args := []any{token, lease.Milliseconds()}
	for _, limit := range limits {
		args = append(args, limit)
	}
	result, err := acquireSessionScript.Run(ctx, limiter.client, keys, args...).Int64Slice()
	if err != nil {
		return domain.RateLimitDecision{Allowed: true, Degraded: true}, func() {}, nil
	}
	if len(result) != 2 || result[1] < 0 || result[1] > int64(len(limits)) {
		return domain.RateLimitDecision{Allowed: true, Degraded: true}, func() {}, nil
	}
	if result[0] == 0 {
		rule := activeRules[result[1]-1]
		return domain.RateLimitDecision{Allowed: false, Reason: rule.reason(), Limit: rule.limit, Remaining: 0, RetryAfter: time.Second}, func() {}, nil
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				renewed, renewErr := renewSessionScript.Run(renewCtx, limiter.client, keys, token, lease.Milliseconds()).Int64()
				if renewErr != nil || renewed == 0 {
					// The lease expired or was removed while Redis was unavailable;
					// never resurrect the token and inflate the active count.
					cancel()
					return
				}
				cancel()
			}
		}
	}()
	var once sync.Once
	release := func() {
		once.Do(func() {
			close(stop)
			<-done
			releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = releaseSessionScript.Run(releaseCtx, limiter.client, keys, token).Err()
		})
	}
	return domain.RateLimitDecision{Allowed: true}, release, nil
}

// All keys touched by the multi-key Lua scripts share a Redis Cluster slot.
func concurrencyBucket(prefix, name string, key domain.RateLimitKey) string {
	scope := "global"
	switch name {
	case "organization":
		scope = "organization:" + strconv.FormatUint(key.OrganizationID, 10)
	case "key":
		scope = "key:" + strconv.FormatUint(key.APIKeyID, 10)
	}
	return prefix + ":{sessions}:" + scope
}

var sessionSequence atomic.Uint64

func configuredConcurrencyRules(config adminDomain.RateLimitUpdate, key domain.RateLimitKey) []redisLimitRule {
	var rules []redisLimitRule
	if config.Global != nil {
		rules = append(rules, redisLimitRule{"global", config.Global.QPS, 0})
	}
	for _, item := range config.Organizations {
		if item.OrganizationID == key.OrganizationID {
			rules = append(rules, redisLimitRule{"organization", item.QPS, 0})
		}
	}
	for _, item := range config.Keys {
		if item.KeyID == key.APIKeyID {
			rules = append(rules, redisLimitRule{"key", item.QPS, 0})
		}
	}
	return rules
}

var rateLimitScript = redis.NewScript(`
local deniedIndex = 0
local retryAfter = 0
for index, key in ipairs(KEYS) do
  local limit = tonumber(ARGV[(index - 1) * 2 + 1])
  local window = tonumber(ARGV[(index - 1) * 2 + 2])
  local current = tonumber(redis.call('GET', key) or '0')
  if current + 1 > limit then
    local ttl = redis.call('PTTL', key)
    if ttl < 0 then
      redis.call('PEXPIRE', key, window)
      ttl = window
    end
    if deniedIndex == 0 or ttl > retryAfter then
      deniedIndex = index
      retryAfter = ttl
    end
  end
end
if deniedIndex > 0 then return {0, deniedIndex, 0, retryAfter} end
local tightestIndex = 0
local remaining = 0
for index, key in ipairs(KEYS) do
  local limit = tonumber(ARGV[(index - 1) * 2 + 1])
  local window = tonumber(ARGV[(index - 1) * 2 + 2])
  local current = redis.call('INCR', key)
  if current == 1 then redis.call('PEXPIRE', key, window) end
  if tightestIndex == 0 or limit - current < remaining then
    tightestIndex = index
    remaining = limit - current
  end
end
return {1, tightestIndex, remaining, 0}
`)

func (limiter *RedisRateLimiter) Allow(ctx context.Context, key domain.RateLimitKey, now time.Time) (bool, error) {
	decision, err := limiter.Decide(ctx, key, now)
	return decision.Allowed, err
}

func (limiter *RedisRateLimiter) decideRedis(ctx context.Context, key domain.RateLimitKey) (domain.RateLimitDecision, error) {
	if limiter.client == nil || limiter.repository == nil {
		return domain.RateLimitDecision{}, errors.New("redis rate limiter is not configured")
	}
	config, err := readGatewayRateLimits(ctx, limiter.repository, key)
	if err != nil {
		return domain.RateLimitDecision{}, err
	}
	limits := limiter.rules(config, key)
	keys := make([]string, 0, len(limits))
	args := make([]any, 0, len(limits)*2)
	activeRules := make([]redisLimitRule, 0, len(limits))
	for _, rule := range limits {
		if rule.limit <= 0 {
			continue
		}
		keys = append(keys, rateLimitBucket(limiter.prefix, rule.name, key))
		args = append(args, rule.limit, rule.window.Milliseconds())
		activeRules = append(activeRules, rule)
	}
	if len(keys) == 0 {
		return domain.RateLimitDecision{Allowed: true}, nil
	}
	result, err := rateLimitScript.Run(ctx, limiter.client, keys, args...).Int64Slice()
	if err != nil {
		return domain.RateLimitDecision{}, err
	}
	if len(result) != 4 || (result[0] != 0 && result[0] != 1) || result[1] < 1 || result[1] > int64(len(activeRules)) {
		return domain.RateLimitDecision{}, errors.New("invalid redis rate limit decision")
	}
	rule := activeRules[result[1]-1]
	decision := domain.RateLimitDecision{Allowed: result[0] == 1, Limit: rule.limit, Remaining: result[2], RetryAfter: time.Duration(result[3]) * time.Millisecond}
	if !decision.Allowed {
		decision.Reason = rule.reason()
	}
	return decision, nil
}

func (limiter *RedisRateLimiter) Record(context.Context, domain.RateLimitKey, time.Time) error {
	return nil
}

type redisLimitRule struct {
	name   string
	limit  int64
	window time.Duration
}

func (rule redisLimitRule) reason() domain.RateLimitReason {
	switch rule.name {
	case "global":
		return domain.RateLimitGlobalConcurrency
	case "organization":
		return domain.RateLimitOrganizationConcurrency
	case "key":
		return domain.RateLimitKeyConcurrency
	default:
		return domain.RateLimitReason(rule.name)
	}
}

func readGatewayRateLimits(ctx context.Context, repository adminDomain.RateLimitRepository, key domain.RateLimitKey) (adminDomain.RateLimitUpdate, error) {
	config, err := repository.ReadRateLimits(ctx, key.OrganizationID, key.APIKeyID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Encrypted-store gateway keys have no api_keys row. They still use
		// global and organization limits without exposing another key's settings.
		return repository.ReadRateLimits(ctx, key.OrganizationID, 0)
	}
	return config, err
}

func (limiter *RedisRateLimiter) rules(config adminDomain.RateLimitUpdate, key domain.RateLimitKey) []redisLimitRule {
	return configuredRateLimitRules(config, key)
}

func configuredRateLimitRules(config adminDomain.RateLimitUpdate, key domain.RateLimitKey) []redisLimitRule {
	var rules []redisLimitRule
	// Legacy QPS fields are now enforced as active-session caps in AcquireSession.
	for _, item := range config.Keys {
		if item.KeyID == key.APIKeyID {
			rules = append(rules, redisLimitRule{"quota_5h", pointerOrZero(item.Quota5H), 5 * time.Hour}, redisLimitRule{"quota_1d", pointerOrZero(item.Quota1D), 24 * time.Hour}, redisLimitRule{"quota_7d", pointerOrZero(item.Quota7D), 7 * 24 * time.Hour})
		}
	}
	return rules
}

func pointerOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
