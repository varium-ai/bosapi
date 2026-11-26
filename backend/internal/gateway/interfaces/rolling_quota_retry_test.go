package interfaces

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	adminDomain "bosapi/backend/internal/admin/domain"
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
)

type missingRollingKeyRepository struct {
	adminDomain.RateLimitRepository
	err error
}

func (repository missingRollingKeyRepository) ReadRateLimits(context.Context, uint64, uint64) (adminDomain.RateLimitUpdate, error) {
	return adminDomain.RateLimitUpdate{}, repository.err
}

func TestDeletedKeyRollingQuotaDoesNotBlockOrganizationReceipts(t *testing.T) {
	repository := newDurableReceiptRepository()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = client.Close() })
	limiter := gatewayinfra.NewRedisRateLimiter(client, missingRollingKeyRepository{err: gorm.ErrRecordNotFound}, "test:deleted")
	runtime := &organizationQuotaStub{allowed: true}
	a := &app{keyRepository: repository, organizationQuota: runtime, usageQueue: make(chan usageEvent, 1)}
	a.background.RateLimiter = limiter
	recordOrganizationReceipt(t, a, 7, 2)
	for range 2 {
		if allowed, response := organizationQuotaCheck(a, t.Context(), 8, 2); !allowed {
			t.Fatalf("deleted key blocked another key: status=%d body=%s", response.Code, response.Body)
		}
	}
	if len(repository.records) != 1 || len(a.organizationPending) != 0 || len(repository.rows) != 0 || runtime.inserted != 1 {
		t.Fatalf("ledger=%d pending=%d durable=%d inserted=%d", len(repository.records), len(a.organizationPending), len(repository.rows), runtime.inserted)
	}
}

func TestRollingQuotaBackendFailureStillRetainsReceiptForRecovery(t *testing.T) {
	want := errors.New("configuration backend unavailable")
	repository := newDurableReceiptRepository()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = client.Close() })
	limiter := gatewayinfra.NewRedisRateLimiter(client, missingRollingKeyRepository{err: want}, "test:failure")
	a := &app{keyRepository: repository, organizationQuota: &organizationQuotaStub{allowed: true}, usageQueue: make(chan usageEvent, 1)}
	a.background.RateLimiter = limiter
	event := recordOrganizationReceipt(t, a, 7, 2)
	if len(repository.records) != 1 || len(repository.rows) != 1 {
		t.Fatalf("unconfirmed rolling counters were dropped: ledger=%d receipts=%d", len(repository.records), len(repository.rows))
	}
	if allowed, response := organizationQuotaCheck(a, t.Context(), 8, 2); allowed || response.Code != 503 {
		t.Fatalf("actual backend failure bypassed recovery: allowed=%v status=%d", allowed, response.Code)
	}
	if err := limiter.RecordTokenUsage(t.Context(), 7, "receipt", 1, 0, time.Now()); !errors.Is(err, want) {
		t.Fatalf("backend error masked: %v", err)
	}
	// The key can be deleted while a failed counter receipt is waiting. Its
	// ledger must remain exactly once and organization admission must recover.
	a.background.RateLimiter = gatewayinfra.NewRedisRateLimiter(client, missingRollingKeyRepository{err: gorm.ErrRecordNotFound}, "test:failure")
	if allowed, response := organizationQuotaCheck(a, t.Context(), 8, 2); !allowed {
		t.Fatalf("deleted-key receipt did not recover: %s", response.Body)
	}
	if len(repository.records) != 1 || len(a.organizationPending) != 0 {
		t.Fatalf("recovery duplicated or retained receipt %s", event.UsageID)
	}
}
