package infrastructure

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"math"
	"net"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	adminDomain "bosapi/backend/internal/admin/domain"
	userInfra "bosapi/backend/internal/user/infrastructure"

	"github.com/redis/go-redis/v9"
)

type quotaTestScriptCall struct {
	method, hash string
	keys         []string
	args         []any
}

// quotaTestRedis models just the transport contract needed by the counter. It
// does not execute Lua: tests assert dispatch, exact arguments, and client-side
// coordination independently of this small model of the script responses.
// Embedding a nil UniversalClient makes unexpected Redis operations fail loudly.
type quotaTestRedis struct {
	redis.UniversalClient
	mu                                  sync.Mutex
	values                              map[string]string
	loaded                              map[string]bool
	calls                               []quotaTestScriptCall
	delCalls                            [][]string
	mgets, locks, deletes               int
	readErr, lockErr, scriptErr, delErr error
	fenceLost                           bool
	contended                           chan struct{}
	contendedOnce                       sync.Once
}

func newQuotaTestRedis() *quotaTestRedis {
	return &quotaTestRedis{values: map[string]string{}, loaded: map[string]bool{}, contended: make(chan struct{})}
}
func (s *quotaTestRedis) MGet(ctx context.Context, keys ...string) *redis.SliceCmd {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mgets++
	if ctx.Err() != nil {
		return redis.NewSliceResult(nil, ctx.Err())
	}
	if s.readErr != nil {
		return redis.NewSliceResult(nil, s.readErr)
	}
	values := make([]any, len(keys))
	for i, key := range keys {
		if v, ok := s.values[key]; ok {
			values[i] = v
		}
	}
	return redis.NewSliceResult(values, nil)
}
func (s *quotaTestRedis) SetNX(ctx context.Context, key string, value any, expiration time.Duration) *redis.BoolCmd {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locks++
	if ctx.Err() != nil {
		return redis.NewBoolResult(false, ctx.Err())
	}
	if s.lockErr != nil {
		return redis.NewBoolResult(false, s.lockErr)
	}
	deadline, ok := ctx.Deadline()
	if !ok || expiration <= time.Until(deadline) {
		return redis.NewBoolResult(false, errors.New("lock lease must outlive operation deadline"))
	}
	if _, ok := s.values[key]; ok {
		s.contendedOnce.Do(func() { close(s.contended) })
		return redis.NewBoolResult(false, nil)
	}
	s.values[key] = fmt.Sprint(value)
	return redis.NewBoolResult(true, nil)
}
func (s *quotaTestRedis) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	s.delCalls = append(s.delCalls, append([]string(nil), keys...))
	if ctx.Err() != nil {
		return redis.NewIntResult(0, ctx.Err())
	}
	if s.delErr != nil {
		return redis.NewIntResult(0, s.delErr)
	}
	var count int64
	for _, key := range keys {
		if _, ok := s.values[key]; ok {
			count++
			delete(s.values, key)
		}
	}
	return redis.NewIntResult(count, nil)
}
func (s *quotaTestRedis) EvalSha(ctx context.Context, hash string, keys []string, args ...any) *redis.Cmd {
	return s.eval(ctx, "evalsha", hash, keys, args)
}
func (s *quotaTestRedis) Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd {
	return s.eval(ctx, "eval", fmt.Sprintf("%x", sha1.Sum([]byte(script))), keys, args)
}
func (s *quotaTestRedis) eval(ctx context.Context, method, hash string, keys []string, args []any) *redis.Cmd {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, quotaTestScriptCall{method, hash, append([]string(nil), keys...), append([]any(nil), args...)})
	if ctx.Err() != nil {
		return redis.NewCmdResult(nil, ctx.Err())
	}
	if method == "evalsha" && !s.loaded[hash] {
		return redis.NewCmdResult(nil, redis.ErrNoScript)
	}
	s.loaded[hash] = true
	if hash == quotaUnlockScript.Hash() {
		if len(keys) != 1 || len(args) != 1 {
			return redis.NewCmdResult(nil, errors.New("bad unlock arguments"))
		}
		if s.values[keys[0]] == args[0] {
			delete(s.values, keys[0])
			return redis.NewCmdResult(int64(1), nil)
		}
		return redis.NewCmdResult(int64(0), nil)
	}
	if hash != quotaCurrentScript.Hash() && hash != quotaIncrementScript.Hash() {
		return redis.NewCmdResult(nil, errors.New("unexpected script"))
	}
	if s.scriptErr != nil {
		return redis.NewCmdResult(nil, s.scriptErr)
	}
	if len(keys) != 7 || len(args) != 13 {
		return redis.NewCmdResult(nil, errors.New("expected six quota buckets and lock fence"))
	}
	if s.fenceLost || s.values[keys[6]] != args[12] {
		return redis.NewCmdResult(int64(-1), nil)
	}
	for i, key := range keys[:6] {
		text, ok := args[2*i].(string)
		if !ok {
			return redis.NewCmdResult(nil, errors.New("quota integer must be a decimal string"))
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || n < 0 {
			return redis.NewCmdResult(nil, errors.New("bad quota integer"))
		}
		if _, ok := args[2*i+1].(int64); !ok {
			return redis.NewCmdResult(nil, errors.New("expiry must be Unix milliseconds"))
		}
		old, exists := s.values[key]
		previous, parseErr := strconv.ParseInt(old, 10, 64)
		if hash == quotaCurrentScript.Hash() {
			if parseErr == nil && previous > n {
				n = previous
			}
		} else {
			if !exists {
				continue
			}
			if parseErr != nil || previous < 0 {
				return redis.NewCmdResult(nil, errors.New("invalid quota counter"))
			}
			if n > math.MaxInt64-previous {
				n = math.MaxInt64
			} else {
				n += previous
			}
		}
		s.values[key] = strconv.FormatInt(n, 10)
	}
	return redis.NewCmdResult(int64(1), nil)
}
func (s *quotaTestRedis) scriptCalls(hash string) []quotaTestScriptCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var calls []quotaTestScriptCall
	for _, call := range s.calls {
		if call.hash == hash {
			calls = append(calls, call)
		}
	}
	return calls
}
func (s *quotaTestRedis) assertIdle(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mgets != 0 || s.locks != 0 || s.deletes != 0 || len(s.calls) != 0 {
		t.Fatalf("unexpected Redis work: reads=%d locks=%d deletes=%d scripts=%d", s.mgets, s.locks, s.deletes, len(s.calls))
	}
}
func quotaTestAssertUsage(t *testing.T, c *OrganizationQuotaCounter, now time.Time, want userInfra.OrganizationPeriodicUsage) {
	t.Helper()
	got, err := c.Usage(t.Context(), 1, now)
	if err != nil || got != want {
		t.Fatalf("usage=%+v err=%v want=%+v", got, err, want)
	}
}
func quotaTestAssertNoBuckets(t *testing.T, s *quotaTestRedis, now time.Time) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range organizationQuotaKeys("unit", 1, now) {
		if v, ok := s.values[key]; ok {
			t.Fatalf("unexpected counter %s=%s", key, v)
		}
	}
}

func TestOrganizationQuotaUnlimitedAvoidsAccountingIO(t *testing.T) {
	r, s := &quotaTestRepository{}, newQuotaTestRedis()
	c := NewOrganizationQuotaCounter(s, r, "unit")
	for range 3 {
		if allowed, d, err := c.Check(t.Context(), 1, quotaTestNow()); !allowed || d != nil || err != nil {
			t.Fatalf("allowed=%v denial=%+v err=%v", allowed, d, err)
		}
	}
	persisted := 0
	if err := c.Settle(t.Context(), 1, 5, 1.2, quotaTestNow(), func(context.Context) (bool, error) { persisted++; return true, nil }); err != nil {
		t.Fatal(err)
	}
	if err := c.Calibrate(t.Context(), quotaTestNow()); err != nil {
		t.Fatal(err)
	}
	if _, reads := r.reads(); reads != 0 || persisted != 1 {
		t.Fatalf("DB usage reads=%d persist calls=%d", reads, persisted)
	}
	s.assertIdle(t)

	// Explicit admin Usage is allowed one DB read, then uses its display cache.
	r.usage = quotaTestUsage(7, 0.25)
	quotaTestAssertUsage(t, c, quotaTestNow(), r.usage)
	quotaTestAssertUsage(t, c, quotaTestNow().Add(time.Second), r.usage)
	if _, reads := r.reads(); reads != 1 {
		t.Fatalf("display usage reads=%d want 1", reads)
	}
	s.assertIdle(t)
}

func TestOrganizationQuotaPartialMissUsesMergedCounters(t *testing.T) {
	r, s := quotaTestLimited(), newQuotaTestRedis()
	r.usage = quotaTestUsage(10, 1)
	c := NewOrganizationQuotaCounter(s, r, "unit")
	now := quotaTestNow()
	keys := organizationQuotaKeys("unit", 1, now)
	// Redis already observed more usage than the ledger snapshot, while an
	// unrelated amount bucket was evicted. Repair must not admit this request.
	s.values[keys[0]] = "100"
	allowed, denial, err := c.Check(t.Context(), 1, now)
	if err != nil || allowed || denial == nil || denial.Reason != "organization_daily_tokens" {
		t.Fatalf("allowed=%v denial=%+v err=%v", allowed, denial, err)
	}
	want := quotaTestUsage(10, 1)
	want.DailyTokens = 100
	quotaTestAssertUsage(t, c, now, want)
	if snapshot, ok := c.recent(1, now); !ok || snapshot != want {
		t.Fatalf("snapshot=%+v valid=%v want=%+v", snapshot, ok, want)
	}
}

func TestOrganizationQuotaColdStartConcurrentReplicas(t *testing.T) {
	r, s := quotaTestLimited(), newQuotaTestRedis()
	r.usage = quotaTestUsage(23, 1.25)
	const replicas = 12
	start := make(chan struct{})
	results := make(chan error, replicas)
	for range replicas {
		c := NewOrganizationQuotaCounter(s, r, "unit")
		go func() {
			<-start
			u, err := c.Usage(t.Context(), 1, quotaTestNow())
			if err == nil && u != r.usage {
				err = fmt.Errorf("usage=%+v want %+v", u, r.usage)
			}
			results <- err
		}()
	}
	close(start)
	for range replicas {
		if err := quotaTestResult(t, results); err != nil {
			t.Fatal(err)
		}
	}
	if _, reads := r.reads(); reads != 1 {
		t.Fatalf("cold-start DB usage reads=%d want 1", reads)
	}
	calls := s.scriptCalls(quotaCurrentScript.Hash())
	if len(calls) != 2 || calls[0].method != "evalsha" || calls[1].method != "eval" {
		t.Fatalf("expected one seed with NOSCRIPT fallback: %+v", calls)
	}
	if !reflect.DeepEqual(calls[0].keys, calls[1].keys) || !reflect.DeepEqual(calls[0].args, calls[1].args) {
		t.Fatal("EVAL fallback changed seed arguments")
	}
}

func TestOrganizationQuotaSettleDispatchAndDuplicates(t *testing.T) {
	r, s := quotaTestLimited(), newQuotaTestRedis()
	r.usage = quotaTestUsage(10, 0.25)
	c := NewOrganizationQuotaCounter(s, r, "unit")
	now := quotaTestNow()
	quotaTestAssertUsage(t, c, now, r.usage)
	persisted := 0
	for _, inserted := range []bool{true, false} {
		err := c.Settle(t.Context(), 1, 7, 1.23456789, now, func(ctx context.Context) (bool, error) {
			persisted++
			if _, ok := ctx.Deadline(); !ok {
				return false, errors.New("persist has no lock deadline")
			}
			s.mu.Lock()
			held := s.values[c.lockKey(1)] != ""
			s.mu.Unlock()
			if !held {
				return false, errors.New("persist ran outside lock")
			}
			if inserted {
				r.commit(7, 1.23456789)
			}
			return inserted, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !inserted {
			quotaTestAssertNoBuckets(t, s, now)
		}
		quotaTestAssertUsage(t, c, now, quotaTestUsage(17, 1.48456789))
	}
	if persisted != 2 {
		t.Fatalf("persist called %d times", persisted)
	}
	calls := s.scriptCalls(quotaIncrementScript.Hash())
	if len(calls) != 2 || calls[0].method != "evalsha" || calls[1].method != "eval" {
		t.Fatalf("duplicate dispatched increment: %+v", calls)
	}
	call := calls[1]
	wantKeys := append(organizationQuotaKeys("unit", 1, now), "unit:{1}:lock")
	if !reflect.DeepEqual(call.keys, wantKeys) {
		t.Fatalf("keys=%v want=%v", call.keys, wantKeys)
	}
	ends := []time.Time{time.Date(2026, 10, 14, 16, 5, 0, 0, time.UTC), time.Date(2026, 10, 18, 16, 5, 0, 0, time.UTC), time.Date(2026, 10, 31, 16, 5, 0, 0, time.UTC)}
	for i := 0; i < 6; i++ {
		want := "7"
		if i%2 == 1 {
			want = "123456789"
		}
		if call.args[2*i] != want || call.args[2*i+1] != ends[i/2].UnixMilli() {
			t.Fatalf("bucket %d args=%v", i, call.args[2*i:2*i+2])
		}
	}
	token, ok := call.args[12].(string)
	if !ok || token == "" {
		t.Fatalf("missing fence token: %v", call.args[12])
	}
	unlocks := s.scriptCalls(quotaUnlockScript.Hash())
	found := false
	for _, u := range unlocks {
		if reflect.DeepEqual(u.keys, []string{"unit:{1}:lock"}) && reflect.DeepEqual(u.args, []any{token}) {
			found = true
		}
	}
	if !found {
		t.Fatal("settlement did not release its own lock token")
	}
}

func TestOrganizationQuotaSettlePreservesInt64Arguments(t *testing.T) {
	r, s := quotaTestLimited(), newQuotaTestRedis()
	large := int64(1<<53) + 1
	r.usage = quotaTestUsage(large, 0)
	c := NewOrganizationQuotaCounter(s, r, "unit")
	now := quotaTestNow()
	quotaTestAssertUsage(t, c, now, r.usage)
	if err := c.Settle(t.Context(), 1, large, 0.00000001, now, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	quotaTestAssertUsage(t, c, now, quotaTestUsage(large*2, 0.00000001))
	for _, hash := range []string{quotaCurrentScript.Hash(), quotaIncrementScript.Hash()} {
		for _, call := range s.scriptCalls(hash) {
			for _, i := range []int{0, 4, 8} {
				if call.args[i] != "9007199254740993" {
					t.Fatalf("integer precision lost: argument %d=%#v", i, call.args[i])
				}
			}
		}
	}
	// Calibration must not lower the modelled committed counters.
	if err := c.Calibrate(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	quotaTestAssertUsage(t, c, now, quotaTestUsage(large*2, 0.00000001))
}

func TestOrganizationQuotaDatabaseFailureDoesNotCacheZero(t *testing.T) {
	for _, operation := range []string{"seed", "calibrate"} {
		t.Run(operation, func(t *testing.T) {
			r, s := quotaTestLimited(), newQuotaTestRedis()
			failure := errors.New("usage query failed")
			r.usageErr = failure
			c := NewOrganizationQuotaCounter(s, r, "unit")
			var err error
			if operation == "seed" {
				_, _, err = c.Check(t.Context(), 1, quotaTestNow())
			} else {
				err = c.Calibrate(t.Context(), quotaTestNow())
			}
			if !errors.Is(err, failure) {
				t.Fatalf("err=%v want repository failure", err)
			}
			quotaTestAssertNoBuckets(t, s, quotaTestNow())
			if _, ok := c.recent(1, quotaTestNow()); ok {
				t.Fatal("failed query installed a valid snapshot")
			}
			if len(s.scriptCalls(quotaCurrentScript.Hash())) != 0 {
				t.Fatal("failed query dispatched a seed")
			}
			r.usageErr = nil
			r.usage = quotaTestUsage(100, 1)
			allowed, d, err := c.Check(t.Context(), 1, quotaTestNow())
			if err != nil || allowed || d == nil {
				t.Fatalf("retry used zero snapshot: allowed=%v denial=%+v err=%v", allowed, d, err)
			}
			if _, reads := r.reads(); reads != 2 {
				t.Fatalf("DB usage reads=%d want 2", reads)
			}
		})
	}
}

func TestOrganizationQuotaSettlementFailuresStillPersistOnce(t *testing.T) {
	for _, mode := range []string{"nil Redis", "lock failure", "script failure", "lost lease", "persist failure", "config failure"} {
		t.Run(mode, func(t *testing.T) {
			r, s := quotaTestLimited(), newQuotaTestRedis()
			c := NewOrganizationQuotaCounter(s, r, "unit")
			now := quotaTestNow()
			quotaTestAssertUsage(t, c, now, quotaTestUsage(0, 0))
			failure := errors.New(mode)
			switch mode {
			case "nil Redis":
				c.client = nil
			case "lock failure":
				s.lockErr = failure
			case "script failure":
				s.scriptErr = failure
			case "lost lease":
				s.fenceLost = true
			case "config failure":
				c.Invalidate(1)
				r.configErr = failure
			}
			persisted := 0
			err := c.Settle(t.Context(), 1, 3, 0.5, now, func(context.Context) (bool, error) {
				persisted++
				if mode == "persist failure" {
					return false, failure
				}
				return true, nil
			})
			if persisted != 1 {
				t.Fatalf("persist calls=%d want 1", persisted)
			}
			if mode == "persist failure" {
				if !errors.Is(err, failure) {
					t.Fatalf("ledger error=%v", err)
				}
			} else if err != nil {
				t.Fatalf("counter failure cancelled billing: %v", err)
			}
			if _, ok := c.recent(1, now); ok {
				t.Fatal("failed settlement retained stale snapshot")
			}
			if mode != "nil Redis" {
				quotaTestAssertNoBuckets(t, s, now)
			}
			if mode == "persist failure" && len(s.scriptCalls(quotaIncrementScript.Hash())) != 0 {
				t.Fatal("failed ledger write incremented counters")
			}
		})
	}
}

func TestOrganizationQuotaMissingBucketsRepairFromDatabase(t *testing.T) {
	for _, missing := range []int{-1, 0, 1, 2, 3, 4, 5} {
		t.Run(fmt.Sprintf("missing_%d", missing), func(t *testing.T) {
			r, s := quotaTestLimited(), newQuotaTestRedis()
			r.usage = quotaTestUsage(10, 0.25)
			c := NewOrganizationQuotaCounter(s, r, "unit")
			now := quotaTestNow()
			quotaTestAssertUsage(t, c, now, r.usage)
			keys := organizationQuotaKeys("unit", 1, now)
			if missing < 0 {
				s.Del(t.Context(), keys...)
			} else {
				s.Del(t.Context(), keys[missing])
			}
			if err := c.Settle(t.Context(), 1, 5, 0.5, now, func(context.Context) (bool, error) { r.commit(5, 0.5); return true, nil }); err != nil {
				t.Fatal(err)
			}
			// Missing counters cannot be initialized to just this receipt.
			s.mu.Lock()
			for i, key := range keys {
				if missing < 0 || i == missing {
					if _, ok := s.values[key]; ok {
						s.mu.Unlock()
						t.Fatalf("settlement initialized missing bucket %s", key)
					}
				}
			}
			s.mu.Unlock()
			quotaTestAssertUsage(t, c, now, quotaTestUsage(15, 0.75))
			if _, reads := r.reads(); reads != 2 {
				t.Fatalf("DB reads=%d want initial seed + repair", reads)
			}
		})
	}
}

func TestOrganizationQuotaRedisOutageUsesOnlyRecentSnapshot(t *testing.T) {
	r, s := quotaTestLimited(), newQuotaTestRedis()
	r.usage = quotaTestUsage(12, 0)
	c := NewOrganizationQuotaCounter(s, r, "unit")
	now := quotaTestNow()
	quotaTestAssertUsage(t, c, now, r.usage)
	s.readErr = errors.New("Redis unavailable")
	quotaTestAssertUsage(t, c, now.Add(10*time.Second), r.usage)
	if allowed, d, err := c.Check(t.Context(), 1, now.Add(10*time.Second+time.Nanosecond)); allowed || d != nil || !errors.Is(err, s.readErr) {
		t.Fatalf("stale fallback allowed=%v denial=%+v err=%v", allowed, d, err)
	}
	if _, reads := r.reads(); reads != 1 {
		t.Fatalf("outage queried DB usage %d times", reads)
	}
}

func TestOrganizationQuotaCorruptCountersFailClosed(t *testing.T) {
	for _, bad := range []string{"-1", "not-an-integer", "9223372036854775808"} {
		r, s := quotaTestLimited(), newQuotaTestRedis()
		c := NewOrganizationQuotaCounter(s, r, "unit")
		for _, key := range organizationQuotaKeys("unit", 1, quotaTestNow()) {
			s.values[key] = "0"
		}
		s.values[organizationQuotaKeys("unit", 1, quotaTestNow())[0]] = bad
		if allowed, d, err := c.Check(t.Context(), 1, quotaTestNow()); allowed || d != nil || err == nil {
			t.Fatalf("bad=%q allowed=%v denial=%+v err=%v", bad, allowed, d, err)
		}
		if _, reads := r.reads(); reads != 0 {
			t.Fatalf("corrupt value fell back to DB: reads=%d", reads)
		}
	}
}

func TestOrganizationQuotaSeedAndCalibrationFenceSettlement(t *testing.T) {
	for _, operation := range []string{"seed", "calibrate"} {
		for _, first := range []string{"read", "settle"} {
			t.Run(operation+"/"+first+"_first", func(t *testing.T) {
				r, s := quotaTestLimited(), newQuotaTestRedis()
				r.usage = quotaTestUsage(10, 0.25)
				reader := NewOrganizationQuotaCounter(s, r, "unit")
				writer := NewOrganizationQuotaCounter(s, r, "unit")
				now := quotaTestNow()
				if operation == "calibrate" {
					quotaTestAssertUsage(t, reader, now, r.usage)
				}
				entered, release, persistEntered := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
				block := func(ctx context.Context) error {
					close(entered)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				if first == "read" {
					r.beforeUsage = block
				}
				read := func() error {
					if operation == "calibrate" {
						return reader.Calibrate(t.Context(), now)
					}
					_, err := reader.Usage(t.Context(), 1, now)
					return err
				}
				settle := func() error {
					return writer.Settle(t.Context(), 1, 7, 0.5, now, func(ctx context.Context) (bool, error) {
						close(persistEntered)
						r.commit(7, 0.5)
						if first == "settle" {
							if err := block(ctx); err != nil {
								return false, err
							}
						}
						return true, nil
					})
				}
				readResult, settleResult := make(chan error, 1), make(chan error, 1)
				if first == "read" {
					go func() { readResult <- read() }()
					quotaTestWait(t, entered)
					go func() { settleResult <- settle() }()
				} else {
					go func() { settleResult <- settle() }()
					quotaTestWait(t, entered)
					go func() { readResult <- read() }()
				}
				quotaTestWait(t, s.contended)
				if first == "read" {
					select {
					case <-persistEntered:
						t.Fatal("ledger committed while DB snapshot held the fence")
					default:
					}
				} else {
					_, reads := r.reads()
					want := 0
					if operation == "calibrate" {
						want = 1
					}
					if reads != want {
						t.Fatalf("DB snapshot read uncompleted settlement: reads=%d want=%d", reads, want)
					}
				}
				releaseOnce.Do(func() { close(release) })
				if err := quotaTestResult(t, readResult); err != nil {
					t.Fatal(err)
				}
				if err := quotaTestResult(t, settleResult); err != nil {
					t.Fatal(err)
				}
				quotaTestAssertUsage(t, reader, now, quotaTestUsage(17, 0.75))
				quotaTestAssertUsage(t, writer, now, quotaTestUsage(17, 0.75))
			})
		}
	}
}

func TestOrganizationQuotaCalibrateRefreshesAndPropagatesErrors(t *testing.T) {
	r, s := quotaTestLimited(), newQuotaTestRedis()
	r.usage = quotaTestUsage(10, 1)
	c := NewOrganizationQuotaCounter(s, r, "unit")
	now := quotaTestNow()
	quotaTestAssertUsage(t, c, now, r.usage)
	r.usage = quotaTestUsage(20, 2)
	if err := c.Calibrate(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	quotaTestAssertUsage(t, c, now, r.usage)
	failure := errors.New("configured organizations unavailable")
	r.listErr = failure
	if err := c.Calibrate(t.Context(), now); !errors.Is(err, failure) {
		t.Fatalf("list error=%v", err)
	}
	r.listErr = nil
	r.usageErr = failure
	if err := c.Calibrate(t.Context(), now); !errors.Is(err, failure) {
		t.Fatalf("usage error=%v", err)
	}
	quotaTestAssertUsage(t, c, now, quotaTestUsage(20, 2))
	if err := NewOrganizationQuotaCounter(nil, r, "unit").Calibrate(t.Context(), now); err != nil {
		t.Fatal(err)
	}
}

func TestOrganizationQuotaSettleInvalidatesLocalSnapshot(t *testing.T) {
	r, s := quotaTestLimited(), newQuotaTestRedis()
	r.usage = quotaTestUsage(99, 0)
	c := NewOrganizationQuotaCounter(s, r, "unit")
	now := quotaTestNow()
	quotaTestAssertUsage(t, c, now, r.usage)
	if err := c.Settle(t.Context(), 1, 1, 0, now, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	s.readErr = errors.New("Redis unavailable after settlement")
	if allowed, _, err := c.Check(t.Context(), 1, now); allowed || err == nil {
		t.Fatalf("settlement left pre-charge snapshot: allowed=%v err=%v", allowed, err)
	}
}

func TestOrganizationQuotaConfigExpiry(t *testing.T) {
	r, s := &quotaTestRepository{}, newQuotaTestRedis()
	c := NewOrganizationQuotaCounter(s, r, "unit")
	now := quotaTestNow()
	if allowed, _, err := c.Check(t.Context(), 1, now); !allowed || err != nil {
		t.Fatal("initial unlimited check failed")
	}
	r.config = adminDomain.OrganizationRateLimit{DailyQuotaTokens: quotaTestPtr(int64(0))}
	if allowed, _, err := c.Check(t.Context(), 1, now.Add(c.configTTL-time.Nanosecond)); !allowed || err != nil {
		t.Fatal("configuration expired early")
	}
	if allowed, d, err := c.Check(t.Context(), 1, now.Add(c.configTTL)); allowed || d == nil || err != nil {
		t.Fatalf("expiry: allowed=%v denial=%+v err=%v", allowed, d, err)
	}
	s.assertIdle(t)
}

func TestOrganizationQuotaDuplicateRepairsPreviouslyUncountedReceipt(t *testing.T) {
	r, s := quotaTestLimited(), newQuotaTestRedis()
	r.usage = quotaTestUsage(10, 0.25)
	c := NewOrganizationQuotaCounter(s, r, "unit")
	now := quotaTestNow()
	quotaTestAssertUsage(t, c, now, r.usage)
	// A previous DB commit succeeded, but its response and Redis update were lost.
	r.commit(7, 0.5)
	if err := c.Settle(t.Context(), 1, 7, 0.5, now, func(context.Context) (bool, error) {
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	quotaTestAssertNoBuckets(t, s, now)
	quotaTestAssertUsage(t, c, now, quotaTestUsage(17, 0.75))
	if len(s.scriptCalls(quotaIncrementScript.Hash())) != 0 {
		t.Fatal("duplicate receipt dispatched an increment")
	}
	if _, reads := r.reads(); reads != 2 {
		t.Fatalf("DB usage reads=%d want initial seed + duplicate repair", reads)
	}
}

func TestOrganizationQuotaFailedSeedDoesNotPublishSnapshot(t *testing.T) {
	for _, lostLease := range []bool{false, true} {
		r, s := quotaTestLimited(), newQuotaTestRedis()
		r.usage = quotaTestUsage(12, 0.5)
		if lostLease {
			s.fenceLost = true
		} else {
			s.scriptErr = errors.New("seed write failed")
		}
		c := NewOrganizationQuotaCounter(s, r, "unit")
		allowed, d, err := c.Check(t.Context(), 1, quotaTestNow())
		if allowed || d != nil || err == nil {
			t.Fatalf("failed seed allowed=%v denial=%+v err=%v", allowed, d, err)
		}
		if _, ok := c.recent(1, quotaTestNow()); ok {
			t.Fatal("failed Redis seed published a local snapshot")
		}
		quotaTestAssertNoBuckets(t, s, quotaTestNow())
	}
}

func TestOrganizationQuotaLockContentionHonorsCancellation(t *testing.T) {
	r, s := quotaTestLimited(), newQuotaTestRedis()
	c := NewOrganizationQuotaCounter(s, r, "unit")
	s.values[c.lockKey(1)] = "another-replica"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := c.Check(ctx, 1, quotaTestNow())
		done <- err
	}()
	quotaTestWait(t, s.contended)
	cancel()
	if err := quotaTestResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquisition error=%v", err)
	}
	if _, reads := r.reads(); reads != 0 {
		t.Fatalf("contended check queried usage %d times", reads)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values[c.lockKey(1)] != "another-replica" {
		t.Fatal("cancelled acquisition released another replica's lock")
	}
}

func TestOrganizationQuotaConstructorOwnsBoundedClientClone(t *testing.T) {
	// NewClient is lazy; a rejecting dialer also ensures this unit test cannot
	// contact a Redis server, even if constructor behavior changes.
	var dialed atomic.Bool
	source := redis.NewClient(&redis.Options{
		Addr: "unit-test.invalid:6379", DialTimeout: 5 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		PoolTimeout: 5 * time.Second, MaxRetries: 7,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			dialed.Store(true)
			return nil, errors.New("network disabled in unit tests")
		},
	})
	t.Cleanup(func() { _ = source.Close() })
	c := NewOrganizationQuotaCounter(source, quotaTestLimited(), "unit")
	t.Cleanup(c.Close)
	clone, ok := c.client.(*redis.Client)
	if !ok || clone == source || c.owned != clone {
		t.Fatal("counter must own a separate client with bounded transport settings")
	}
	options := clone.Options()
	if !options.ContextTimeoutEnabled || options.MaxRetries != 0 {
		t.Fatalf("clone timeout/retry settings=%+v", options)
	}
	for name, timeout := range map[string]time.Duration{
		"dial": options.DialTimeout, "read": options.ReadTimeout,
		"write": options.WriteTimeout, "pool": options.PoolTimeout,
	} {
		if timeout <= 0 || timeout > 300*time.Millisecond {
			t.Errorf("%s timeout=%s", name, timeout)
		}
	}
	if source.Options().ReadTimeout != 5*time.Second || source.Options().MaxRetries != 7 || source.Options().ContextTimeoutEnabled {
		t.Fatal("constructor mutated the caller's client options")
	}
	c.Close()
	c.Close()
	if dialed.Load() {
		t.Fatal("construction or closing attempted a connection")
	}
	stub := newQuotaTestRedis()
	stubCounter := NewOrganizationQuotaCounter(stub, quotaTestLimited(), "unit")
	if stubCounter.client != stub || stubCounter.owned != nil {
		t.Fatal("constructor must retain the supplied transport stub")
	}
	stubCounter.Close()
	stub.assertIdle(t)
}

func TestOrganizationQuotaTimedOutSettlementFencesInFlightSeed(t *testing.T) {
	for _, cancelAfterPersist := range []bool{false, true} {
		name := "live caller"
		if cancelAfterPersist {
			name = "caller cancelled after commit"
		}
		t.Run(name, func(t *testing.T) {
			r, s := quotaTestLimited(), newQuotaTestRedis()
			r.usage = quotaTestUsage(90, 0)
			reader := NewOrganizationQuotaCounter(s, r, "unit")
			writer := NewOrganizationQuotaCounter(s, r, "unit")
			now := quotaTestNow()

			captured, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			// OrganizationUsage captures its result before invoking this hook.
			// Keep that old 90-token snapshot in flight while settlement commits.
			r.beforeUsage = func(ctx context.Context) error {
				close(captured)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			seedResult := make(chan error, 1)
			go func() {
				_, err := reader.Usage(t.Context(), 1, now)
				seedResult <- err
			}()
			quotaTestWait(t, captured)
			s.mu.Lock()
			seedToken := s.values[reader.lockKey(1)]
			s.mu.Unlock()
			if seedToken == "" {
				t.Fatal("seed captured the database snapshot without holding its lock")
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			persistCalls := 0
			settleResult := make(chan error, 1)
			go func() {
				settleResult <- writer.Settle(ctx, 1, 20, 0, now, func(persistCtx context.Context) (bool, error) {
					persistCalls++
					if persistCtx.Err() != nil {
						return false, fmt.Errorf("fallback persist received expired acquisition context: %w", persistCtx.Err())
					}
					s.mu.Lock()
					stillSeedToken := s.values[reader.lockKey(1)]
					s.mu.Unlock()
					if stillSeedToken != seedToken {
						return false, errors.New("fallback persist must run while the seed still owns the lock")
					}
					r.commit(20, 0)
					if cancelAfterPersist {
						cancel()
					}
					return true, nil
				})
			}()
			quotaTestWait(t, s.contended)
			// Wait for the real bounded acquisition to expire, without releasing
			// the seed. The successful fallback commit must invalidate its fence.
			if err := quotaTestResult(t, settleResult); err != nil {
				t.Fatalf("fallback settlement failed: %v", err)
			}
			if persistCalls != 1 {
				t.Fatalf("persist calls=%d want 1", persistCalls)
			}
			s.mu.Lock()
			deletions := append([][]string(nil), s.delCalls...)
			_, leaseRemains := s.values[reader.lockKey(1)]
			s.mu.Unlock()
			wantKeys := append(organizationQuotaKeys("unit", 1, now), reader.lockKey(1))
			if len(deletions) != 1 || !reflect.DeepEqual(deletions[0], wantKeys) {
				t.Errorf("cleanup must atomically delete six buckets and the active lease: calls=%v", deletions)
			}
			if leaseRemains {
				t.Error("fallback settlement left the old seed's lease intact")
			}

			r.mu.Lock()
			r.beforeUsage = nil
			r.mu.Unlock()
			releaseOnce.Do(func() { close(release) })
			if err := quotaTestResult(t, seedResult); !errors.Is(err, errQuotaUnavailable) {
				t.Errorf("old seed must fail its revoked fence: err=%v", err)
			}
			if snapshot, ok := reader.recent(1, now); ok {
				t.Errorf("old seed published a valid snapshot after settlement: %+v", snapshot)
			}
			quotaTestAssertNoBuckets(t, s, now)
			quotaTestAssertUsage(t, reader, now, quotaTestUsage(110, 0))
			quotaTestAssertUsage(t, writer, now, quotaTestUsage(110, 0))
		})
	}
}

func TestOrganizationQuotaFailedIncrementAndDeleteRequireRepair(t *testing.T) {
	for _, databaseUnavailable := range []bool{false, true} {
		name := "database can repair"
		if databaseUnavailable {
			name = "database unavailable fails closed"
		}
		t.Run(name, func(t *testing.T) {
			r, s := quotaTestLimited(), newQuotaTestRedis()
			r.usage = quotaTestUsage(90, 0)
			c := NewOrganizationQuotaCounter(s, r, "unit")
			now := quotaTestNow()
			quotaTestAssertUsage(t, c, now, quotaTestUsage(90, 0))
			s.mu.Lock()
			s.readErr = errors.New("reads temporarily unavailable")
			s.scriptErr = errors.New("increment failed")
			s.delErr = errors.New("cleanup delete failed")
			s.mu.Unlock()
			persistCalls := 0
			if err := c.Settle(t.Context(), 1, 20, 0, now, func(context.Context) (bool, error) {
				persistCalls++
				r.commit(20, 0)
				return true, nil
			}); err != nil {
				t.Fatalf("Redis failures cancelled the committed receipt: %v", err)
			}
			if persistCalls != 1 {
				t.Fatalf("persist calls=%d want 1", persistCalls)
			}
			s.mu.Lock()
			deletes := s.deletes
			staleTokens := s.values[organizationQuotaKeys("unit", 1, now)[0]]
			// Reads and scripts recover, but DEL continues to fail. Successful
			// reads of all six old buckets must not bypass pending local repair.
			s.readErr, s.scriptErr = nil, nil
			s.mu.Unlock()
			if deletes == 0 || staleTokens != "90" {
				t.Fatalf("fixture must retain stale counters after failed cleanup: deletes=%d tokens=%q", deletes, staleTokens)
			}
			if snapshot, ok := c.recent(1, now); ok {
				t.Fatalf("failed settlement retained valid snapshot: %+v", snapshot)
			}
			if databaseUnavailable {
				r.mu.Lock()
				r.usageErr = errors.New("authoritative usage unavailable")
				r.mu.Unlock()
			}
			usage, err := c.Usage(t.Context(), 1, now)
			if err == nil && usage != quotaTestUsage(110, 0) {
				t.Fatalf("Usage trusted stale counters after failed cleanup: %+v", usage)
			}
			if databaseUnavailable && err == nil {
				t.Fatal("repair with unavailable authoritative usage did not fail closed")
			}
			allowed, denial, err := c.Check(t.Context(), 1, now)
			if allowed || err == nil && (denial == nil || denial.Reason != "organization_daily_tokens") {
				t.Fatalf("unrepaired counters admitted usage: allowed=%v denial=%+v err=%v", allowed, denial, err)
			}

			// A failed repair must remain pending so a later healthy request can
			// recover without ever treating the old 90-token counters as valid.
			r.mu.Lock()
			r.usageErr = nil
			r.mu.Unlock()
			s.mu.Lock()
			s.delErr = nil
			s.mu.Unlock()
			quotaTestAssertUsage(t, c, now, quotaTestUsage(110, 0))
			if _, reads := r.reads(); reads < 2 {
				t.Fatalf("pending repair never read authoritative usage: reads=%d", reads)
			}
		})
	}
}

func TestOrganizationQuotaReenabledLimitRepairsUnlimitedUsage(t *testing.T) {
	r, s := quotaTestLimited(), newQuotaTestRedis()
	r.usage = quotaTestUsage(90, 0)
	c := NewOrganizationQuotaCounter(s, r, "unit")
	now := quotaTestNow()
	if allowed, denial, err := c.Check(t.Context(), 1, now); !allowed || denial != nil || err != nil {
		t.Fatalf("initial limited check: allowed=%v denial=%+v err=%v", allowed, denial, err)
	}
	r.mu.Lock()
	r.config = adminDomain.OrganizationRateLimit{OrganizationID: 1}
	r.mu.Unlock()
	c.Invalidate(1)
	if allowed, denial, err := c.Check(t.Context(), 1, now); !allowed || denial != nil || err != nil {
		t.Fatalf("unlimited check: allowed=%v denial=%+v err=%v", allowed, denial, err)
	}
	s.mu.Lock()
	beforeMgets, beforeLocks, beforeDeletes, beforeScripts := s.mgets, s.locks, s.deletes, len(s.calls)
	s.mu.Unlock()
	persistCalls := 0
	if err := c.Settle(t.Context(), 1, 20, 0, now, func(context.Context) (bool, error) {
		persistCalls++
		r.commit(20, 0)
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	redisUsed := beforeMgets != s.mgets || beforeLocks != s.locks || beforeDeletes != s.deletes || beforeScripts != len(s.calls)
	staleTokens := s.values[organizationQuotaKeys("unit", 1, now)[0]]
	s.mu.Unlock()
	if persistCalls != 1 || redisUsed || staleTokens != "90" {
		t.Fatalf("unlimited settlement fixture: persist calls=%d Redis used=%v old tokens=%q", persistCalls, redisUsed, staleTokens)
	}
	r.mu.Lock()
	r.config = adminDomain.OrganizationRateLimit{OrganizationID: 1, DailyQuotaTokens: quotaTestPtr(int64(100))}
	r.mu.Unlock()
	c.Invalidate(1)
	// The first admission after re-enabling the limit must include the receipt
	// committed while unlimited; do not prime the repair with a Usage call.
	allowed, denial, err := c.Check(t.Context(), 1, now)
	if allowed || err != nil || denial == nil || denial.Reason != "organization_daily_tokens" || denial.Limit != "100" {
		t.Fatalf("first re-enabled check: allowed=%v denial=%+v err=%v", allowed, denial, err)
	}
	quotaTestAssertUsage(t, c, now, quotaTestUsage(110, 0))
	if _, reads := r.reads(); reads != 2 {
		t.Fatalf("DB reads=%d want initial seed + re-enable repair", reads)
	}
}

func TestOrganizationQuotaUnlimitedCommitFencesNewlyEnabledSeed(t *testing.T) {
	r := &quotaTestRepository{
		config: adminDomain.OrganizationRateLimit{OrganizationID: 1},
		usage:  quotaTestUsage(90, 0),
	}
	s := newQuotaTestRedis()
	c := NewOrganizationQuotaCounter(s, r, "unit")
	now := quotaTestNow()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	persistCalls := 0
	settled := make(chan error, 1)
	go func() {
		settled <- c.Settle(t.Context(), 1, 20, 0, now, func(ctx context.Context) (bool, error) {
			persistCalls++
			close(entered)
			select {
			case <-release:
				r.commit(20, 0)
				return true, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		})
	}()
	quotaTestWait(t, entered)
	s.assertIdle(t)

	// Enable the quota while the unlimited settlement is still waiting to
	// commit. Admission seeds 90, which must become invalid after that commit.
	r.mu.Lock()
	r.config = adminDomain.OrganizationRateLimit{OrganizationID: 1, DailyQuotaTokens: quotaTestPtr(int64(100))}
	r.mu.Unlock()
	c.Invalidate(1)
	quotaTestAssertUsage(t, c, now, quotaTestUsage(90, 0))
	if snapshot, ok := c.recent(1, now); !ok || snapshot != quotaTestUsage(90, 0) {
		t.Fatalf("newly enabled quota did not publish its initial seed: snapshot=%+v valid=%v", snapshot, ok)
	}

	releaseOnce.Do(func() { close(release) })
	if err := quotaTestResult(t, settled); err != nil {
		t.Fatalf("unlimited settlement failed: %v", err)
	}
	if persistCalls != 1 {
		t.Fatalf("persist calls=%d want 1", persistCalls)
	}
	quotaTestAssertNoBuckets(t, s, now)
	if snapshot, ok := c.recent(1, now); ok {
		t.Fatalf("unlimited commit retained the pre-commit snapshot: %+v", snapshot)
	}
	// Check must repair on its first call, without a Usage call priming it.
	allowed, denial, err := c.Check(t.Context(), 1, now)
	if allowed || err != nil || denial == nil || denial.Reason != "organization_daily_tokens" || denial.Limit != "100" {
		t.Fatalf("first check after commit: allowed=%v denial=%+v err=%v", allowed, denial, err)
	}
	quotaTestAssertUsage(t, c, now, quotaTestUsage(110, 0))
	if _, reads := r.reads(); reads != 2 {
		t.Fatalf("DB reads=%d want initial seed + post-commit repair", reads)
	}
}
