package infrastructure

import (
	"context"
	"errors"
	"io"
	"net"
	"os/exec"
	"reflect"
	"strconv"
	"testing"
	"time"

	adminDomain "bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/gateway/domain"
	"github.com/redis/go-redis/v9"
)

// Stub the Redis transport to verify tuple decoding and the active-rule index.
type rateLimitRedisStub struct {
	redis.UniversalClient
	result []any
	err    error
	keys   []string
	args   []any
}

func (stub *rateLimitRedisStub) EvalSha(ctx context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	stub.keys, stub.args = keys, args
	cmd := redis.NewCmd(ctx)
	cmd.SetVal(stub.result)
	cmd.SetErr(stub.err)
	return cmd
}

func TestRedisRateLimiterReturnsTokenQuotaPolicyAndTTL(t *testing.T) {
	quota := int64(10)
	repository := &rateLimitTestRepository{config: adminDomain.RateLimitUpdate{Global: &adminDomain.RateLimitConfig{QPS: 3}, Organizations: []adminDomain.OrganizationRateLimit{{OrganizationID: 1, QPS: 2}}, Keys: []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: adminDomain.RateLimitConfig{QPS: 1, Quota5H: &quota, Quota1D: &quota, Quota7D: &quota}}}}}
	for index, reason := range []domain.RateLimitReason{domain.RateLimitQuota5H, domain.RateLimitQuota1D, domain.RateLimitQuota7D} {
		t.Run(string(reason), func(t *testing.T) {
			stub := &rateLimitRedisStub{result: []any{int64(0), int64(index + 1), int64(0), int64(1251)}}
			limiter := NewRedisRateLimiter(stub, repository, "test:rate")
			decision, err := limiter.Decide(t.Context(), domain.RateLimitKey{OrganizationID: 1, APIKeyID: 3}, time.Now())
			if err != nil || decision.Allowed || decision.Degraded || decision.Reason != reason || decision.Limit != 10 || decision.RetryAfter != 1251*time.Millisecond {
				t.Fatalf("decision=%+v err=%v", decision, err)
			}
			wantKeys := []string{"test:rate:quota_5h:3", "test:rate:quota_1d:3", "test:rate:quota_7d:3"}
			wantArgs := []any{int64(10), int64(18000000), int64(10), int64(86400000), int64(10), int64(604800000)}
			if !reflect.DeepEqual(stub.keys, wantKeys) || !reflect.DeepEqual(stub.args, wantArgs) {
				t.Fatalf("unexpected script input keys=%v args=%v", stub.keys, stub.args)
			}
		})
	}
}

func TestRedisRateLimiterSkipsUnlimitedRules(t *testing.T) {
	quota := int64(10)
	repository := &rateLimitTestRepository{config: adminDomain.RateLimitUpdate{
		Global: &adminDomain.RateLimitConfig{},
		Keys:   []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: adminDomain.RateLimitConfig{Quota1D: &quota}}},
	}}
	stub := &rateLimitRedisStub{result: []any{int64(0), int64(1), int64(0), int64(86400000)}}
	limiter := NewRedisRateLimiter(stub, repository, "test:rate")
	key := domain.RateLimitKey{OrganizationID: 1, APIKeyID: 3}
	decision, err := limiter.Decide(t.Context(), key, time.Now())
	if err != nil || decision.Reason != domain.RateLimitQuota1D || decision.RetryAfter != 24*time.Hour || len(stub.keys) != 1 {
		t.Fatalf("filtered rules: %+v err=%v keys=%v", decision, err, stub.keys)
	}
	stub.result = []any{int64(1), int64(1), int64(9), int64(0)}
	decision, err = limiter.Decide(t.Context(), key, time.Now())
	if err != nil || !decision.Allowed || decision.Limit != 10 || decision.Remaining != 9 || decision.Reason != "" {
		t.Fatalf("allowed request: %+v err=%v", decision, err)
	}
}

func TestRedisSessionAcquireUsesOnlyConfiguredConcurrentScopes(t *testing.T) {
	repository := &rateLimitTestRepository{config: adminDomain.RateLimitUpdate{
		Global:        &adminDomain.RateLimitConfig{},
		Organizations: []adminDomain.OrganizationRateLimit{{OrganizationID: 1, QPS: 1}},
		Keys:          []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: adminDomain.RateLimitConfig{QPS: 2}}},
	}}
	stub := &rateLimitRedisStub{result: []any{int64(0), int64(1)}}
	limiter := NewRedisRateLimiter(stub, repository, "test:session")
	decision, _, err := limiter.AcquireSession(t.Context(), domain.RateLimitKey{OrganizationID: 1, APIKeyID: 3})
	if err != nil || decision.Allowed || decision.Reason != domain.RateLimitOrganizationQPS || decision.Limit != 1 {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	wantKeys := []string{"test:session:{sessions}:organization:1", "test:session:{sessions}:key:3"}
	if !reflect.DeepEqual(stub.keys, wantKeys) || len(stub.args) != 4 || stub.args[2] != int64(1) || stub.args[3] != int64(2) {
		t.Fatalf("unexpected session script input keys=%v args=%v", stub.keys, stub.args)
	}
}

func TestRedisRateLimiterAllowsAndMarksDegradedOnFailure(t *testing.T) {
	quota := int64(10)
	repository := &rateLimitTestRepository{config: adminDomain.RateLimitUpdate{Keys: []adminDomain.KeyRateLimit{{KeyID: 3, RateLimitConfig: adminDomain.RateLimitConfig{Quota5H: &quota}}}}}
	for _, stub := range []*rateLimitRedisStub{
		{err: errors.New("redis unavailable")},
		{result: []any{int64(0), int64(99), int64(0), int64(1000)}},
	} {
		limiter := NewRedisRateLimiter(stub, repository, "test:rate")
		now := time.Now()
		decision, err := limiter.Decide(t.Context(), domain.RateLimitKey{APIKeyID: 3}, now)
		if err != nil || !decision.Allowed || !decision.Degraded {
			t.Fatalf("failure decision=%+v err=%v", decision, err)
		}
	}
}

func TestRedisSessionRenewalDoesNotResurrectExpiredOrReplacedLease(t *testing.T) {
	client := startSessionRenewRedis(t)
	ctx := t.Context()
	keys := []string{"test:{sessions}:organization:1"}
	const lease = int64(time.Hour / time.Millisecond)

	// A contender atomically removes the expired owner and takes the slot.
	if err := client.ZAdd(ctx, keys[0], redis.Z{Score: 1, Member: "request-a"}).Err(); err != nil {
		t.Fatal(err)
	}
	acquired, err := acquireSessionScript.Run(ctx, client, keys, "request-b", lease, int64(1)).Int64Slice()
	if err != nil || len(acquired) != 2 || acquired[0] != 1 {
		t.Fatalf("contender acquire=%v err=%v", acquired, err)
	}

	// A's late renewal must not reinsert it beside B.
	renewed, err := renewSessionScript.Run(ctx, client, keys, "request-a", lease).Int64()
	if err != nil || renewed != 0 {
		t.Fatalf("stale renewal=%d err=%v, want lost lease", renewed, err)
	}
	if count, err := client.ZCard(ctx, keys[0]).Result(); err != nil || count != 1 {
		t.Fatalf("lease count=%d err=%v, want only contender", count, err)
	}
	if score, err := client.ZScore(ctx, keys[0], "request-b").Result(); err != nil || score <= 1 {
		t.Fatalf("contender score=%v err=%v", score, err)
	}
}

func TestRedisSessionRenewalIsAtomicAcrossScopes(t *testing.T) {
	client := startSessionRenewRedis(t)
	ctx := t.Context()
	keys := []string{"test:{sessions}:global", "test:{sessions}:key:3"}
	const lease = int64(time.Hour / time.Millisecond)
	const currentScore = float64(4_000_000_000_000)
	if err := client.ZAdd(ctx, keys[0], redis.Z{Score: currentScore, Member: "request-a"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.ZAdd(ctx, keys[1], redis.Z{Score: currentScore, Member: "request-b"}).Err(); err != nil {
		t.Fatal(err)
	}
	renewed, err := renewSessionScript.Run(ctx, client, keys, "request-a", lease).Int64()
	if err != nil || renewed != 0 {
		t.Fatalf("partial renewal=%d err=%v, want lost lease", renewed, err)
	}
	if score, err := client.ZScore(ctx, keys[0], "request-a").Result(); err != nil || score != currentScore {
		t.Fatalf("partially updated score=%v err=%v", score, err)
	}
}

func startSessionRenewRedis(t *testing.T) *redis.Client {
	t.Helper()
	server, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	command := exec.Command(server, "--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--save", "", "--appendonly", "no")
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), MaxRetries: 0})
	t.Cleanup(func() { _ = client.Close(); _ = command.Process.Kill(); _ = command.Wait() })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := client.Ping(t.Context()).Err(); err == nil {
			return client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("local Redis did not start")
	return nil
}
