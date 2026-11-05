package infrastructure

import (
	"context"
	"errors"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"

	adminDomain "bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/usagetime"
	userInfra "bosapi/backend/internal/user/infrastructure"
)

// The repository and Redis transport are deliberately local test doubles. No
// test in these files opens a connection to Redis or a database.
type quotaTestRepository struct {
	mu                           sync.Mutex
	config                       adminDomain.OrganizationRateLimit
	usage                        userInfra.OrganizationPeriodicUsage
	configErr, usageErr, listErr error
	configReads, usageReads      int
	beforeConfig, beforeUsage    func(context.Context) error
}

func (r *quotaTestRepository) OrganizationRateLimit(ctx context.Context, id uint64) (adminDomain.OrganizationRateLimit, error) {
	r.mu.Lock()
	r.configReads++
	config, err, hook := r.config, r.configErr, r.beforeConfig
	r.mu.Unlock()
	config.OrganizationID = id
	if hook != nil {
		if e := hook(ctx); e != nil {
			return config, e
		}
	}
	return config, err
}
func (r *quotaTestRepository) OrganizationUsage(ctx context.Context, id uint64, _ time.Time) (userInfra.OrganizationPeriodicUsage, error) {
	r.mu.Lock()
	r.usageReads++
	usage, err, hook := r.usage, r.usageErr, r.beforeUsage
	r.mu.Unlock()
	usage.OrganizationID = id
	if hook != nil {
		if e := hook(ctx); e != nil {
			return usage, e
		}
	}
	return usage, err
}
func (r *quotaTestRepository) ConfiguredOrganizations(context.Context) ([]uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []uint64{1}, r.listErr
}
func (r *quotaTestRepository) reads() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.configReads, r.usageReads
}
func (r *quotaTestRepository) commit(tokens int64, amount float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage.DailyTokens += tokens
	r.usage.WeeklyTokens += tokens
	r.usage.MonthlyTokens += tokens
	r.usage.DailyAmount += amount
	r.usage.WeeklyAmount += amount
	r.usage.MonthlyAmount += amount
}
func quotaTestPtr[T any](v T) *T { return &v }
func quotaTestNow() time.Time    { return time.Date(2026, 10, 14, 4, 0, 0, 0, time.UTC) }
func quotaTestLimited() *quotaTestRepository {
	return &quotaTestRepository{config: adminDomain.OrganizationRateLimit{OrganizationID: 1, DailyQuotaTokens: quotaTestPtr(int64(100))}}
}
func quotaTestUsage(tokens int64, amount float64) userInfra.OrganizationPeriodicUsage {
	return userInfra.OrganizationPeriodicUsage{OrganizationID: 1, DailyTokens: tokens, WeeklyTokens: tokens, MonthlyTokens: tokens, DailyAmount: amount, WeeklyAmount: amount, MonthlyAmount: amount}
}
func quotaTestDimension(index int, limit int64) adminDomain.OrganizationRateLimit {
	c := adminDomain.OrganizationRateLimit{OrganizationID: 1}
	switch index {
	case 0:
		c.DailyQuotaTokens = quotaTestPtr(limit)
	case 1:
		c.DailyQuotaAmount = quotaTestPtr(float64(limit))
	case 2:
		c.WeeklyQuotaTokens = quotaTestPtr(limit)
	case 3:
		c.WeeklyQuotaAmount = quotaTestPtr(float64(limit))
	case 4:
		c.MonthlyQuotaTokens = quotaTestPtr(limit)
	case 5:
		c.MonthlyQuotaAmount = quotaTestPtr(float64(limit))
	}
	return c
}

var quotaTestReasons = []string{"organization_daily_tokens", "organization_daily_amount", "organization_weekly_tokens", "organization_weekly_amount", "organization_monthly_tokens", "organization_monthly_amount"}

func TestOrganizationQuotaSixLimits(t *testing.T) {
	now := quotaTestNow()
	resets := []time.Time{
		time.Date(2026, 10, 14, 16, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 18, 16, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC),
	}
	for i, reason := range quotaTestReasons {
		t.Run(reason, func(t *testing.T) {
			for _, used := range []int64{0, 9, 10, 11} {
				u := quotaTestUsage(used, float64(used))
				if d := EvaluateOrganizationQuota(adminDomain.OrganizationRateLimit{}, u, now); d != nil {
					t.Fatalf("nil limits denied: %+v", d)
				}
				d := EvaluateOrganizationQuota(quotaTestDimension(i, 10), u, now)
				if used < 10 {
					if d != nil {
						t.Fatalf("usage %d below limit denied: %+v", used, d)
					}
				} else if d == nil || d.Reason != reason || d.Limit != "10" || d.RetryAfter != resets[i/2].Sub(now) {
					t.Fatalf("usage %d: denial=%+v", used, d)
				}
			}
			r := &quotaTestRepository{config: quotaTestDimension(i, 0)}
			c := NewOrganizationQuotaCounter(nil, r, "unit")
			allowed, d, err := c.Check(t.Context(), 1, now)
			if err != nil || allowed || d == nil || d.Reason != reason || d.Limit != "0" || d.RetryAfter != 0 {
				t.Fatalf("zero limit: allowed=%v denial=%+v err=%v", allowed, d, err)
			}
			if _, reads := r.reads(); reads != 0 {
				t.Fatalf("zero limit queried usage %d times", reads)
			}
		})
	}
}

func TestOrganizationQuotaRetryCalendarBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		index    int
		boundary time.Time
		next     time.Duration
	}{
		{"day", 0, time.Date(2026, 10, 14, 16, 0, 0, 0, time.UTC), 24 * time.Hour},
		{"Monday", 2, time.Date(2026, 10, 18, 16, 0, 0, 0, time.UTC), 7 * 24 * time.Hour},
		{"month", 4, time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC), 30 * 24 * time.Hour},
		{"year", 4, time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC), 31 * 24 * time.Hour},
		{"leap February", 4, time.Date(2028, 1, 31, 16, 0, 0, 0, time.UTC), 29 * 24 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
				now := tc.boundary.Add(offset).In(time.FixedZone("host", -7*3600))
				want := tc.next - offset
				if offset < 0 {
					want = -offset
				}
				for _, index := range []int{tc.index, tc.index + 1} {
					d := EvaluateOrganizationQuota(quotaTestDimension(index, 1), quotaTestUsage(1, 1), now)
					if d == nil || d.RetryAfter != want {
						t.Fatalf("index=%d offset=%s: denial=%+v want=%s", index, offset, d, want)
					}
				}
			}
		})
	}
}

func TestOrganizationQuotaLongestAndPermanentBlocker(t *testing.T) {
	c := adminDomain.OrganizationRateLimit{DailyQuotaTokens: quotaTestPtr(int64(1)), WeeklyQuotaAmount: quotaTestPtr(1.0), MonthlyQuotaTokens: quotaTestPtr(int64(1))}
	d := EvaluateOrganizationQuota(c, quotaTestUsage(1, 1), quotaTestNow())
	if d == nil || d.Reason != "organization_monthly_tokens" {
		t.Fatalf("longest blocker=%+v", d)
	}
	for _, permanentFirst := range []bool{true, false} {
		if permanentFirst {
			c.DailyQuotaTokens = quotaTestPtr(int64(0))
		} else {
			c.DailyQuotaTokens = quotaTestPtr(int64(1))
			c.MonthlyQuotaAmount = quotaTestPtr(0.0)
		}
		d = EvaluateOrganizationQuota(c, quotaTestUsage(1, 1), quotaTestNow())
		if d == nil || d.RetryAfter != 0 || d.Limit != "0" {
			t.Fatalf("permanent blocker=%+v", d)
		}
	}
}

func TestOrganizationQuotaNilRedisFailsClosed(t *testing.T) {
	for _, used := range []int64{0, 99, 100} {
		r := quotaTestLimited()
		r.usage = quotaTestUsage(used, 0)
		c := NewOrganizationQuotaCounter(nil, r, "unit")
		allowed, d, err := c.Check(t.Context(), 1, quotaTestNow())
		if allowed || d != nil || !errors.Is(err, errQuotaUnavailable) {
			t.Fatalf("usage=%d allowed=%v denial=%+v err=%v", used, allowed, d, err)
		}
		if _, reads := r.reads(); reads != 0 {
			t.Fatalf("unexpected DB usage fallback: %d", reads)
		}
	}
}

func TestOrganizationQuotaSnapshotGraceAndRollover(t *testing.T) {
	now := quotaTestNow()
	for _, tc := range []struct {
		name            string
		observed, check time.Time
		valid           bool
	}{
		{"fresh", now, now.Add(time.Second), true},
		{"grace inclusive", now, now.Add(10 * time.Second), true},
		{"expired", now, now.Add(10*time.Second + time.Nanosecond), false},
		{"future observation", now, now.Add(-time.Nanosecond), false},
		{"day rollover", time.Date(2026, 10, 14, 15, 59, 59, 0, time.UTC), time.Date(2026, 10, 14, 16, 0, 0, 0, time.UTC), false},
		{"week rollover", time.Date(2026, 10, 18, 15, 59, 59, 0, time.UTC), time.Date(2026, 10, 18, 16, 0, 0, 0, time.UTC), false},
		{"month rollover", time.Date(2026, 10, 31, 15, 59, 59, 0, time.UTC), time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, used := range []int64{99, 100} {
				c := NewOrganizationQuotaCounter(nil, quotaTestLimited(), "unit")
				c.remember(1, quotaTestUsage(used, 0), tc.observed)
				allowed, d, err := c.Check(t.Context(), 1, tc.check)
				if !tc.valid {
					if allowed || d != nil || err == nil {
						t.Fatalf("invalid snapshot accepted: allowed=%v denial=%+v err=%v", allowed, d, err)
					}
				} else if err != nil || allowed != (used < 100) || (d != nil) != (used >= 100) {
					t.Fatalf("valid snapshot: used=%d allowed=%v denial=%+v err=%v", used, allowed, d, err)
				}
			}
		})
	}
}

func TestOrganizationQuotaInvalidateClearsConfigAndSnapshot(t *testing.T) {
	r := quotaTestLimited()
	c := NewOrganizationQuotaCounter(nil, r, "unit")
	now := quotaTestNow()
	c.remember(1, quotaTestUsage(1, 0), now)
	if allowed, _, err := c.Check(t.Context(), 1, now); !allowed || err != nil {
		t.Fatalf("initial check allowed=%v err=%v", allowed, err)
	}
	r.config.DailyQuotaTokens = quotaTestPtr(int64(0))
	c.Invalidate(1)
	if _, ok := c.recent(1, now); ok {
		t.Fatal("invalidated snapshot retained")
	}
	allowed, d, err := c.Check(t.Context(), 1, now)
	if allowed || err != nil || d == nil || d.Limit != "0" {
		t.Fatalf("stale configuration: allowed=%v denial=%+v err=%v", allowed, d, err)
	}
	if reads, _ := r.reads(); reads != 2 {
		t.Fatalf("config reads=%d want 2", reads)
	}
}

func TestOrganizationQuotaInvalidateDuringConfigRead(t *testing.T) {
	r := quotaTestLimited()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	r.beforeConfig = func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c := NewOrganizationQuotaCounter(nil, r, "unit")
	result := make(chan error, 1)
	go func() { _, _, err := c.Check(t.Context(), 1, quotaTestNow()); result <- err }()
	quotaTestWait(t, entered)
	c.Invalidate(1)
	r.mu.Lock()
	r.config = adminDomain.OrganizationRateLimit{}
	r.beforeConfig = nil
	r.mu.Unlock()
	once.Do(func() { close(release) })
	if err := quotaTestResult(t, result); err == nil {
		t.Fatal("in-flight stale configuration accepted")
	}
	if allowed, _, err := c.Check(t.Context(), 1, quotaTestNow()); !allowed || err != nil {
		t.Fatalf("new configuration: allowed=%v err=%v", allowed, err)
	}
}

func TestOrganizationQuotaFailuresAndAnonymous(t *testing.T) {
	failure := errors.New("configuration unavailable")
	for _, repository := range []OrganizationQuotaRepository{nil, &quotaTestRepository{configErr: failure}} {
		c := NewOrganizationQuotaCounter(nil, repository, "unit")
		if allowed, d, err := c.Check(t.Context(), 1, quotaTestNow()); allowed || d != nil || err == nil {
			t.Fatalf("allowed=%v denial=%+v err=%v", allowed, d, err)
		}
		if allowed, d, err := c.Check(t.Context(), 0, quotaTestNow()); !allowed || d != nil || err != nil {
			t.Fatalf("anonymous allowed=%v denial=%+v err=%v", allowed, d, err)
		}
	}
}

func TestOrganizationQuotaAmountConversion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input float64
		want  int64
	}{
		{"zero", 0, 0}, {"negative", -1, 0}, {"NaN", math.NaN(), 0}, {"negative infinity", math.Inf(-1), 0},
		{"positive infinity", math.Inf(1), math.MaxInt64}, {"overflow", math.MaxFloat64, math.MaxInt64},
		{"one unit", 0.00000001, 1}, {"round down", 0.000000004, 0}, {"round up", 0.000000006, 1}, {"fixed point", 1.23456789, 123456789},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := amountInt(tc.input); got != tc.want {
				t.Fatalf("amountInt(%v)=%d want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestOrganizationQuotaRedisDecoding(t *testing.T) {
	large := int64(1<<53) + 1
	good := []any{strconv.FormatInt(large, 10), "123456789", "2", "3", "9223372036854775807", "0"}
	u, err := usageFromRedis(good)
	if err != nil || u.DailyTokens != large || u.MonthlyTokens != math.MaxInt64 || u.DailyAmount != 1.23456789 || u.WeeklyAmount != 0.00000003 {
		t.Fatalf("usage=%+v err=%v", u, err)
	}
	for _, bad := range []any{nil, int64(1), "-1", "1.5", "NaN", "9223372036854775808", ""} {
		for i := range good {
			values := append([]any(nil), good...)
			values[i] = bad
			if _, err := usageFromRedis(values); err == nil {
				t.Fatalf("accepted bad value %#v at %d", bad, i)
			}
		}
	}
	for _, size := range []int{0, 5, 7} {
		if _, err := usageFromRedis(make([]any, size)); err == nil {
			t.Fatalf("accepted %d values", size)
		}
	}
}

func TestOrganizationQuotaCloseWithoutRedisDoesNotDeadlock(t *testing.T) {
	for _, start := range []bool{false, true} {
		c := NewOrganizationQuotaCounter(nil, quotaTestLimited(), "unit")
		done := make(chan struct{})
		go func() {
			if start {
				c.Start()
				c.Start()
			}
			c.Close()
			c.Close()
			c.Start()
			close(done)
		}()
		quotaTestWait(t, done)
	}
}

func quotaTestWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for test operation")
	}
}
func quotaTestResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for test result")
		return nil
	}
}

func TestOrganizationQuotaKeysAndExpirations(t *testing.T) {
	now := time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC)
	keys, args := quotaArguments("unit", 42, now, []int64{1, 2, 3, 4, 5, 6})
	wantKeys := []string{"unit:{42}:daily:20270101:tokens", "unit:{42}:daily:20270101:amount", "unit:{42}:weekly:20261228:tokens", "unit:{42}:weekly:20261228:amount", "unit:{42}:monthly:202701:tokens", "unit:{42}:monthly:202701:amount"}
	ends := []time.Time{time.Date(2027, 1, 1, 16, 5, 0, 0, time.UTC), time.Date(2027, 1, 3, 16, 5, 0, 0, time.UTC), time.Date(2027, 1, 31, 16, 5, 0, 0, time.UTC)}
	if len(keys) != 6 || len(args) != 12 {
		t.Fatalf("keys=%v args=%v", keys, args)
	}
	for i, key := range keys {
		if key != wantKeys[i] || args[i*2] != strconv.Itoa(i+1) || args[i*2+1] != ends[i/2].UnixMilli() {
			t.Fatalf("bucket %d: key=%s args=%v", i, key, args[i*2:i*2+2])
		}
	}
	// All boundaries follow the business calendar even with a foreign host zone.
	other := organizationQuotaKeys("unit", 42, now.In(time.FixedZone("host", -7*3600)))
	for i := range keys {
		if keys[i] != other[i] {
			t.Fatalf("host timezone changed key %d", i)
		}
	}
	if usagetime.DayStart(now).Equal(usagetime.DayStart(now.Add(-time.Nanosecond))) {
		t.Fatal("fixture must cross a business-day boundary")
	}
}
