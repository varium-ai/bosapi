package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func clockedCache(t *testing.T) (*Cache, *atomic.Int64) {
	t.Helper()
	c := New(nil, "test:", 20*time.Minute, 30*time.Minute, 4)
	t.Cleanup(c.Close)
	now := &atomic.Int64{}
	now.Store(time.Now().UnixNano())
	c.now = func() time.Time { return time.Unix(0, now.Load()) }
	return c, now
}
func awaitIdle(t *testing.T, c *Cache) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		c.mu.Lock()
		busy := c.busy
		c.mu.Unlock()
		if !busy {
			return
		}
		select {
		case <-deadline:
			t.Fatal("refresh did not finish")
		case <-time.After(time.Millisecond):
		}
	}
}
func TestConcurrentColdLoadsAndMutationIsolation(t *testing.T) {
	c, _ := clockedCache(t)
	var calls atomic.Int32
	release := make(chan struct{})
	load := func(context.Context) (json.RawMessage, time.Time, error) {
		calls.Add(1)
		<-release
		return json.RawMessage(`{"count":12}`), time.Time{}, nil
	}
	var wg sync.WaitGroup
	results := make(chan Value, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := c.Get(context.Background(), "key", false, load)
			if err != nil {
				t.Error(err)
			}
			results <- v
		}()
	}
	close(release)
	wg.Wait()
	close(results)
	if calls.Load() != 1 {
		t.Fatalf("loads=%d", calls.Load())
	}
	for v := range results {
		v.Data[0] = '!'
	}
	v, err := c.Get(context.Background(), "key", false, load)
	if err != nil || string(v.Data) != `{"count":12}` {
		t.Fatalf("value=%s err=%v", v.Data, err)
	}
}
func TestRefreshFailurePreservesValidDataAndHidesExpiredData(t *testing.T) {
	c, now := clockedCache(t)
	load := func(context.Context) (json.RawMessage, time.Time, error) {
		return json.RawMessage(`1`), time.Time{}, nil
	}
	original, err := c.Get(context.Background(), "key", false, load)
	if err != nil {
		t.Fatal(err)
	}
	now.Add(int64(21 * time.Minute))
	release := make(chan struct{})
	failing := func(context.Context) (json.RawMessage, time.Time, error) {
		<-release
		return nil, time.Time{}, errors.New("db unavailable")
	}
	old, err := c.Get(context.Background(), "key", true, failing)
	if err != nil || old.ID != original.ID || old.Status != "refreshing" {
		t.Fatalf("old=%+v err=%v", old, err)
	}
	close(release)
	awaitIdle(t, c)
	old, err = c.Get(context.Background(), "key", false, failing)
	if err != nil || old.ID != original.ID || old.Status != "refresh_failed" {
		t.Fatalf("old=%+v err=%v", old, err)
	}
	now.Add(int64(10 * time.Minute))
	expired, err := c.Get(context.Background(), "key", false, failing)
	if !errors.Is(err, ErrExpired) || len(expired.Data) != 0 {
		t.Fatalf("expired=%+v err=%v", expired, err)
	}
}
func TestCanceledCallerDoesNotCancelSharedLoad(t *testing.T) {
	c, _ := clockedCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	started, release := make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context) (json.RawMessage, time.Time, error) {
		close(started)
		select {
		case <-ctx.Done():
			return nil, time.Time{}, ctx.Err()
		case <-release:
			return json.RawMessage(`42`), time.Time{}, nil
		}
	}
	result := make(chan error, 1)
	go func() { _, err := c.Get(ctx, "key", false, load); result <- err }()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	close(release)
	awaitIdle(t, c)
	v, err := c.Get(context.Background(), "key", false, load)
	if err != nil || string(v.Data) != "42" {
		t.Fatalf("survivor=%+v err=%v", v, err)
	}
}
func TestCapacityBoundAndSourceFreshness(t *testing.T) {
	c, now := clockedCache(t)
	for i := range 8 {
		key := Key(i)
		_, err := c.Get(context.Background(), key, false, func(context.Context) (json.RawMessage, time.Time, error) {
			return json.RawMessage(`1`), time.Time{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		now.Add(int64(time.Second))
	}
	c.mu.Lock()
	n := len(c.entries)
	c.mu.Unlock()
	if n != 4 {
		t.Fatalf("capacity=%d", n)
	}
	_, err := c.Get(context.Background(), "lagging", false, func(context.Context) (json.RawMessage, time.Time, error) {
		return json.RawMessage(`1`), time.Unix(0, now.Load()).Add(-time.Minute), nil
	})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("source lag ignored: %v", err)
	}
}
