package snapshot

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"github.com/redis/go-redis/v9"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestRedisSnapshotsSurviveRestartAndRejectLostLock(t *testing.T) {
	address := os.Getenv("BOS_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set BOS_TEST_REDIS_ADDR for Redis integration")
	}
	client := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1, ContextTimeoutEnabled: true})
	defer client.Close()
	ctx := context.Background()
	prefix := "test:usage-snapshot:" + rand.Text() + ":"
	defer func() {
		keys, _ := client.Keys(ctx, prefix+"*").Result()
		if len(keys) > 0 {
			client.Del(ctx, keys...)
		}
	}()
	var calls atomic.Int32
	load := func(context.Context) (json.RawMessage, time.Time, error) {
		calls.Add(1)
		return json.RawMessage(`{"requests":7}`), time.Time{}, nil
	}
	first := New(client, prefix, 20*time.Minute, 30*time.Minute, 2)
	v, err := first.Get(ctx, "a", false, load)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	second := New(client, prefix, 20*time.Minute, 30*time.Minute, 2)
	defer second.Close()
	shared, err := second.Get(ctx, "a", false, load)
	if err != nil || shared.ID != v.ID || calls.Load() != 1 {
		t.Fatalf("shared=%+v calls=%d err=%v", shared, calls.Load(), err)
	}
	losing := func(context.Context) (json.RawMessage, time.Time, error) {
		if err := client.Set(ctx, prefix+"b:lock", "new-owner", time.Second).Err(); err != nil {
			t.Fatal(err)
		}
		return json.RawMessage(`1`), time.Time{}, nil
	}
	_, err = second.Get(ctx, "b", false, losing)
	if err == nil {
		t.Fatal("expired owner published a snapshot")
	}
	if exists, _ := client.Exists(ctx, prefix+"b").Result(); exists != 0 {
		t.Fatal("stale writer persisted data")
	}
	owner, _ := client.Get(ctx, prefix+"b:lock").Result()
	if owner != "new-owner" {
		t.Fatal("old task released a new lock")
	}
	for _, key := range []string{"c", "d"} {
		if _, err := second.Get(ctx, key, false, load); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := client.ZCard(ctx, prefix+"index").Result(); n > 2 {
		t.Fatalf("unbounded Redis cache: %d", n)
	}
}
