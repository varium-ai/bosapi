package interfaces

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	userinfra "bosapi/backend/internal/user/infrastructure"
)

type durableReceiptRepository struct {
	*organizationReceiptRepository
	rows       []userinfra.GatewayUsageRecord
	listErr    error
	lists      int
	listStart  chan struct{}
	listFinish chan struct{}
}

func (r *durableReceiptRepository) SaveGatewayQuotaRetry(_ context.Context, record userinfra.GatewayUsageRecord) error {
	r.rows = append(r.rows, record)
	return nil
}
func (r *durableReceiptRepository) ListGatewayQuotaRetries(ctx context.Context, id uint64, limit int) ([]userinfra.GatewayUsageRecord, error) {
	r.mu.Lock()
	r.lists++
	r.mu.Unlock()
	if r.listStart != nil {
		select {
		case r.listStart <- struct{}{}:
		default:
		}
		select {
		case <-r.listFinish:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if r.listErr != nil {
		return nil, r.listErr
	}
	var rows []userinfra.GatewayUsageRecord
	for _, row := range r.rows {
		if row.OrganizationID == id && len(rows) < limit {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
func (r *durableReceiptRepository) ScanGatewayQuotaRetries(_ context.Context, after uint64, limit int) ([]userinfra.GatewayUsageRecord, uint64, error) {
	end := min(len(r.rows), int(after)+limit)
	return r.rows[int(after):end], uint64(end), nil
}
func (r *durableReceiptRepository) DeleteGatewayQuotaRetry(_ context.Context, request, usage string) error {
	for i, row := range r.rows {
		if row.RequestID == request && row.UsageID == usage {
			r.rows[i].OrganizationID = 0
		}
	}
	return nil
}
func newDurableReceiptRepository() *durableReceiptRepository {
	return &durableReceiptRepository{organizationReceiptRepository: &organizationReceiptRepository{records: make(map[string]userinfra.GatewayUsageRecord)}}
}

func TestQuotaRecoveryRestartAndDuplicate(t *testing.T) {
	repo := newDurableReceiptRepository()
	repo.failure = errors.New("lost commit response")
	repo.commitThenFail = true
	a := &app{keyRepository: repo, organizationQuota: &organizationQuotaStub{allowed: true}, usageQueue: make(chan usageEvent, 1)}
	event := recordOrganizationReceipt(t, a, 7, 2)
	if len(repo.rows) != 1 {
		t.Fatal("failed settlement was not saved")
	}
	repo.failure = nil
	// A fresh app has no in-memory receipts or admission state.
	restarted := &app{keyRepository: repo, organizationQuota: &organizationQuotaStub{allowed: true}}
	restarted.recoverGatewayQuotaRetries()
	if len(repo.records) != 1 || repo.rows[0].OrganizationID != 0 {
		t.Fatalf("duplicate charged or outbox not drained: %+v", repo)
	}
	last := repo.attempts[len(repo.attempts)-1]
	if last.UsageID != event.UsageID || !last.CreatedAt.Equal(event.CreatedAt) || last.DurationMillis != 1000 {
		t.Fatalf("recovery changed receipt: %+v", last)
	}
}

func TestQuotaRecoveryAdvancesPastFailedPage(t *testing.T) {
	repo := newDurableReceiptRepository()
	for i := range 257 {
		org := uint64(2)
		if i == 256 {
			org = 3
		}
		repo.rows = append(repo.rows, userinfra.GatewayUsageRecord{RequestID: fmt.Sprint(i), UsageID: fmt.Sprint(i), APIKeyID: 7, OrganizationID: org, CreatedAt: time.Now()})
	}
	repo.beforePersist = func(_ context.Context, row userinfra.GatewayUsageRecord) error {
		if row.OrganizationID == 2 {
			return errors.New("permanent tenant failure")
		}
		return nil
	}
	a := &app{keyRepository: repo, organizationQuota: &organizationQuotaStub{allowed: true}}
	a.recoverGatewayQuotaRetries()
	attempts := len(repo.attempts)
	a.recoverGatewayQuotaRetries()
	if len(repo.records) != 1 || repo.rows[256].OrganizationID != 0 || a.quotaRetryCursor != 0 {
		t.Fatalf("later tenant starved: records=%d cursor=%d", len(repo.records), a.quotaRetryCursor)
	}
	if len(repo.attempts) != attempts+1 {
		t.Fatal("failed tenant ignored retry backoff")
	}
}

func TestQuotaRecoveryDrainsMemoryWithoutDurableRows(t *testing.T) {
	repo := newDurableReceiptRepository()
	a := &app{keyRepository: repo, organizationQuota: &organizationQuotaStub{allowed: true}}
	a.retainOrganizationReceipt(usageEvent{Context: t.Context(), RequestID: "memory", UsageID: "memory", Persistent: true, Key: apiKey{ID: 7, OrgID: 2}, CreatedAt: time.Now()})
	a.recoverGatewayQuotaRetries()
	if len(repo.records) != 1 || len(a.organizationPending) != 0 {
		t.Fatal("receipt with a failed outbox write was never recovered")
	}
}

func TestQuotaRecoveryProbeGraceAndColdFailure(t *testing.T) {
	repo := newDurableReceiptRepository()
	a := &app{keyRepository: repo}
	if err := a.hydrateOrganizationReceipts(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	probe := a.organizationPendingProbe[2]
	probe.checkedAt = time.Now().Add(-6 * time.Second)
	repo.listErr = errors.New("temporary read failure")
	if err := a.hydrateOrganizationReceipts(t.Context(), 2); err != nil {
		t.Fatalf("recently empty tenant blocked: %v", err)
	}
	// A cached fallback must not extend the grace window beyond 30 seconds.
	probe.emptyAt = time.Now().Add(-31 * time.Second)
	if err := a.hydrateOrganizationReceipts(t.Context(), 2); err == nil {
		t.Fatal("expired grace admitted request")
	}
	if err := a.hydrateOrganizationReceipts(t.Context(), 3); err == nil {
		t.Fatal("cold failure admitted request")
	}
	if err := a.hydrateOrganizationReceipts(t.Context(), 3); err == nil {
		t.Fatal("failed cold probe was cached as success")
	}
}

func TestQuotaRecoveryProbeSingleFlight(t *testing.T) {
	repo := newDurableReceiptRepository()
	repo.listStart, repo.listFinish = make(chan struct{}, 1), make(chan struct{})
	a := &app{keyRepository: repo}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.hydrateOrganizationReceipts(t.Context(), 2); err != nil {
				t.Error(err)
			}
		}()
	}
	<-repo.listStart
	close(repo.listFinish)
	wg.Wait()
	if repo.lists != 1 {
		t.Fatalf("concurrent probes=%d", repo.lists)
	}
}

func TestQuotaRecoveryKnownReceiptOverridesEmptyGrace(t *testing.T) {
	repo := newDurableReceiptRepository()
	a := &app{keyRepository: repo, organizationQuota: &organizationQuotaStub{allowed: true}}
	if err := a.hydrateOrganizationReceipts(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	a.retainOrganizationReceipt(usageEvent{Context: t.Context(), RequestID: "failed", UsageID: "failed", Persistent: true, Key: apiKey{ID: 7, OrgID: 2}, CreatedAt: time.Now()})
	repo.failure = errors.New("ledger unavailable")
	if _, err := a.retryOrganizationReceipts(t.Context(), 2); err == nil {
		t.Fatal("cached empty probe bypassed a known unsettled receipt")
	}
}

func TestQuotaRecoveryProbeTimeoutDoesNotCacheSuccess(t *testing.T) {
	repo := newDurableReceiptRepository()
	repo.listStart, repo.listFinish = make(chan struct{}, 1), make(chan struct{})
	a := &app{keyRepository: repo}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := a.hydrateOrganizationReceipts(ctx, 2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("probe timeout=%v", err)
	}
	close(repo.listFinish)
	if err := a.hydrateOrganizationReceipts(ctx, 2); err != nil || repo.lists != 2 {
		t.Fatalf("failed probe prevented recovery: probes=%d err=%v", repo.lists, err)
	}
}
