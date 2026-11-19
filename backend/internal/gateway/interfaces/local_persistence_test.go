package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/edition"
)

type controlledGatewayStore struct {
	gatewayDataStore
	save func(any) error
}

func (store controlledGatewayStore) Save(value any) error { return store.save(value) }

func localPersistenceApp(t *testing.T) (*app, string, usageEvent) {
	t.Helper()
	a := testApp(t)
	secret := "sk-gw-local-persistence-regression-secret"
	key := apiKey{ID: 1, Hash: digest(secret), Status: "active", ProviderID: 1}
	a.data.Keys = []apiKey{key}
	a.data.Providers = []provider{{ID: 1, Type: "api_key", Status: "active"}}
	event := usageEvent{
		Context: context.Background(), Key: key, KeyHash: key.Hash, Model: "test-model", Status: http.StatusOK,
		Duration: time.Second, Metrics: usageMetrics{InputTokens: 2, OutputTokens: 3, TotalTokens: 5, Cost: 0.25},
	}
	return a, secret, event
}

func awaitLocalPersistence[T any](t *testing.T, result <-chan T) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("local persistence operation blocked")
		var zero T
		return zero
	}
}

// Exercise both authentication locks and the usage query while the underlying
// store is deliberately blocked, without relying on filesystem timing.
func assertLocalPersistenceReaders(t *testing.T, a *app, secret string, tokens int64) {
	t.Helper()
	result := make(chan bool, 1)
	go func() {
		request := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
		request.Header.Set("Authorization", "Bearer "+secret)
		key, found := a.authenticatedKey(request)
		_, providerFound := a.authenticateKey(request)
		_, selected := a.providerForKeyModel(request.Context(), key, "test-model")
		response := httptest.NewRecorder()
		a.usage(response, request)
		result <- found && providerFound && selected && key.QuotaUsedTokens == tokens && response.Code == http.StatusOK
	}()
	if !awaitLocalPersistence(t, result) {
		t.Fatal("authentication or usage query did not see the committed state")
	}
}

func reloadLocalPersistence(t *testing.T, a *app) data {
	t.Helper()
	var restored data
	if err := gatewayinfra.NewEncryptedStore(a.path, a.encryptionKey).Load(&restored); err != nil {
		t.Fatal(err)
	}
	return restored
}

func TestGatewayUsageSaveRetriesDoNotBlockAuthentication(t *testing.T) {
	a, secret, event := localPersistenceApp(t)
	backing := gatewayinfra.NewEncryptedStore(a.path, a.encryptionKey)
	attempts := make(chan int, 3)
	proceed := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(proceed) }) }
	t.Cleanup(release)
	var calls atomic.Int64
	a.store = controlledGatewayStore{gatewayDataStore: backing, save: func(value any) error {
		attempt := int(calls.Add(1))
		attempts <- attempt
		<-proceed
		if attempt < 3 {
			return errors.New("temporary write failure")
		}
		return backing.Save(value)
	}}
	done := make(chan struct{})
	go func() {
		a.processUsageEvent(event)
		close(done)
	}()
	for attempt := 1; attempt <= 3; attempt++ {
		if got := awaitLocalPersistence(t, attempts); got != attempt {
			t.Fatalf("save attempt=%d, want %d", got, attempt)
		}
		assertLocalPersistenceReaders(t, a, secret, 0)
		proceed <- struct{}{}
	}
	awaitLocalPersistence(t, done)
	restored := reloadLocalPersistence(t, a)
	if stats := restored.Usage[event.KeyHash]; stats.Requests != 1 || stats.Models[event.Model].TotalTokens != 5 || stats.Cost != 0.25 {
		t.Fatalf("retries lost or duplicated usage: %+v", stats)
	}
	if a.usageQueueFailed.Load() != 0 {
		t.Fatal("successful retry counted as a failed event")
	}
}

func TestGatewayLocalQuotaSavePublishesOnlyOnSuccess(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		name := "success"
		if failSave {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			a, secret, event := localPersistenceApp(t)
			backing := gatewayinfra.NewEncryptedStore(a.path, a.encryptionKey)
			entered, proceed := make(chan struct{}), make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(proceed) }) }
			t.Cleanup(release)
			writeErr := errors.New("quota write failed")
			a.store = controlledGatewayStore{gatewayDataStore: backing, save: func(value any) error {
				close(entered)
				<-proceed
				if failSave {
					return writeErr
				}
				return backing.Save(value)
			}}
			done := make(chan error, 1)
			go func() { done <- a.settleLocalQuota(event) }()
			awaitLocalPersistence(t, entered)
			assertLocalPersistenceReaders(t, a, secret, 0)
			release()
			err := awaitLocalPersistence(t, done)
			if failSave {
				if !errors.Is(err, writeErr) {
					t.Fatalf("settlement error=%v", err)
				}
				assertLocalPersistenceReaders(t, a, secret, 0)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertLocalPersistenceReaders(t, a, secret, 5)
			if key := reloadLocalPersistence(t, a).Keys[0]; key.QuotaUsedTokens != 5 || key.QuotaUsedAmount != 0.25 {
				t.Fatalf("quota not persisted: %+v", key)
			}
		})
	}
}

func TestGatewayLocalConfigurationSaveFailureKeepsCommittedState(t *testing.T) {
	for _, operation := range []string{"disable_key", "reset_key", "delete_key", "update_provider"} {
		t.Run(operation, func(t *testing.T) {
			a, secret, _ := localPersistenceApp(t)
			a.data.Providers[0].Name = "original"
			entered, proceed := make(chan struct{}), make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(proceed) }) }
			t.Cleanup(release)
			a.store = controlledGatewayStore{save: func(any) error {
				close(entered)
				<-proceed
				return errors.New("configuration write failed")
			}}
			done := make(chan int, 1)
			go func() {
				response := httptest.NewRecorder()
				switch operation {
				case "disable_key":
					a.keyAction(response, httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"status":"disabled"}`)), "1/status")
				case "reset_key":
					a.keyAction(response, httptest.NewRequest(http.MethodPost, "/", nil), "1/reset")
				case "delete_key":
					a.keyAction(response, httptest.NewRequest(http.MethodDelete, "/", nil), "1")
				case "update_provider":
					payload := `{"name":"changed","platform":"openai","type":"api_key","status":"disabled"}`
					a.updateProvider(response, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(payload)), "1")
				}
				done <- response.Code
			}()
			awaitLocalPersistence(t, entered)
			assertLocalPersistenceReaders(t, a, secret, 0)
			release()
			if status := awaitLocalPersistence(t, done); status != http.StatusInternalServerError {
				t.Fatalf("failed configuration status=%d", status)
			}
			assertLocalPersistenceReaders(t, a, secret, 0)
			if a.data.Providers[0].Name != "original" {
				t.Fatal("failed provider change leaked into committed state")
			}
		})
	}
}

func TestGatewayUsageQuotaAndConfigurationSavesStayOrdered(t *testing.T) {
	a, secret, event := localPersistenceApp(t)
	backing := gatewayinfra.NewEncryptedStore(a.path, a.encryptionKey)
	entered, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(proceed) }) }
	t.Cleanup(release)
	var calls atomic.Int64
	a.store = controlledGatewayStore{gatewayDataStore: backing, save: func(value any) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-proceed
		}
		return backing.Save(value)
	}}
	usageDone := make(chan struct{})
	go func() { a.processUsageEvent(event); close(usageDone) }()
	awaitLocalPersistence(t, entered)
	quotaDone := make(chan error, 1)
	go func() { quotaDone <- a.settleLocalQuota(event) }()
	configurationDone := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"status":"disabled"}`))
		a.keyAction(response, request, "1/status")
		configurationDone <- response.Code
	}()
	assertLocalPersistenceReaders(t, a, secret, 0)
	release()
	awaitLocalPersistence(t, usageDone)
	if err := awaitLocalPersistence(t, quotaDone); err != nil {
		t.Fatal(err)
	}
	if status := awaitLocalPersistence(t, configurationDone); status != http.StatusOK {
		t.Fatalf("configuration status=%d", status)
	}
	restored := reloadLocalPersistence(t, a)
	if restored.Keys[0].Status != "disabled" || restored.Keys[0].QuotaUsedTokens != 5 || restored.Usage[event.KeyHash].Requests != 1 {
		t.Fatalf("concurrent saves overwrote a committed change: %+v", restored)
	}
	if calls.Load() != 3 {
		t.Fatalf("save calls=%d, want 3", calls.Load())
	}
}

func TestGatewayUsageQueueBatchesAndDrains(t *testing.T) {
	a, secret, event := localPersistenceApp(t)
	backing := gatewayinfra.NewEncryptedStore(a.path, a.encryptionKey)
	entered, proceed := make(chan struct{}), make(chan struct{})
	var releaseOnce, stopOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(proceed) }) }
	stop := func() { stopOnce.Do(a.stopUsageQueue) }
	t.Cleanup(func() { release(); stop() })
	var calls atomic.Int64
	a.store = controlledGatewayStore{gatewayDataStore: backing, save: func(value any) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-proceed
		}
		return backing.Save(value)
	}}
	a.startUsageQueue()
	a.usageQueue <- event
	awaitLocalPersistence(t, entered)
	for range 300 {
		a.usageQueue <- event
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	assertLocalPersistenceReaders(t, a, secret, 0)
	select {
	case <-done:
		t.Fatal("stop returned before the blocked save finished")
	default:
	}
	release()
	awaitLocalPersistence(t, done)
	stats := reloadLocalPersistence(t, a).Usage[event.KeyHash]
	if stats.Requests != 301 || stats.Models[event.Model].TotalTokens != 1505 || stats.DurationMS != 301000 {
		t.Fatalf("shutdown lost queued usage: %+v", stats)
	}
	if len(a.usageQueue) != 0 || calls.Load() != 4 {
		t.Fatalf("queue length=%d saves=%d, want empty queue and 4 batch saves", len(a.usageQueue), calls.Load())
	}
}

func TestGatewayUsageFailedBatchRetainsStatisticsForNextSave(t *testing.T) {
	a, _, event := localPersistenceApp(t)
	backing := gatewayinfra.NewEncryptedStore(a.path, a.encryptionKey)
	var calls atomic.Int64
	a.store = controlledGatewayStore{gatewayDataStore: backing, save: func(any) error {
		calls.Add(1)
		return errors.New("storage unavailable")
	}}
	a.processUsageEvents([]usageEvent{event, event})
	if calls.Load() != 3 || a.usageQueueFailed.Load() != 2 {
		t.Fatalf("save calls=%d failed events=%d", calls.Load(), a.usageQueueFailed.Load())
	}
	a.store = backing
	if err := a.settleLocalQuota(event); err != nil {
		t.Fatal(err)
	}
	restored := reloadLocalPersistence(t, a)
	if restored.Usage[event.KeyHash].Requests != 2 || restored.Keys[0].QuotaUsedTokens != 5 {
		t.Fatalf("later save lost usage or charged quota twice: %+v", restored)
	}
}

func TestGatewayConcurrentLocalKeyCreationHonorsCommunityLimit(t *testing.T) {
	a := testApp(t)
	a.data.Providers = []provider{{ID: 1, Status: "active"}}
	for id := 1; id < edition.MaxAPIKeys; id++ {
		a.data.Keys = append(a.data.Keys, apiKey{ID: id, Status: "disabled"})
	}
	const attempts = 4
	start := make(chan struct{})
	results := make(chan int, attempts)
	for range attempts {
		go func() {
			<-start
			response := httptest.NewRecorder()
			a.createKey(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"last slot"}`)))
			results <- response.Code
		}()
	}
	close(start)
	var created int
	for range attempts {
		switch status := awaitLocalPersistence(t, results); status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
		default:
			t.Fatalf("unexpected create status: %d", status)
		}
	}
	if keys := reloadLocalPersistence(t, a).Keys; created != 1 || len(keys) != edition.MaxAPIKeys {
		t.Fatalf("created=%d persisted=%d limit=%d", created, len(keys), edition.MaxAPIKeys)
	}
}

func TestGatewayUsageResponseReadsConsistentStatistics(t *testing.T) {
	a, secret, event := localPersistenceApp(t)
	a.store = controlledGatewayStore{save: func(any) error { return nil }}
	request := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	read := func() int64 {
		t.Helper()
		response := httptest.NewRecorder()
		a.usage(response, request)
		var result struct {
			Data struct {
				Total  modelUsage   `json:"total"`
				Models []modelUsage `json:"models"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK {
			t.Fatalf("usage status=%d error=%v", response.Code, err)
		}
		var requests, tokens int64
		for _, stats := range result.Data.Models {
			requests += stats.Requests
			tokens += stats.TotalTokens
		}
		if total := result.Data.Total; requests != total.Requests || tokens != total.TotalTokens {
			t.Fatalf("inconsistent usage snapshot: total=%+v model requests=%d tokens=%d", total, requests, tokens)
		}
		return requests
	}
	const count = 128
	done := make(chan struct{})
	go func() {
		defer close(done)
		models := [...]string{"model-a", "model-b"}
		for i := range count {
			event.Model = models[i%len(models)]
			a.processUsageEvent(event)
		}
	}()
	for range count {
		read()
	}
	awaitLocalPersistence(t, done)
	if got := read(); got != count {
		t.Fatalf("final request count=%d, want %d", got, count)
	}
}
