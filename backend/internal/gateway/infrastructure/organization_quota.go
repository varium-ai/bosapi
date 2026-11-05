package infrastructure

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	adminDomain "bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/usagetime"
	userInfra "bosapi/backend/internal/user/infrastructure"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// amountScale represents one USD as a Redis integer.
const amountScale = int64(100_000_000)

// OrganizationQuotaRepository supplies organization quota configuration and the
// authoritative database usage used by cold-start and calibration.
type OrganizationQuotaRepository interface {
	OrganizationRateLimit(context.Context, uint64) (adminDomain.OrganizationRateLimit, error)
	OrganizationUsage(context.Context, uint64, time.Time) (userInfra.OrganizationPeriodicUsage, error)
	ConfiguredOrganizations(context.Context) ([]uint64, error)
}

type gormOrganizationQuotaRepository struct{ database *gorm.DB }

// NewGormOrganizationQuotaRepository constructs the authoritative repository.
func NewGormOrganizationQuotaRepository(database *gorm.DB) OrganizationQuotaRepository {
	return &gormOrganizationQuotaRepository{database: database}
}

func (repository *gormOrganizationQuotaRepository) OrganizationRateLimit(ctx context.Context, organizationID uint64) (adminDomain.OrganizationRateLimit, error) {
	var rows []quotaSettingRow
	if repository.database == nil {
		return adminDomain.OrganizationRateLimit{}, errors.New("organization quota database unavailable")
	}
	if err := repository.database.WithContext(ctx).Table("settings").Select("value_text").Where("setting_key = ?", fmt.Sprintf("rate_limit.organization.%d", organizationID)).Limit(1).Scan(&rows).Error; err != nil {
		return adminDomain.OrganizationRateLimit{}, err
	}
	config := adminDomain.OrganizationRateLimit{OrganizationID: organizationID}
	if len(rows) > 0 && rows[0].ValueText != nil {
		if err := json.Unmarshal([]byte(*rows[0].ValueText), &config); err != nil {
			return config, err
		}
	}
	config.OrganizationID = organizationID
	return config, nil
}

func (repository *gormOrganizationQuotaRepository) OrganizationUsage(ctx context.Context, organizationID uint64, now time.Time) (userInfra.OrganizationPeriodicUsage, error) {
	return userInfra.FindOrganizationPeriodicUsage(ctx, repository.database, organizationID, now)
}

type quotaSettingRow struct {
	SettingKey string
	ValueText  *string
}

func (repository *gormOrganizationQuotaRepository) ConfiguredOrganizations(ctx context.Context) ([]uint64, error) {
	var rows []quotaSettingRow
	err := repository.database.WithContext(ctx).Table("settings").
		Select("setting_key, value_text").
		Where("setting_key LIKE ?", "rate_limit.organization.%").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		id, err := strconv.ParseUint(strings.TrimPrefix(row.SettingKey, "rate_limit.organization."), 10, 64)
		if err != nil || id == 0 {
			continue
		}
		var config adminDomain.OrganizationRateLimit
		if row.ValueText == nil {
			continue
		}
		if err := json.Unmarshal([]byte(*row.ValueText), &config); err != nil {
			return nil, err
		}
		if hasOrganizationQuotaFields(config) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// OrganizationQuotaDenial describes a calendar quota that blocks requests.
type OrganizationQuotaDenial struct {
	Reason     string
	Limit      string
	RetryAfter time.Duration
}

// EvaluateOrganizationQuota reports the longest automatic blocker. A zero limit
// always blocks and deliberately has no automatic recovery duration.
func EvaluateOrganizationQuota(config adminDomain.OrganizationRateLimit, usage userInfra.OrganizationPeriodicUsage, now time.Time) *OrganizationQuotaDenial {
	day, week, month := usagetime.PeriodStarts(now)
	dayReset := day.AddDate(0, 0, 1)
	weekReset := week.AddDate(0, 0, 7)
	monthReset := month.In(usagetime.Location()).AddDate(0, 1, 0)
	var denial *OrganizationQuotaDenial
	add := func(reason, limit string, reset time.Time) {
		retry := time.Duration(0)
		if !reset.IsZero() {
			retry = reset.Sub(now)
		}
		if denial == nil || denial.RetryAfter > 0 && (retry == 0 || retry > denial.RetryAfter) {
			denial = &OrganizationQuotaDenial{Reason: reason, Limit: limit, RetryAfter: retry}
		}
	}
	tokens := func(reason string, limit *int64, used int64, reset time.Time) {
		if limit != nil && used >= *limit {
			if *limit <= 0 {
				reset = time.Time{}
			}
			add(reason, strconv.FormatInt(*limit, 10), reset)
		}
	}
	amount := func(reason string, limit *float64, used float64, reset time.Time) {
		if limit != nil && used >= *limit {
			if *limit <= 0 {
				reset = time.Time{}
			}
			add(reason, strconv.FormatFloat(*limit, 'f', -1, 64), reset)
		}
	}
	tokens("organization_daily_tokens", config.DailyQuotaTokens, usage.DailyTokens, dayReset)
	amount("organization_daily_amount", config.DailyQuotaAmount, usage.DailyAmount, dayReset)
	tokens("organization_weekly_tokens", config.WeeklyQuotaTokens, usage.WeeklyTokens, weekReset)
	amount("organization_weekly_amount", config.WeeklyQuotaAmount, usage.WeeklyAmount, weekReset)
	tokens("organization_monthly_tokens", config.MonthlyQuotaTokens, usage.MonthlyTokens, monthReset)
	amount("organization_monthly_amount", config.MonthlyQuotaAmount, usage.MonthlyAmount, monthReset)
	return denial
}

func hasOrganizationQuotaFields(config adminDomain.OrganizationRateLimit) bool {
	return config.DailyQuotaTokens != nil || config.WeeklyQuotaTokens != nil || config.MonthlyQuotaTokens != nil ||
		config.DailyQuotaAmount != nil || config.WeeklyQuotaAmount != nil || config.MonthlyQuotaAmount != nil
}

type organizationQuotaConfigEntry struct {
	config    adminDomain.OrganizationRateLimit
	expiresAt time.Time
}
type organizationQuotaState struct {
	observed time.Time
	snapshot userInfra.OrganizationPeriodicUsage
}

// OrganizationQuotaCounter shares counters across replicas. A short tenant lock
// fences DB reads and ledger settlement, never upstream execution. This prevents
// a cold-start/calibration snapshot from including a receipt that is then added
// a second time by its settling request.
type OrganizationQuotaCounter struct {
	owned                                         *redis.Client
	closeOnce                                     sync.Once
	client                                        redis.UniversalClient
	repository                                    OrganizationQuotaRepository
	prefix                                        string
	configTTL, snapshotGrace, calibrationInterval time.Duration
	mu                                            sync.Mutex
	configs                                       map[uint64]organizationQuotaConfigEntry
	states                                        map[uint64]organizationQuotaState
	generations                                   map[uint64]uint64
	usageVersions                                 map[uint64]uint64
	repairs                                       map[uint64]bool
	configLocks                                   [64]sync.Mutex
	usageLocks                                    [64]sync.Mutex
	cancel                                        context.CancelFunc
	done                                          chan struct{}
	closed                                        bool
}

func NewOrganizationQuotaCounter(client redis.UniversalClient, repository OrganizationQuotaRepository, prefix string) *OrganizationQuotaCounter {
	if prefix == "" {
		prefix = "bos:org:quota"
	}
	var owned *redis.Client
	if source, ok := client.(*redis.Client); ok {
		options := *source.Options()
		options.ContextTimeoutEnabled = true
		options.MaxRetries = -1
		options.DialTimeout = 300 * time.Millisecond
		options.ReadTimeout = 300 * time.Millisecond
		options.WriteTimeout = 300 * time.Millisecond
		options.PoolTimeout = 300 * time.Millisecond
		owned = redis.NewClient(&options)
		client = owned
	}
	return &OrganizationQuotaCounter{
		client: client, owned: owned, repository: repository, prefix: prefix,
		configTTL: 20 * time.Second, snapshotGrace: 10 * time.Second, calibrationInterval: 30 * time.Second,
		configs: map[uint64]organizationQuotaConfigEntry{}, states: map[uint64]organizationQuotaState{}, generations: map[uint64]uint64{},
		usageVersions: map[uint64]uint64{}, repairs: map[uint64]bool{},
	}
}
func (c *OrganizationQuotaCounter) Invalidate(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.configs, id)
	c.markRepairLocked(id)
	c.generations[id]++
}
func (c *OrganizationQuotaCounter) PublishInvalidate(ctx context.Context, id uint64) {
	// Retired counters must not survive a disable/re-enable cycle. Fencing
	// outstanding writers also prevents them from republishing old balances.
	c.discard(ctx, id, time.Now())
	if c.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if err := c.client.Publish(ctx, c.prefix+":invalidate", strconv.FormatUint(id, 10)).Err(); err != nil {
		logging.Printf(ctx, "WARN", "organization_quota.invalidate_failed", "organization_id=%d err=%v", id, err)
	}
}
func (c *OrganizationQuotaCounter) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil || c.closed || c.client == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})
	go func() {
		defer close(c.done)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); c.subscribe(ctx) }()
		defer wg.Wait()
		ticker := time.NewTicker(c.calibrationInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				op, done := context.WithTimeout(ctx, 15*time.Second)
				if err := c.Calibrate(op, time.Now()); err != nil && ctx.Err() == nil {
					logging.Printf(ctx, "WARN", "organization_quota.calibration_failed", "err=%v", err)
				}
				done()
			}
		}
	}()
}
func (c *OrganizationQuotaCounter) subscribe(ctx context.Context) {
	sub := c.client.Subscribe(ctx, c.prefix+":invalidate")
	defer sub.Close()
	messages := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-messages:
			if !ok {
				return
			}
			if id, err := strconv.ParseUint(msg.Payload, 10, 64); err == nil {
				c.Invalidate(id)
			}
		}
	}
}
func (c *OrganizationQuotaCounter) Close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		cancel, done := c.cancel, c.done
		c.mu.Unlock()
		if cancel != nil {
			cancel()
			<-done
		}
		if c.owned != nil {
			_ = c.owned.Close()
		}
	})
}

func (c *OrganizationQuotaCounter) config(ctx context.Context, id uint64, now time.Time) (adminDomain.OrganizationRateLimit, error) {
	c.mu.Lock()
	entry, ok := c.configs[id]
	c.mu.Unlock()
	if ok && now.Before(entry.expiresAt) {
		return entry.config, nil
	}
	lock := &c.configLocks[id%64]
	lock.Lock()
	defer lock.Unlock()
	c.mu.Lock()
	entry, ok = c.configs[id]
	generation := c.generations[id]
	c.mu.Unlock()
	if ok && now.Before(entry.expiresAt) {
		return entry.config, nil
	}
	if c.repository == nil {
		return adminDomain.OrganizationRateLimit{}, errors.New("organization quota repository unavailable")
	}
	config, err := c.repository.OrganizationRateLimit(ctx, id)
	if err != nil {
		return config, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generations[id] {
		return config, errors.New("organization quota configuration changed during read")
	}
	if ok && !hasOrganizationQuotaFields(entry.config) && hasOrganizationQuotaFields(config) {
		c.markRepairLocked(id)
	}
	c.configs[id] = organizationQuotaConfigEntry{config: config, expiresAt: now.Add(c.configTTL)}
	return config, nil
}
func (c *OrganizationQuotaCounter) Check(ctx context.Context, id uint64, now time.Time) (bool, *OrganizationQuotaDenial, error) {
	if id == 0 {
		return true, nil, nil
	}
	config, err := c.config(ctx, id, now)
	if err != nil {
		return false, nil, err
	}
	if !hasOrganizationQuotaFields(config) {
		return true, nil, nil
	}
	if denial := EvaluateOrganizationQuota(config, userInfra.OrganizationPeriodicUsage{}, now); denial != nil {
		return false, denial, nil
	}
	usage, err := c.Usage(ctx, id, now)
	if err != nil {
		return false, nil, err
	}
	denial := EvaluateOrganizationQuota(config, usage, now)
	return denial == nil, denial, nil
}
func (c *OrganizationQuotaCounter) remember(id uint64, usage userInfra.OrganizationPeriodicUsage, now time.Time) {
	version, _ := c.usageVersion(id)
	c.rememberVersion(id, usage, now, version, false)
}
func (c *OrganizationQuotaCounter) usageVersion(id uint64) (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usageVersions[id], c.repairs[id]
}
func (c *OrganizationQuotaCounter) markRepairLocked(id uint64) {
	delete(c.states, id)
	c.usageVersions[id]++
	c.repairs[id] = true
}
func (c *OrganizationQuotaCounter) rememberVersion(id uint64, usage userInfra.OrganizationPeriodicUsage, now time.Time, version uint64, repaired bool) bool {
	usage.OrganizationID = id
	c.mu.Lock()
	defer c.mu.Unlock()
	if version != c.usageVersions[id] || c.repairs[id] && !repaired {
		return false
	}
	if repaired {
		delete(c.repairs, id)
	}
	// A slower earlier read must not overwrite a more recent successful snapshot.
	if c.states[id].observed.After(now) {
		return true
	}
	c.states[id] = organizationQuotaState{observed: now, snapshot: usage}
	return true
}
func (c *OrganizationQuotaCounter) recent(id uint64, now time.Time) (userInfra.OrganizationPeriodicUsage, bool) {
	c.mu.Lock()
	s, ok := c.states[id]
	needsRepair := c.repairs[id]
	c.mu.Unlock()
	return s.snapshot, ok && !needsRepair && !s.observed.IsZero() && !now.Before(s.observed) && now.Sub(s.observed) <= c.snapshotGrace && usagetime.DayStart(s.observed).Equal(usagetime.DayStart(now))
}
func (c *OrganizationQuotaCounter) redisUsage(ctx context.Context, id uint64, now time.Time) (userInfra.OrganizationPeriodicUsage, error) {
	if c.client == nil {
		return userInfra.OrganizationPeriodicUsage{}, errQuotaUnavailable
	}
	op, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	values, err := c.client.MGet(op, organizationQuotaKeys(c.prefix, id, now)...).Result()
	if err != nil {
		return userInfra.OrganizationPeriodicUsage{}, err
	}
	if len(values) != 6 {
		return userInfra.OrganizationPeriodicUsage{}, errQuotaUnavailable
	}
	for _, v := range values {
		if v == nil {
			return userInfra.OrganizationPeriodicUsage{}, errRedisMiss
		}
	}
	usage, err := usageFromRedis(values)
	usage.OrganizationID = id
	return usage, err
}

// Usage serves the admin view and admission from the same counters. Unlimited
// tenants use a short display cache and never create Redis usage counters.
func (c *OrganizationQuotaCounter) Usage(ctx context.Context, id uint64, now time.Time) (userInfra.OrganizationPeriodicUsage, error) {
	// Keep one observation version across configuration and usage reads. An
	// older unlimited read must not clear a repair requested by a newer limit.
	version, _ := c.usageVersion(id)
	config, err := c.config(ctx, id, now)
	if err != nil {
		return userInfra.OrganizationPeriodicUsage{}, err
	}
	limited := hasOrganizationQuotaFields(config)
	currentVersion, needsRepair := c.usageVersion(id)
	if currentVersion != version {
		return userInfra.OrganizationPeriodicUsage{}, errQuotaUnavailable
	}
	if !limited {
		if usage, ok := c.recent(id, now); ok {
			return usage, nil
		}
	}
	if limited && !needsRepair {
		usage, err := c.redisUsage(ctx, id, now)
		if err == nil {
			if !c.rememberVersion(id, usage, now, version, false) {
				return usage, errQuotaUnavailable
			}
			return usage, nil
		}
		if !errors.Is(err, errRedisMiss) {
			if usage, ok := c.recent(id, now); ok {
				return usage, nil
			}
			return usage, err
		}
	}
	lock := &c.usageLocks[id%64]
	lock.Lock()
	defer lock.Unlock()
	currentVersion, needsRepair = c.usageVersion(id)
	if currentVersion != version {
		return userInfra.OrganizationPeriodicUsage{}, errQuotaUnavailable
	}
	if !limited {
		if usage, ok := c.recent(id, now); ok {
			return usage, nil
		}
		op, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		usage, err := c.repository.OrganizationUsage(op, id, now)
		if err == nil {
			if !c.rememberVersion(id, usage, now, version, true) {
				return usage, errQuotaUnavailable
			}
		}
		return usage, err
	}
	var usage userInfra.OrganizationPeriodicUsage
	err = c.withLock(ctx, id, func(op context.Context, token string) error {
		// Another replica may have repaired the missing keys while we waited.
		var e error
		if !needsRepair {
			usage, e = c.redisUsage(op, id, now)
			if e == nil {
				return nil
			}
			if !errors.Is(e, errRedisMiss) {
				return e
			}
		}
		usage, e = c.repository.OrganizationUsage(op, id, now)
		if e != nil {
			return e
		}
		if e = c.writeCurrent(op, id, usage, now, token); e != nil {
			return e
		}
		// Existing buckets may exceed the ledger snapshot. Admission must use
		// the merged counters, including on the first read after a partial miss.
		usage, e = c.redisUsage(op, id, now)
		return e
	})
	if err != nil {
		if last, ok := c.recent(id, now); ok {
			return last, nil
		}
		return usage, err
	}
	if !c.rememberVersion(id, usage, now, version, true) {
		return usage, errQuotaUnavailable
	}
	return usage, nil
}

// Settle fences the authoritative commit against cold starts/calibration. The
// persist callback reports whether this receipt was newly inserted. Redis
// failures never cancel billing; deleting affected buckets forces DB repair.
func (c *OrganizationQuotaCounter) Settle(ctx context.Context, id uint64, tokens int64, amount float64, at time.Time, persist func(context.Context) (bool, error)) error {
	version, _ := c.usageVersion(id)
	config, configErr := c.config(ctx, id, time.Now())
	if configErr == nil && !hasOrganizationQuotaFields(config) {
		_, err := persist(ctx)
		// A quota can be enabled and seeded while this unmetered commit is
		// still in flight. Fence that seed even if the commit response is lost.
		if currentVersion, _ := c.usageVersion(id); currentVersion != version {
			c.discard(ctx, id, at)
		}
		return err
	}
	var persisted bool
	var ledgerErr error
	err := c.withLock(ctx, id, func(op context.Context, token string) error {
		inserted, e := persist(op)
		persisted = true
		ledgerErr = e
		if e != nil {
			return e
		}
		if !inserted {
			// An earlier commit may have succeeded despite a lost DB response.
			// Rebuild from receipts instead of guessing whether Redis saw it.
			return c.client.Del(op, organizationQuotaKeys(c.prefix, id, at)...).Err()
		}
		keys, args := quotaArguments(c.prefix, id, at, []int64{max(0, tokens), amountInt(amount), max(0, tokens), amountInt(amount), max(0, tokens), amountInt(amount)})
		keys = append(keys, c.lockKey(id))
		args = append(args, token)
		result, e := quotaIncrementScript.Run(op, c.client, keys, args...).Int64()
		if e == nil && result < 0 {
			e = errQuotaUnavailable
		}
		return e
	})
	if !persisted {
		_, ledgerErr = persist(ctx)
	}
	if err != nil || configErr != nil {
		c.discard(ctx, id, at)
		logging.Printf(ctx, "WARN", "organization_quota.settlement_counter_failed", "organization_id=%d err=%v config_err=%v", id, err, configErr)
	} else {
		// A previous observation remains usable briefly, but must include this local
		// settlement. Re-read on the next request rather than extending its lifetime.
		c.mu.Lock()
		delete(c.states, id)
		c.usageVersions[id]++
		c.mu.Unlock()
	}
	return ledgerErr
}
func (c *OrganizationQuotaCounter) discard(ctx context.Context, id uint64, at time.Time) {
	c.mu.Lock()
	c.markRepairLocked(id)
	c.mu.Unlock()
	if c.client != nil {
		op, cancel := context.WithTimeout(context.WithoutCancel(ctx), 300*time.Millisecond)
		defer cancel()
		// DEL is atomic: removing the lease fences any older DB snapshot writer.
		// Its script will reject the lost token instead of recreating stale keys.
		keys := append(organizationQuotaKeys(c.prefix, id, at), c.lockKey(id))
		if err := c.client.Del(op, keys...).Err(); err != nil {
			logging.Printf(op, "WARN", "organization_quota.repair_pending", "organization_id=%d err=%v", id, err)
		}
	}
}
func (c *OrganizationQuotaCounter) lockKey(id uint64) string {
	return fmt.Sprintf("%s:{%d}:lock", c.prefix, id)
}
func (c *OrganizationQuotaCounter) withLock(ctx context.Context, id uint64, run func(context.Context, string) error) error {
	if c.client == nil {
		return errQuotaUnavailable
	}
	// Lease exceeds the maximum operation deadline. Scripts verify ownership.
	op, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	token := rand.Text()
	key := c.lockKey(id)
	acquire, stopAcquire := context.WithTimeout(op, time.Second)
	defer stopAcquire()
	for {
		acquired, err := c.client.SetNX(acquire, key, token, 30*time.Second).Result()
		if err != nil {
			return err
		}
		if acquired {
			break
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-acquire.Done():
			timer.Stop()
			return acquire.Err()
		case <-timer.C:
		}
	}
	defer func() {
		release, done := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer done()
		_ = quotaUnlockScript.Run(release, c.client, []string{key}, token).Err()
	}()
	return run(op, token)
}
func (c *OrganizationQuotaCounter) Calibrate(ctx context.Context, now time.Time) error {
	if c.client == nil || c.repository == nil {
		return nil
	}
	ids, err := c.repository.ConfiguredOrganizations(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		config, e := c.config(ctx, id, now)
		if e != nil {
			failures = append(failures, e)
			continue
		}
		if !hasOrganizationQuotaFields(config) {
			continue
		}
		began := time.Now()
		version, _ := c.usageVersion(id)
		var usage, previous userInfra.OrganizationPeriodicUsage
		e = c.withLock(ctx, id, func(op context.Context, token string) error {
			var err error
			previous, _ = c.redisUsage(op, id, now)
			usage, err = c.repository.OrganizationUsage(op, id, now)
			if err != nil {
				return err
			}
			return c.writeCurrent(op, id, usage, now, token)
		})
		if e != nil {
			failures = append(failures, fmt.Errorf("organization %d: %w", id, e))
			continue
		}
		// Read the merged counters; DB may be smaller than a previous Redis value.
		if merged, e := c.redisUsage(ctx, id, now); e == nil {
			c.rememberVersion(id, merged, now, version, true)
		}
		logging.Printf(ctx, "INFO", "organization_quota.calibrated", "organization_id=%d duration_ms=%d daily_tokens_delta=%d weekly_tokens_delta=%d monthly_tokens_delta=%d daily_amount_delta=%.8f weekly_amount_delta=%.8f monthly_amount_delta=%.8f",
			id, time.Since(began).Milliseconds(), max(0, usage.DailyTokens-previous.DailyTokens), max(0, usage.WeeklyTokens-previous.WeeklyTokens), max(0, usage.MonthlyTokens-previous.MonthlyTokens), max(0, usage.DailyAmount-previous.DailyAmount), max(0, usage.WeeklyAmount-previous.WeeklyAmount), max(0, usage.MonthlyAmount-previous.MonthlyAmount))
	}
	return errors.Join(failures...)
}
func (c *OrganizationQuotaCounter) writeCurrent(ctx context.Context, id uint64, u userInfra.OrganizationPeriodicUsage, now time.Time, token string) error {
	keys, args := quotaArguments(c.prefix, id, now, []int64{u.DailyTokens, amountInt(u.DailyAmount), u.WeeklyTokens, amountInt(u.WeeklyAmount), u.MonthlyTokens, amountInt(u.MonthlyAmount)})
	keys = append(keys, c.lockKey(id))
	args = append(args, token)
	result, err := quotaCurrentScript.Run(ctx, c.client, keys, args...).Int64()
	if err == nil && result < 0 {
		return errQuotaUnavailable
	}
	return err
}
func quotaArguments(prefix string, id uint64, now time.Time, values []int64) ([]string, []any) {
	day, week, month := usagetime.PeriodStarts(now)
	ends := []time.Time{day.AddDate(0, 0, 1), week.AddDate(0, 0, 7), month.In(usagetime.Location()).AddDate(0, 1, 0)}
	args := make([]any, 0, 13)
	for i, value := range values {
		args = append(args, strconv.FormatInt(max(0, value), 10), ends[i/2].Add(5*time.Minute).UnixMilli())
	}
	return organizationQuotaKeys(prefix, id, now), args
}
func amountInt(value float64) int64 {
	if math.IsNaN(value) || value <= 0 {
		return 0
	}
	scaled := math.Round(value * float64(amountScale))
	if scaled >= float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(scaled)
}
func usageFromRedis(values []any) (userInfra.OrganizationPeriodicUsage, error) {
	if len(values) != 6 {
		return userInfra.OrganizationPeriodicUsage{}, errQuotaUnavailable
	}
	var ints [6]int64
	for i, value := range values {
		s, ok := value.(string)
		if !ok {
			return userInfra.OrganizationPeriodicUsage{}, errQuotaUnavailable
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return userInfra.OrganizationPeriodicUsage{}, errQuotaUnavailable
		}
		ints[i] = n
	}
	return userInfra.OrganizationPeriodicUsage{DailyTokens: ints[0], DailyAmount: float64(ints[1]) / float64(amountScale), WeeklyTokens: ints[2], WeeklyAmount: float64(ints[3]) / float64(amountScale), MonthlyTokens: ints[4], MonthlyAmount: float64(ints[5]) / float64(amountScale)}, nil
}
func organizationQuotaKeys(prefix string, id uint64, now time.Time) []string {
	day, week, month := usagetime.PeriodStarts(now)
	loc := usagetime.Location()
	base := fmt.Sprintf("%s:{%d}:", prefix, id)
	result := make([]string, 0, 6)
	for _, period := range []string{"daily:" + day.In(loc).Format("20060102"), "weekly:" + week.In(loc).Format("20060102"), "monthly:" + month.In(loc).Format("200601")} {
		result = append(result, base+period+":tokens", base+period+":amount")
	}
	return result
}

var errRedisMiss = errors.New("organization quota counters missing")
var errQuotaUnavailable = errors.New("organization quota accounting unavailable")
var quotaUnlockScript = redis.NewScript(`if redis.call('GET',KEYS[1]) == ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`)

// Decimal comparisons and INCRBY arguments stay strings, preserving all int64
// bits beyond Lua's double precision range. Redis overflow saturates safely.
const quotaDecimalLua = `
local function valid(s) return s and string.match(s, '^%d+$') and (#s < 19 or (#s == 19 and s <= '9223372036854775807')) end
local function larger(a,b) return #a > #b or (#a == #b and a > b) end
if redis.call('GET',KEYS[7]) ~= ARGV[13] then return -1 end
`

var quotaIncrementScript = redis.NewScript(quotaDecimalLua + `
for i=1,6 do
 local value=redis.call('GET',KEYS[i])
 if value and not valid(value) then return redis.error_reply('invalid quota counter') end
end
for i=1,6 do
 -- Missing buckets must remain missing: the next DB seed includes this receipt.
 if redis.call('EXISTS',KEYS[i]) == 1 then
  local result=redis.pcall('INCRBY',KEYS[i],ARGV[(i-1)*2+1])
  if type(result)=='table' and result.err then redis.call('SET',KEYS[i],'9223372036854775807') end
  redis.call('PEXPIREAT',KEYS[i],ARGV[(i-1)*2+2])
 end
end
return 1
`)
var quotaCurrentScript = redis.NewScript(quotaDecimalLua + `
for i=1,6 do
 local value=ARGV[(i-1)*2+1]
 local current=redis.call('GET',KEYS[i])
 if valid(current) and larger(current,value) then value=current end
 redis.call('SET',KEYS[i],value,'PXAT',ARGV[(i-1)*2+2])
end
return 1
`)
