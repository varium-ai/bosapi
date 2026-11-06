package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	userdomain "bosapi/backend/internal/user/domain"
	userinfra "bosapi/backend/internal/user/infrastructure"
)

type quotaRequestMarker struct{}

type quotaWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

type quotaCancelReader struct {
	reader io.Reader
	cancel context.CancelFunc
	once   sync.Once
}

// Read cancels the client after upstream usage has arrived to exercise detached billing.
func (reader *quotaCancelReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	if count > 0 {
		reader.once.Do(reader.cancel)
	}
	return count, err
}

// Done signals that admission is observing cancellation before returning the parent channel.
func (ctx *quotaWaitContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

type quotaKeyRepository struct {
	mu         sync.Mutex
	secret     string
	key        userdomain.APIKey
	records    []userinfra.GatewayUsageRecord
	lookup     chan string
	usageIDs   map[string]bool
	chargeErr  error
	attemptIDs []string
}

// FindGatewayKey returns a snapshot so concurrent admission must refresh settled usage.
func (repository *quotaKeyRepository) FindGatewayKey(ctx context.Context, secret string, _ time.Time) (userdomain.APIKey, bool, error) {
	repository.mu.Lock()
	key := repository.key
	found := secret == repository.secret && key.Status == "active"
	repository.mu.Unlock()
	if marker, ok := ctx.Value(quotaRequestMarker{}).(string); ok && repository.lookup != nil {
		select {
		case repository.lookup <- marker:
		default:
		}
	}
	return key, found, nil
}

// RecordGatewayUsageAndQuota preserves usage identity across deterministic failure retries.
func (repository *quotaKeyRepository) RecordGatewayUsageAndQuota(ctx context.Context, record userinfra.GatewayUsageRecord) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.attemptIDs = append(repository.attemptIDs, record.UsageID)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if repository.chargeErr != nil {
		return false, repository.chargeErr
	}
	if repository.usageIDs[record.UsageID] {
		return false, nil
	}
	repository.usageIDs[record.UsageID] = true
	repository.records = append(repository.records, record)
	repository.key.QuotaUsedTokens += int64(record.TotalTokens)
	repository.key.QuotaUsedAmount += record.CostAmount
	// Test records belong to the current day, week, and month.
	repository.key.DailyUsedTokens += int64(record.TotalTokens)
	repository.key.WeeklyUsedTokens += int64(record.TotalTokens)
	repository.key.MonthlyUsedTokens += int64(record.TotalTokens)
	repository.key.DailyUsedAmount += record.CostAmount
	repository.key.WeeklyUsedAmount += record.CostAmount
	repository.key.MonthlyUsedAmount += record.CostAmount
	return true, nil
}

type periodicQuotaTestCase struct {
	name      string
	amount    bool
	configure func(*userdomain.APIKey, float64, float64)
}

func periodicQuotaTestCases() []periodicQuotaTestCase {
	return []periodicQuotaTestCase{
		{name: "daily_tokens", configure: func(key *userdomain.APIKey, limit, used float64) {
			key.DailyQuotaTokens, key.DailyUsedTokens = int64Ptr(int64(limit)), int64(used)
		}},
		{name: "weekly_tokens", configure: func(key *userdomain.APIKey, limit, used float64) {
			key.WeeklyQuotaTokens, key.WeeklyUsedTokens = int64Ptr(int64(limit)), int64(used)
		}},
		{name: "monthly_tokens", configure: func(key *userdomain.APIKey, limit, used float64) {
			key.MonthlyQuotaTokens, key.MonthlyUsedTokens = int64Ptr(int64(limit)), int64(used)
		}},
		{name: "daily_amount", amount: true, configure: func(key *userdomain.APIKey, limit, used float64) {
			key.DailyQuotaAmount, key.DailyUsedAmount = float64Ptr(limit), used
		}},
		{name: "weekly_amount", amount: true, configure: func(key *userdomain.APIKey, limit, used float64) {
			key.WeeklyQuotaAmount, key.WeeklyUsedAmount = float64Ptr(limit), used
		}},
		{name: "monthly_amount", amount: true, configure: func(key *userdomain.APIKey, limit, used float64) {
			key.MonthlyQuotaAmount, key.MonthlyUsedAmount = float64Ptr(limit), used
		}},
	}
}

func TestGatewayPeriodicQuotaEnforcement(t *testing.T) {
	for _, period := range periodicQuotaTestCases() {
		for _, withTotal := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				for _, state := range []string{"exhausted", "available", "zero"} {
					name := period.name + "/periodic_only/" + state + "/json"
					if withTotal {
						name = strings.Replace(name, "periodic_only", "with_total", 1)
					}
					if stream {
						name = strings.TrimSuffix(name, "json") + "stream"
					}
					t.Run(name, func(t *testing.T) {
						a, repository, secret := newQuotaTestApp(t, true, nil, nil)
						limit, used := float64(277), float64(0)
						if period.amount {
							limit = 0.000277
						}
						if state == "exhausted" {
							used = limit
						} else if state == "zero" {
							limit = 0
						}
						period.configure(&repository.key, limit, used)
						if withTotal {
							repository.key.QuotaTokens, repository.key.QuotaAmount = int64Ptr(100000), float64Ptr(100)
						}
						var calls atomic.Int64
						a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
							calls.Add(1)
							return quotaUpstreamResponse(stream), nil
						})}
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						handler := a.routes()
						for index := 0; index < 2; index++ {
							want := http.StatusTooManyRequests
							if state == "available" && index == 0 {
								want = http.StatusOK
							}
							response := httptest.NewRecorder()
							handler.ServeHTTP(response, quotaTestRequest(ctx, secret, state, stream))
							assertQuotaResponse(t, response, want)
						}
						wantCalls := int64(0)
						if state == "available" {
							wantCalls = 1
						}
						if calls.Load() != wantCalls || len(repository.records) != 2 {
							t.Fatalf("upstream calls=%d records=%d, want calls=%d and both requests recorded", calls.Load(), len(repository.records), wantCalls)
						}
					})
				}
			}
		}
	}
}

func newQuotaTestApp(t *testing.T, database bool, capTokens *int64, capAmount *float64) (*app, *quotaKeyRepository, string) {
	t.Helper()
	a := testApp(t)
	secret := "sk-gw-quota-regression-secret"
	a.data.Providers = []provider{{
		ID: 1, Type: "api_key", Status: "active", BaseURL: "https://api.openai.com/v1", APIKey: "upstream-secret",
		Pricing: map[string]any{"input": float64(1), "output": float64(1)},
	}}
	key := apiKey{ID: 7, OrgID: 2, Hash: digest(secret), Status: "active", ProviderID: 1, QuotaTokens: capTokens, QuotaAmount: capAmount}
	if !database {
		a.data.Keys = []apiKey{key}
		return a, nil, secret
	}
	repository := &quotaKeyRepository{
		secret: secret, usageIDs: make(map[string]bool), lookup: make(chan string, 32),
		key: userdomain.APIKey{ID: 7, OrganizationID: 2, Status: "active", QuotaTokens: capTokens, QuotaAmount: capAmount},
	}
	a.keyRepository = repository
	return a, repository, secret
}

func quotaUpstreamResponse(stream bool) *http.Response {
	contentType := "application/json"
	payload := `{"id":"quota-response","usage":{"prompt_tokens":200,"completion_tokens":77,"total_tokens":277}}`
	if stream {
		contentType = "text/event-stream"
		payload = "data: " + payload + "\n\ndata: [DONE]\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(payload))}
}

func quotaTestRequest(ctx context.Context, secret, marker string, stream bool) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"quota-model","messages":[{"role":"user","content":"hello"}]}`))
	r.Header.Set("Authorization", "Bearer "+secret)
	if stream {
		r.Header.Set("Accept", "text/event-stream")
	}
	return r.WithContext(context.WithValue(ctx, quotaRequestMarker{}, marker))
}

func assertQuotaResponse(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status=%d, want=%d, body=%s", response.Code, want, response.Body.String())
	}
	if want != http.StatusTooManyRequests {
		return
	}
	var envelope struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != "quota_exceeded" {
		t.Fatalf("missing quota error: body=%s err=%v", response.Body.String(), err)
	}
	if response.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("missing exhausted quota header: %v", response.Header())
	}
}

func quotaRecordedUsage(a *app, repository *quotaKeyRepository) (int64, float64, int) {
	if repository != nil {
		repository.mu.Lock()
		defer repository.mu.Unlock()
		return repository.key.QuotaUsedTokens, repository.key.QuotaUsedAmount, len(repository.records)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.data.Keys[0].QuotaUsedTokens, a.data.Keys[0].QuotaUsedAmount, 0
}

func TestGatewayQuotaSettlesBeforeNextRequest(t *testing.T) {
	for _, database := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, amount := range []bool{false, true} {
				for _, fullQueue := range []bool{false, true} {
					name := quotaTestName(database, stream, amount, fullQueue)
					t.Run(name, func(t *testing.T) {
						testQuotaSettlement(t, database, stream, amount, fullQueue)
					})
				}
			}
		}
	}
}

func testQuotaSettlement(t *testing.T, database, stream, amount, fullQueue bool) {
	t.Helper()
	var tokens *int64
	var cost *float64
	if amount {
		cost = float64Ptr(0.00001)
	} else {
		tokens = int64Ptr(10)
	}
	a, repository, secret := newQuotaTestApp(t, database, tokens, cost)
	if fullQueue {
		a.usageQueue = make(chan usageEvent, 1)
		a.usageQueue <- usageEvent{}
	}
	var calls atomic.Int64
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return quotaUpstreamResponse(stream), nil
	})}
	handler := a.routes()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for index, status := range []int{http.StatusOK, http.StatusTooManyRequests, http.StatusTooManyRequests} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, quotaTestRequest(ctx, secret, "sequential", stream))
		assertQuotaResponse(t, response, status)
		usedTokens, usedCost, records := quotaRecordedUsage(a, repository)
		if usedTokens != 277 || math.Abs(usedCost-0.000277) > 1e-10 || database && records != index+1 {
			t.Fatalf("request=%d tokens=%d cost=%g records=%d", index, usedTokens, usedCost, records)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls=%d, want=1", calls.Load())
	}
}

func quotaTestName(database, stream, amount, fullQueue bool) string {
	parts := []string{"local", "json", "tokens", "absent_queue"}
	if database {
		parts[0] = "database"
	}
	if stream {
		parts[1] = "stream"
	}
	if amount {
		parts[2] = "amount"
	}
	if fullQueue {
		parts[3] = "full_queue"
	}
	return strings.Join(parts, "/")
}

func TestGatewayQuotaZeroAndUnlimited(t *testing.T) {
	for _, database := range []bool{false, true} {
		for _, test := range []struct {
			name   string
			tokens *int64
			amount *float64
			status int
		}{
			{name: "zero_tokens", tokens: int64Ptr(0), status: http.StatusTooManyRequests},
			{name: "zero_amount", amount: float64Ptr(0), status: http.StatusTooManyRequests},
			{name: "unlimited", status: http.StatusOK},
		} {
			t.Run(quotaTestName(database, false, false, false)+"/"+test.name, func(t *testing.T) {
				a, repository, secret := newQuotaTestApp(t, database, test.tokens, test.amount)
				var calls atomic.Int64
				a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls.Add(1)
					return quotaUpstreamResponse(false), nil
				})}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				handler := a.routes()
				for index := 0; index < 3; index++ {
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, quotaTestRequest(ctx, secret, test.name, false))
					assertQuotaResponse(t, response, test.status)
				}
				wantTokens, wantCalls := int64(0), int64(0)
				if test.status == http.StatusOK {
					wantTokens, wantCalls = 831, 3
				}
				usedTokens, _, _ := quotaRecordedUsage(a, repository)
				if usedTokens != wantTokens || calls.Load() != wantCalls {
					t.Fatalf("tokens=%d calls=%d, want tokens=%d calls=%d", usedTokens, calls.Load(), wantTokens, wantCalls)
				}
			})
		}
	}
}

func TestGatewayQuotaAllowsExplicitOutputLimitInLooseMode(t *testing.T) {
	capTokens := int64(10)
	a, _, secret := newQuotaTestApp(t, false, &capTokens, nil)
	var calls atomic.Int64
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return quotaUpstreamResponse(false), nil
	})}

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"quota-model","max_tokens":20,"messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, request)

	assertQuotaResponse(t, response, http.StatusOK)
	if calls.Load() != 1 {
		t.Fatalf("upstream calls=%d, want=1", calls.Load())
	}
}

func TestGatewayQuotaChargesAuthenticatedSnapshot(t *testing.T) {
	for _, database := range []bool{false, true} {
		t.Run(quotaTestName(database, false, false, false), func(t *testing.T) {
			a, repository, secret := newQuotaTestApp(t, database, int64Ptr(10), nil)
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if repository != nil {
					repository.mu.Lock()
					repository.key.Status = "disabled"
					repository.mu.Unlock()
				} else {
					a.mu.Lock()
					a.data.Keys[0].Status = "disabled"
					a.mu.Unlock()
				}
				return quotaUpstreamResponse(false), nil
			})}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			handler := a.routes()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, quotaTestRequest(ctx, secret, "disable_during_request", false))
			assertQuotaResponse(t, response, http.StatusOK)
			usedTokens, _, records := quotaRecordedUsage(a, repository)
			if usedTokens != 277 || database && records != 1 {
				t.Fatalf("authenticated usage was lost: tokens=%d records=%d", usedTokens, records)
			}
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, quotaTestRequest(ctx, secret, "disabled", false))
			assertQuotaResponse(t, response, http.StatusUnauthorized)
		})
	}
}

func TestGatewayQuotaConcurrentRequestRechecksAfterSettlement(t *testing.T) {
	a, repository, secret := newQuotaTestApp(t, true, int64Ptr(10), nil)
	testQuotaConcurrentRecheck(t, a, repository, secret, false)
}

func TestGatewayPeriodicQuotaConcurrentRequestRechecksAfterSettlement(t *testing.T) {
	for _, period := range periodicQuotaTestCases() {
		t.Run(period.name, func(t *testing.T) {
			a, repository, secret := newQuotaTestApp(t, true, nil, nil)
			limit := float64(277)
			if period.amount {
				limit = 0.000277
			}
			period.configure(&repository.key, limit, 0)
			testQuotaConcurrentRecheck(t, a, repository, secret, true)
		})
	}
}

func testQuotaConcurrentRecheck(t *testing.T, a *app, repository *quotaKeyRepository, secret string, stream bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	defer release()
	var calls atomic.Int64
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return quotaUpstreamResponse(stream), nil
	})}
	handler := a.routes()
	first := serveQuotaRequest(handler, quotaTestRequest(ctx, secret, "first", stream))
	select {
	case <-firstStarted:
	case <-ctx.Done():
		t.Fatal("first request did not reach upstream")
	}
	second := serveQuotaRequest(handler, quotaTestRequest(ctx, secret, "second", stream))
	for {
		select {
		case marker := <-repository.lookup:
			if marker == "second" {
				release()
				assertQuotaResponse(t, waitQuotaResponse(t, ctx, first), http.StatusOK)
				assertQuotaResponse(t, waitQuotaResponse(t, ctx, second), http.StatusTooManyRequests)
				if calls.Load() != 1 {
					t.Fatalf("concurrent request bypassed quota: upstream calls=%d", calls.Load())
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("second request did not authenticate concurrently")
		}
	}
}

func serveQuotaRequest(handler http.Handler, request *http.Request) <-chan *httptest.ResponseRecorder {
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		result <- response
	}()
	return result
}

func waitQuotaResponse(t *testing.T, ctx context.Context, result <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case response := <-result:
		return response
	case <-ctx.Done():
		t.Fatal("quota request did not complete")
		return nil
	}
}

func TestGatewayQuotaAccountingFailurePreservesPendingCharge(t *testing.T) {
	a, repository, secret := newQuotaTestApp(t, true, int64Ptr(10), nil)
	testQuotaAccountingFailure(t, a, repository, secret)
}

func TestGatewayPeriodicQuotaAccountingFailurePreservesPendingCharge(t *testing.T) {
	for _, period := range periodicQuotaTestCases() {
		t.Run(period.name, func(t *testing.T) {
			a, repository, secret := newQuotaTestApp(t, true, nil, nil)
			limit := float64(277)
			if period.amount {
				limit = 0.000277
			}
			period.configure(&repository.key, limit, 0)
			testQuotaAccountingFailure(t, a, repository, secret)
		})
	}
}

func testQuotaAccountingFailure(t *testing.T, a *app, repository *quotaKeyRepository, secret string) {
	t.Helper()
	repository.chargeErr = errors.New("database unavailable")
	var calls atomic.Int64
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return quotaUpstreamResponse(false), nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	handler := a.routes()
	for index, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, quotaTestRequest(ctx, secret, "accounting_failure", false))
		assertQuotaResponse(t, response, status)
		if index == 1 && !strings.Contains(response.Body.String(), "quota_accounting_unavailable") {
			t.Fatalf("accounting error missing: %s", response.Body.String())
		}
	}
	repository.mu.Lock()
	repository.chargeErr = nil
	repository.mu.Unlock()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, quotaTestRequest(ctx, secret, "accounting_recovered", false))
	assertQuotaResponse(t, response, http.StatusTooManyRequests)
	usedTokens, _, records := quotaRecordedUsage(a, repository)
	if usedTokens != 277 || records != 2 || calls.Load() != 1 {
		t.Fatalf("recovery lost or duplicated charge: tokens=%d records=%d calls=%d", usedTokens, records, calls.Load())
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	counts := make(map[string]int)
	for _, id := range repository.attemptIDs {
		counts[id]++
	}
	maxAttempts := 0
	for _, count := range counts {
		if count > maxAttempts {
			maxAttempts = count
		}
	}
	if maxAttempts < 2 {
		t.Fatalf("settlement retry did not preserve a usage identity: %v", repository.attemptIDs)
	}
}

func TestGatewayQuotaAdmissionHonorsCancellation(t *testing.T) {
	a := testApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, releaseFirst, err := a.acquireQuotaAdmission(ctx, "limited-key")
	if err != nil {
		t.Fatal(err)
	}
	waiterContext, cancelWaiter := context.WithCancel(ctx)
	defer cancelWaiter()
	waiter := &quotaWaitContext{Context: waiterContext, waiting: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		_, release, err := a.acquireQuotaAdmission(waiter, "limited-key")
		if err == nil {
			release()
		}
		result <- err
	}()
	select {
	case <-waiter.waiting:
		cancelWaiter()
	case <-ctx.Done():
		releaseFirst()
		t.Fatal("admission did not observe waiting context")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			releaseFirst()
			t.Fatalf("canceled waiter error=%v", err)
		}
	case <-ctx.Done():
		releaseFirst()
		t.Fatal("admission ignored cancellation")
	}
	releaseFirst()
	_, releaseNext, err := a.acquireQuotaAdmission(ctx, "limited-key")
	if err != nil {
		t.Fatalf("canceled waiter blocked later admission: %v", err)
	}
	releaseNext()
	a.quotaMu.Lock()
	defer a.quotaMu.Unlock()
	if len(a.quotaAdmissions) != 0 {
		t.Fatalf("completed admissions retained=%d", len(a.quotaAdmissions))
	}
}

func TestGatewayQuotaAsyncStatisticsDoNotChargeTwice(t *testing.T) {
	for _, database := range []bool{false, true} {
		t.Run(quotaTestName(database, false, false, false), func(t *testing.T) {
			a, repository, secret := newQuotaTestApp(t, database, int64Ptr(10), nil)
			a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return quotaUpstreamResponse(false), nil
			})}
			a.startUsageQueue()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			response := httptest.NewRecorder()
			a.routes().ServeHTTP(response, quotaTestRequest(ctx, secret, "statistics", false))
			a.stopUsageQueue()
			assertQuotaResponse(t, response, http.StatusOK)
			usedTokens, _, records := quotaRecordedUsage(a, repository)
			if usedTokens != 277 || database && records != 1 {
				t.Fatalf("statistics charged quota again: tokens=%d records=%d", usedTokens, records)
			}
			if !database {
				reloaded := testApp(t)
				reloaded.path = a.path
				if err := reloaded.load(); err != nil {
					t.Fatal(err)
				}
				if len(reloaded.data.Keys) != 1 || reloaded.data.Keys[0].QuotaUsedTokens != 277 || !quotaExceeded(reloaded.data.Keys[0]) {
					t.Fatalf("quota not retained across reload: %+v", reloaded.data.Keys)
				}
			}
		})
	}
}

func TestGatewayQuotaLocalKeyDoesNotChargeUnrelatedDatabaseKey(t *testing.T) {
	a, _, secret := newQuotaTestApp(t, false, int64Ptr(10), nil)
	repository := &quotaKeyRepository{
		secret: "different-database-secret", usageIDs: make(map[string]bool),
		key: userdomain.APIKey{ID: 7, OrganizationID: 2, Status: "active", QuotaTokens: int64Ptr(10)},
	}
	a.keyRepository = repository
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return quotaUpstreamResponse(false), nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, quotaTestRequest(ctx, secret, "local_database_id_collision", false))
	assertQuotaResponse(t, response, http.StatusOK)
	localTokens, _, _ := quotaRecordedUsage(a, nil)
	databaseTokens, _, records := quotaRecordedUsage(a, repository)
	if localTokens != 277 || databaseTokens != 0 || records != 0 {
		t.Fatalf("wrong quota source charged: local=%d database=%d records=%d", localTokens, databaseTokens, records)
	}
}

func TestGatewayQuotaPendingChargeSurvivesDatabaseSecretRotation(t *testing.T) {
	a, repository, secret := newQuotaTestApp(t, true, int64Ptr(10), nil)
	repository.chargeErr = errors.New("database unavailable")
	var calls atomic.Int64
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return quotaUpstreamResponse(false), nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	handler := a.routes()
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, quotaTestRequest(ctx, secret, "before_rotation", false))
		assertQuotaResponse(t, response, status)
	}
	repository.mu.Lock()
	repository.secret = "sk-gw-rotated-quota-secret"
	repository.chargeErr = nil
	rotatedSecret := repository.secret
	repository.mu.Unlock()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, quotaTestRequest(ctx, rotatedSecret, "after_rotation", false))
	assertQuotaResponse(t, response, http.StatusTooManyRequests)
	usedTokens, _, records := quotaRecordedUsage(a, repository)
	if usedTokens != 277 || records != 2 || calls.Load() != 1 {
		t.Fatalf("rotation bypassed pending settlement: tokens=%d records=%d calls=%d", usedTokens, records, calls.Load())
	}
}

func TestGatewayQuotaChargesAfterClientCancellation(t *testing.T) {
	a, repository, secret := newQuotaTestApp(t, true, int64Ptr(10), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requestContext, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		response := quotaUpstreamResponse(false)
		response.Body = io.NopCloser(&quotaCancelReader{reader: response.Body, cancel: cancelRequest})
		return response, nil
	})}
	handler := a.routes()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, quotaTestRequest(requestContext, secret, "canceled_client", false))
	if !errors.Is(requestContext.Err(), context.Canceled) {
		t.Fatal("client was not canceled during response delivery")
	}
	usedTokens, _, records := quotaRecordedUsage(a, repository)
	if usedTokens != 277 || records != 1 {
		t.Fatalf("client cancellation prevented billing: tokens=%d records=%d", usedTokens, records)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, quotaTestRequest(ctx, secret, "after_cancellation", false))
	assertQuotaResponse(t, response, http.StatusTooManyRequests)
}

func TestGatewayQuotaLocalSaveFailureRecoversWithoutDoubleCharge(t *testing.T) {
	a, _, secret := newQuotaTestApp(t, false, int64Ptr(10), nil)
	validPath := a.path
	a.path = "/dev/null/data.enc"
	var calls atomic.Int64
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return quotaUpstreamResponse(false), nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	handler := a.routes()
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, quotaTestRequest(ctx, secret, "local_save_failure", false))
		assertQuotaResponse(t, response, status)
		usedTokens, _, _ := quotaRecordedUsage(a, nil)
		if usedTokens != 0 {
			t.Fatalf("failed save retained an uncommitted charge: tokens=%d", usedTokens)
		}
	}
	a.path, a.store = validPath, nil
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, quotaTestRequest(ctx, secret, "local_save_recovery", false))
	assertQuotaResponse(t, response, http.StatusTooManyRequests)
	usedTokens, _, _ := quotaRecordedUsage(a, nil)
	if usedTokens != 277 || calls.Load() != 1 {
		t.Fatalf("local save recovery duplicated or lost usage: tokens=%d calls=%d", usedTokens, calls.Load())
	}
}

func TestGatewayQuotaWaitingRequestRevalidatesState(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		name := "disabled_while_waiting"
		if cancelRequest {
			name = "canceled_while_waiting"
		}
		t.Run(name, func(t *testing.T) { testWaitingQuotaRequest(t, cancelRequest) })
	}
}

func testWaitingQuotaRequest(t *testing.T, cancelRequest bool) {
	t.Helper()
	a, repository, secret := newQuotaTestApp(t, true, int64Ptr(10), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	released := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	defer release()
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-released:
			return quotaUpstreamResponse(false), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	handler := a.routes()
	first := serveQuotaRequest(handler, quotaTestRequest(ctx, secret, "active", false))
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first request did not reach upstream")
	}
	parent, cancelWaiter := context.WithCancel(ctx)
	defer cancelWaiter()
	waiter := &quotaWaitContext{Context: parent, waiting: make(chan struct{})}
	second := serveQuotaRequest(handler, quotaTestRequest(waiter, secret, "waiting", false))
	select {
	case <-waiter.waiting:
	case <-ctx.Done():
		t.Fatal("second request did not wait for admission")
	}
	finishWaitingQuotaRequest(t, ctx, repository, cancelRequest, cancelWaiter, release, first, second)
}

func finishWaitingQuotaRequest(t *testing.T, ctx context.Context, repository *quotaKeyRepository, canceled bool, cancel context.CancelFunc, release func(), first, second <-chan *httptest.ResponseRecorder) {
	t.Helper()
	if canceled {
		cancel()
		assertQuotaResponse(t, waitQuotaResponse(t, ctx, second), http.StatusServiceUnavailable)
	} else {
		repository.mu.Lock()
		repository.key.Status = "disabled"
		repository.mu.Unlock()
	}
	release()
	assertQuotaResponse(t, waitQuotaResponse(t, ctx, first), http.StatusOK)
	if !canceled {
		assertQuotaResponse(t, waitQuotaResponse(t, ctx, second), http.StatusUnauthorized)
	}
}

func TestGatewayQuotaSettlementRejectsUnavailableKey(t *testing.T) {
	for _, test := range []struct {
		name       string
		persistent bool
		message    string
	}{
		{name: "missing_repository", persistent: true, message: "repository unavailable"},
		{name: "local_id_reused_after_deletion", message: "key unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _, secret := newQuotaTestApp(t, false, int64Ptr(10), nil)
			key := a.data.Keys[0]
			a.data.Keys[0].Hash = digest("replacement-key-with-reused-id")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := a.settleGatewayQuota(usageEvent{Context: ctx, Key: key, KeyHash: digest(secret), Persistent: test.persistent, Metrics: usageMetrics{TotalTokens: 277}})
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("unavailable key settlement error=%v", err)
			}
			if a.data.Keys[0].QuotaUsedTokens != 0 {
				t.Fatalf("deleted credential charged replacement key: %+v", a.data.Keys[0])
			}
		})
	}
}

func TestGatewayQuotaSettlementStopsRetryingCanceledContext(t *testing.T) {
	a, repository, _ := newQuotaTestApp(t, true, int64Ptr(10), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := a.settleGatewayQuota(usageEvent{Context: ctx, Persistent: true, Key: apiKey{ID: 7, OrgID: 2}, UsageID: "expired-settlement"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled settlement error=%v", err)
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.attemptIDs) != 1 || len(repository.records) != 0 {
		t.Fatalf("canceled settlement kept retrying: attempts=%d records=%d", len(repository.attemptIDs), len(repository.records))
	}
}
