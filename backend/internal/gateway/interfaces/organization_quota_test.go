package interfaces

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"

	"bosapi/backend/internal/platform/errorcode"
	userinfra "bosapi/backend/internal/user/infrastructure"
	"testing"
	"time"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"github.com/gin-gonic/gin"
)

type organizationQuotaStub struct {
	organizationQuotaRuntime
	mu                sync.Mutex
	insertionsAtCheck []int
	allowed           bool
	denial            *gatewayinfra.OrganizationQuotaDenial
	err               error
	checked           []uint64
	settled           []time.Time
	inserted          int
}

func (s *organizationQuotaStub) Check(_ context.Context, id uint64, _ time.Time) (bool, *gatewayinfra.OrganizationQuotaDenial, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checked = append(s.checked, id)
	s.insertionsAtCheck = append(s.insertionsAtCheck, s.inserted)
	return s.allowed, s.denial, s.err
}
func (s *organizationQuotaStub) Settle(ctx context.Context, _ uint64, _ int64, _ float64, at time.Time, persist func(context.Context) (bool, error)) error {
	s.mu.Lock()
	s.settled = append(s.settled, at)
	s.mu.Unlock()
	inserted, err := persist(ctx)
	if inserted {
		s.mu.Lock()
		s.inserted++
		s.mu.Unlock()
	}
	return err
}
func TestOrganizationQuotaMiddlewareResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		allowed bool
		denial  *gatewayinfra.OrganizationQuotaDenial
		err     error
		status  int
		retry   string
	}{
		{name: "available", allowed: true, status: 204},
		{name: "zero", denial: &gatewayinfra.OrganizationQuotaDenial{Reason: "organization_daily_tokens", Limit: "0"}, status: 429},
		{name: "calendar", denial: &gatewayinfra.OrganizationQuotaDenial{Reason: "organization_monthly_amount", Limit: "1.25", RetryAfter: 1500 * time.Millisecond}, status: 429, retry: "2"},
		{name: "accounting unavailable", err: errors.New("private storage error"), status: 503},
		{name: "invalid denial", status: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &organizationQuotaStub{allowed: tc.allowed, denial: tc.denial, err: tc.err}
			a := &app{organizationQuota: stub}
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set(gatewayAPIKeyContext, apiKey{ID: 7, OrgID: 2}) }, a.gatewayQuotaMiddleware())
			called := false
			router.GET("/", func(c *gin.Context) { called = true; c.Status(204) })
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
			if response.Code != tc.status || called != tc.allowed {
				t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
			}
			if len(stub.checked) != 1 || stub.checked[0] != 2 {
				t.Fatalf("wrong tenant: %v", stub.checked)
			}
			if got := response.Header().Get("Retry-After"); got != tc.retry {
				t.Fatalf("retry=%q want=%q", got, tc.retry)
			}
			if tc.denial != nil {
				if response.Header().Get("X-RateLimit-Reason") != tc.denial.Reason || response.Header().Get("X-RateLimit-Limit") != tc.denial.Limit || response.Header().Get("X-RateLimit-Remaining") != "0" {
					t.Fatalf("headers=%v", response.Header())
				}
				if !strings.Contains(response.Body.String(), `"code":"quota_exceeded"`) {
					t.Fatal(response.Body.String())
				}
			}
			if tc.status == http.StatusServiceUnavailable && !strings.Contains(response.Body.String(), `"code":"quota_accounting_unavailable"`) {
				t.Fatal(response.Body.String())
			}
			if strings.Contains(response.Body.String(), "private storage error") {
				t.Fatal("internal error leaked")
			}
		})
	}
}
func TestOrganizationSettlementPreservesReceiptTimeAndDuplicateResult(t *testing.T) {
	a, repo, _ := newQuotaTestApp(t, true, nil, nil)
	stub := &organizationQuotaStub{allowed: true}
	a.organizationQuota = stub
	at := time.Date(2026, 10, 31, 15, 59, 59, 0, time.UTC)
	event := usageEvent{Context: t.Context(), Persistent: true, CreatedAt: at, RequestID: "request", UsageID: "receipt", Key: apiKey{ID: 7, OrgID: 2}, Metrics: usageMetrics{TotalTokens: 17, Cost: 0.00000001}}
	for range 2 {
		if err := a.settleGatewayQuota(event); err != nil {
			t.Fatal(err)
		}
	}
	if len(repo.records) != 1 || stub.inserted != 1 || len(stub.settled) != 2 {
		t.Fatalf("records=%d inserted=%d settled=%d", len(repo.records), stub.inserted, len(stub.settled))
	}
	for _, got := range stub.settled {
		if !got.Equal(at) {
			t.Fatalf("retry changed billing period: %v", got)
		}
	}
	if !repo.records[0].CreatedAt.Equal(at) {
		t.Fatal("ledger time differs from counter time")
	}
}
func TestOrganizationQuotaRecheckedAfterKeyAdmission(t *testing.T) {
	a, repo, secret := newQuotaTestApp(t, true, int64Ptr(100), nil)
	stub := &organizationQuotaStub{denial: &gatewayinfra.OrganizationQuotaDenial{Reason: "organization_daily_tokens", Limit: "10"}}
	a.organizationQuota = stub
	repo.key.OrganizationID = 9
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = quotaTestRequest(t.Context(), secret, "recheck", false)
	if a.prepareQuotaRequest(c, &quotaAdmission{}) {
		t.Fatal("exhausted organization admitted")
	}
	if recorder.Code != 429 || len(stub.checked) != 1 || stub.checked[0] != 9 {
		t.Fatalf("status=%d scope=%v", recorder.Code, stub.checked)
	}
}

// organizationReceiptRepository models idempotent ledger commits, including a
// lost commit response. It never connects to a database or a Redis server.
type organizationReceiptRepository struct {
	gatewayKeyRepository
	mu             sync.Mutex
	attempts       []userinfra.GatewayUsageRecord
	records        map[string]userinfra.GatewayUsageRecord
	failure        error
	commitThenFail bool
	beforePersist  func(context.Context, userinfra.GatewayUsageRecord) error
}

func (r *organizationReceiptRepository) RecordGatewayUsageAndQuota(ctx context.Context, record userinfra.GatewayUsageRecord) (bool, error) {
	if r.beforePersist != nil {
		if err := r.beforePersist(ctx, record); err != nil {
			return false, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts = append(r.attempts, record)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if r.failure != nil && !r.commitThenFail {
		return false, r.failure
	}
	_, duplicate := r.records[record.UsageID]
	if !duplicate {
		r.records[record.UsageID] = record
	}
	if r.failure != nil {
		return false, r.failure
	}
	return !duplicate, nil
}

func recordOrganizationReceipt(t *testing.T, a *app, keyID, orgID int, admissions ...*quotaAdmission) usageEvent {
	t.Helper()
	key := apiKey{ID: keyID, OrgID: orgID}
	if len(admissions) > 0 {
		key.QuotaTokens = int64Ptr(17)
	}
	ctx := context.WithValue(t.Context(), gatewayAPIKeyRequestKey{}, key)
	if len(admissions) > 0 {
		ctx = context.WithValue(ctx, gatewayQuotaAdmissionRequestKey{}, admissions[0])
	}
	ctx = context.WithValue(ctx, gatewayPersistentKeyRequestKey{}, true)
	request := httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	request = withUsageMetrics(request, usageMetrics{TotalTokens: 17, Cost: 0.25, Known: true, CostKnown: true})
	a.recordRequest(request, provider{}, http.StatusOK, time.Second, errorcode.Code(""))
	select {
	case event := <-a.usageQueue:
		return event
	default:
		t.Fatal("missing queued usage event")
		return usageEvent{}
	}
}

func organizationQuotaCheck(a *app, ctx context.Context, keyID, orgID int) (bool, *httptest.ResponseRecorder) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	return a.checkOrganizationQuota(c, apiKey{ID: keyID, OrgID: orgID}), response
}

func TestOrganizationOnlyFailedReceiptsRecoverAcrossKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed commit", true: "lost commit response"}[ambiguous], func(t *testing.T) {
			repo := &organizationReceiptRepository{records: make(map[string]userinfra.GatewayUsageRecord), failure: errors.New("private ledger failure"), commitThenFail: ambiguous}
			runtime := &organizationQuotaStub{allowed: true}
			a := &app{keyRepository: repo, organizationQuota: runtime, usageQueue: make(chan usageEvent, 4)}
			// Both requests were already in flight when persistence became unavailable.
			first := recordOrganizationReceipt(t, a, 7, 2)
			second := recordOrganizationReceipt(t, a, 8, 2)
			if first.UsageID == second.UsageID || first.UsageID == "" || first.CreatedAt.IsZero() {
				t.Fatal("receipt identity/time missing")
			}
			if len(a.quotaAdmissions) != 0 || len(a.organizationPending[2].events) != 2 {
				t.Fatalf("key admissions=%d pending=%+v", len(a.quotaAdmissions), a.organizationPending)
			}
			// A different tenant is unaffected, while every key of this tenant sees the
			// same unpersisted receipts and must stop before organization Check.
			if allowed, _ := organizationQuotaCheck(a, t.Context(), 9, 3); !allowed {
				t.Fatal("unrelated organization blocked")
			}
			for _, keyID := range []int{7, 8, 9} {
				allowed, response := organizationQuotaCheck(a, t.Context(), keyID, 2)
				if allowed || response.Code != 503 || !strings.Contains(response.Body.String(), `"code":"quota_accounting_unavailable"`) {
					t.Fatalf("key=%d allowed=%v status=%d body=%s", keyID, allowed, response.Code, response.Body)
				}
			}
			if len(runtime.checked) != 1 {
				t.Fatalf("checked balance despite unresolved receipts: %v", runtime.checked)
			}
			repo.failure = nil
			if allowed, response := organizationQuotaCheck(a, t.Context(), 9, 2); !allowed {
				t.Fatalf("recovered repository denied admission: %s", response.Body)
			}
			if len(a.organizationPending) != 0 || len(repo.records) != 2 {
				t.Fatalf("pending=%v records=%v", a.organizationPending, repo.records)
			}
			wantInserted := 2
			if ambiguous {
				wantInserted = 0
			}
			if runtime.inserted != wantInserted || runtime.insertionsAtCheck[len(runtime.insertionsAtCheck)-1] != wantInserted {
				t.Fatalf("counter insertions=%d at check=%v want=%d", runtime.inserted, runtime.insertionsAtCheck, wantInserted)
			}
			receipts := map[string]usageEvent{first.UsageID: first, second.UsageID: second}
			for _, attempt := range repo.attempts {
				original, ok := receipts[attempt.UsageID]
				if !ok || !attempt.CreatedAt.Equal(original.CreatedAt) || attempt.APIKeyID != uint64(original.Key.ID) || attempt.TotalTokens != 17 || attempt.CostAmount != 0.25 {
					t.Fatalf("retry changed receipt: %+v", attempt)
				}
			}
			var tokens uint64
			var amount float64
			for _, record := range repo.records {
				tokens += record.TotalTokens
				amount += record.CostAmount
			}
			if tokens != 34 || amount != 0.5 {
				t.Fatalf("receipts not charged exactly once: tokens=%d amount=%f", tokens, amount)
			}
			attempts := len(repo.attempts)
			for _, keyID := range []int{7, 8, 9} {
				if allowed, _ := organizationQuotaCheck(a, t.Context(), keyID, 2); !allowed {
					t.Fatal("recovered tenant blocked")
				}
			}
			if len(repo.attempts) != attempts {
				t.Fatal("completed receipts retried")
			}
		})
	}
}

func TestOrganizationReceiptRetryKeepsConcurrentFailures(t *testing.T) {
	runtime := &organizationQuotaStub{allowed: true}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	repo := &organizationReceiptRepository{records: make(map[string]userinfra.GatewayUsageRecord), beforePersist: func(ctx context.Context, record userinfra.GatewayUsageRecord) error {
		if record.UsageID == "first" {
			once.Do(func() { close(started) })
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}}
	a := &app{keyRepository: repo, organizationQuota: runtime}
	at := time.Date(2026, 10, 31, 15, 59, 59, 0, time.UTC)
	first := usageEvent{Context: t.Context(), Persistent: true, UsageID: "first", CreatedAt: at, Key: apiKey{ID: 7, OrgID: 2}, Metrics: usageMetrics{TotalTokens: 17}}
	a.retainOrganizationReceipt(first)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { _, err := a.retryOrganizationReceipts(ctx, 2); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("retry did not start")
	}
	// Appending another failed receipt must not wait for the in-flight DB retry.
	second := first
	second.UsageID = "second"
	second.Key.ID = 8
	a.retainOrganizationReceipt(second)
	if allowed, _ := organizationQuotaCheck(a, ctx, 9, 3); !allowed {
		t.Fatal("other tenant blocked by retry")
	}
	// Another key in the same organization waits, but can cancel independently.
	waiting := make(chan struct{})
	waiterContext, waiterCancel := context.WithCancel(ctx)
	waiter := &quotaWaitContext{Context: waiterContext, waiting: waiting}
	waiterDone := make(chan error, 1)
	go func() { _, err := a.retryOrganizationReceipts(waiter, 2); waiterDone <- err }()
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal("second retry did not wait")
	}
	waiterCancel()
	if err := <-waiterDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error=%v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(repo.records) != 2 || len(repo.attempts) != 2 || runtime.inserted != 2 || len(a.organizationPending) != 0 {
		t.Fatalf("records=%d attempts=%d inserted=%d pending=%v", len(repo.records), len(repo.attempts), runtime.inserted, a.organizationPending)
	}
	for _, record := range repo.records {
		if !record.CreatedAt.Equal(at) {
			t.Fatal("retry changed the original calendar period")
		}
	}
}

func TestOrganizationQuotaDoesNotSerializeUpstreamRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &app{organizationQuota: &organizationQuotaStub{allowed: true}}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(gatewayAPIKeyContext, apiKey{ID: 7, OrgID: 2}) }, a.gatewayQuotaMiddleware())
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var calls atomic.Int64
	router.GET("/", func(c *gin.Context) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		c.Status(204)
	})
	done := make(chan int, 2)
	for range 2 {
		go func() {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("GET", "/", nil).WithContext(ctx))
			done <- response.Code
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("organization lock serialized upstream requests")
		}
	}
	close(release)
	for range 2 {
		if status := <-done; status != 204 {
			t.Fatalf("status=%d", status)
		}
	}
	if calls.Load() != 2 || len(a.quotaAdmissions) != 0 || len(a.organizationPending) != 0 {
		t.Fatalf("calls=%d key admissions=%v pending=%v", calls.Load(), a.quotaAdmissions, a.organizationPending)
	}
}

func TestOrganizationReceiptRetryRefreshesKeyQuotaBalance(t *testing.T) {
	for _, withAdmission := range []bool{true, false} {
		name := "quota added after failed receipt"
		if withAdmission {
			name = "sibling retries key admission receipt"
		}
		t.Run(name, func(t *testing.T) {
			a, repo, secret := newQuotaTestApp(t, true, nil, nil)
			a.usageQueue = make(chan usageEvent, 1)
			runtime := &organizationQuotaStub{allowed: true}
			a.organizationQuota = runtime
			repo.chargeErr = errors.New("ledger unavailable")
			admission := &quotaAdmission{}
			var event usageEvent
			if withAdmission {
				repo.key.QuotaTokens = int64Ptr(17)
				event = recordOrganizationReceipt(t, a, 7, 2, admission)
				if admission.pending == nil || admission.pending.UsageID != event.UsageID {
					t.Fatal("key admission lost its failed receipt")
				}
				if allowed, response := organizationQuotaCheck(a, t.Context(), 8, 2); allowed || response.Code != 503 {
					t.Fatalf("sibling ignored failed key receipt: status=%d", response.Code)
				}
			} else {
				event = recordOrganizationReceipt(t, a, 7, 2)
				// The failed request had no key admission. Its key acquires a quota before
				// its next request, so the first authenticated balance is still stale.
				repo.key.QuotaTokens = int64Ptr(17)
			}
			repo.chargeErr = nil
			if withAdmission {
				if allowed, response := organizationQuotaCheck(a, t.Context(), 8, 2); !allowed {
					t.Fatalf("sibling could not recover receipt: %s", response.Body)
				}
			}
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = quotaTestRequest(t.Context(), secret, "refresh after retry", false)
			if a.prepareQuotaRequest(c, admission) || response.Code != 429 || response.Header().Get("X-RateLimit-Reason") != "total_tokens" {
				t.Fatalf("stale key balance admitted: status=%d body=%s", response.Code, response.Body)
			}
			if len(repo.records) != 1 || repo.key.QuotaUsedTokens != 17 || runtime.inserted != 1 || admission.pending != nil || len(a.organizationPending) != 0 {
				t.Fatalf("records=%d tokens=%d inserts=%d key pending=%v organization pending=%v", len(repo.records), repo.key.QuotaUsedTokens, runtime.inserted, admission.pending, a.organizationPending)
			}
			if repo.records[0].UsageID != event.UsageID || !repo.records[0].CreatedAt.Equal(event.CreatedAt) {
				t.Fatal("sibling retry changed receipt identity or time")
			}
		})
	}
}
