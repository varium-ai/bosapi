package infrastructure

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
)

func testUnreadCache(now func() time.Time, capacity int) *securityUnreadCache {
	return &securityUnreadCache{entries: make(map[domain.SecurityEventReadScope]*securityUnreadEntry), now: now, capacity: capacity}
}

func TestSecurityUnreadCacheScopesExpiryAndCapacity(t *testing.T) {
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	cache := testUnreadCache(func() time.Time { return now }, 2)
	var calls int64
	load := func(context.Context) (int64, error) { calls++; return calls, nil }
	ctx := context.Background()
	scope := domain.SecurityEventReadScope{UserID: 1, OrganizationID: 9}
	for range 2 {
		if count, err := cache.get(ctx, scope, load); err != nil || count != 1 {
			t.Fatalf("count=%d error=%v", count, err)
		}
	}
	for _, other := range []domain.SecurityEventReadScope{{UserID: 1}, {UserID: 2, OrganizationID: 9}} {
		now = now.Add(time.Second)
		if _, err := cache.get(ctx, other, load); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 || len(cache.entries) != 2 || cache.entries[scope] != nil {
		t.Fatalf("scope leakage or unbounded cache: calls=%d entries=%v", calls, cache.entries)
	}
	scope = domain.SecurityEventReadScope{UserID: 2, OrganizationID: 9}
	now = now.Add(securityUnreadTTL)
	if count, err := cache.get(ctx, scope, load); err != nil || count != 4 {
		t.Fatalf("expired count=%d error=%v", count, err)
	}
}

func TestSecurityUnreadCacheCoalescesConcurrentPolling(t *testing.T) {
	cache := testUnreadCache(time.Now, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(ctx context.Context) (int64, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return 7, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	var readers sync.WaitGroup
	results := make(chan int64, 20)
	for range 20 {
		readers.Go(func() {
			count, err := cache.get(ctx, domain.SecurityEventReadScope{UserID: 1}, load)
			if err != nil {
				results <- -1
				return
			}
			results <- count
		})
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	close(release)
	readers.Wait()
	close(results)
	for count := range results {
		if count != 7 {
			t.Fatalf("count=%d", count)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate count queries=%d", calls.Load())
	}
}

func TestSecurityUnreadCacheInvalidatesInflightCount(t *testing.T) {
	cache := testUnreadCache(time.Now, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	scope := domain.SecurityEventReadScope{UserID: 1, OrganizationID: 9}
	started, release, result := make(chan struct{}), make(chan struct{}), make(chan int64, 1)
	var calls atomic.Int32
	go func() {
		count, err := cache.get(ctx, scope, func(ctx context.Context) (int64, error) {
			if calls.Add(1) == 1 {
				close(started)
				select {
				case <-release:
					return 9, nil
				case <-ctx.Done():
					return 0, ctx.Err()
				}
			}
			return 1, nil // A late-committing event remains unread after acknowledgement.
		})
		if err != nil {
			result <- -1
			return
		}
		result <- count
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cache.invalidate(scope)
	close(release)
	select {
	case count := <-result:
		if count != 1 || calls.Load() != 2 {
			t.Fatalf("stale count=%d calls=%d", count, calls.Load())
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestSecurityUnreadCacheCancellationAndFailures(t *testing.T) {
	cache := testUnreadCache(time.Now, 1)
	scope := domain.SecurityEventReadScope{UserID: 1}
	failure := errors.New("query failed")
	if _, err := cache.get(context.Background(), scope, func(context.Context) (int64, error) { return 0, failure }); !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
	if len(cache.entries) != 0 {
		t.Fatal("failed count was cached")
	}
	entry, _, _ := cache.acquire(scope)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.get(ctx, scope, func(context.Context) (int64, error) { t.Error("canceled query executed"); return 0, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	// With all slots busy, another scope still reads correctly without growing the cache.
	if count, err := cache.get(context.Background(), domain.SecurityEventReadScope{UserID: 2}, func(context.Context) (int64, error) { return 4, nil }); err != nil || count != 4 || len(cache.entries) != 1 {
		t.Fatalf("fallback count=%d error=%v entries=%d", count, err, len(cache.entries))
	}
	cache.finish(scope, entry, 0, nil)
}

type waitingUnreadContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *waitingUnreadContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func TestSecurityUnreadCacheWaitingReaderCancellationAndQueryFailure(t *testing.T) {
	for _, cancelWaiter := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancellation", false: "query failure"}[cancelWaiter], func(t *testing.T) {
			cache := testUnreadCache(time.Now, 1)
			scope := domain.SecurityEventReadScope{UserID: 7}
			entry, _, _ := cache.acquire(scope)
			parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			waiter, cancelWait := context.WithCancel(parent)
			defer cancelWait()
			ctx := &waitingUnreadContext{Context: waiter, waiting: make(chan struct{})}
			result := make(chan error, 1)
			go func() {
				_, err := cache.get(ctx, scope, func(context.Context) (int64, error) {
					t.Error("waiting reader ran another count")
					return 0, nil
				})
				result <- err
			}()
			select {
			case <-ctx.waiting:
			case <-parent.Done():
				t.Fatal(parent.Err())
			}
			failure := errors.New("count failed")
			if cancelWaiter {
				cancelWait()
			} else {
				cache.finish(scope, entry, 0, failure)
			}
			select {
			case err := <-result:
				if cancelWaiter && !errors.Is(err, context.Canceled) || !cancelWaiter && !errors.Is(err, failure) {
					t.Fatalf("error=%v", err)
				}
			case <-parent.Done():
				t.Fatal(parent.Err())
			}
			if cancelWaiter {
				cache.finish(scope, entry, 0, nil)
			}
		})
	}
}
