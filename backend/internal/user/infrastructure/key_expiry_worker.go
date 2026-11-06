package infrastructure

import (
	"context"
	"sync"
	"time"

	"bosapi/backend/internal/platform/logging"
)

type expiredKeyRepository interface {
	DisableExpiredKeys(context.Context, time.Time) error
}

// KeyExpiryWorker persists expired key status even when credentials receive no traffic.
type KeyExpiryWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// NewKeyExpiryWorker sweeps at startup and every 30 seconds; authentication rejects expiry immediately.
func NewKeyExpiryWorker(repository expiredKeyRepository) *KeyExpiryWorker {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &KeyExpiryWorker{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(worker.done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		worker.run(ctx, repository, ticker.C, time.Now)
	}()
	return worker
}

func (worker *KeyExpiryWorker) run(ctx context.Context, repository expiredKeyRepository, ticks <-chan time.Time, now func() time.Time) {
	for ctx.Err() == nil {
		sweepCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := repository.DisableExpiredKeys(sweepCtx, now().UTC())
		cancel()
		if err != nil && ctx.Err() == nil {
			logging.Printf(ctx, "ERROR", "api_key.expiry.failed", "code=expiry_sweep_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

// Close cancels the sweep and waits before the database connection is released.
func (worker *KeyExpiryWorker) Close() {
	worker.once.Do(worker.cancel)
	<-worker.done
}
