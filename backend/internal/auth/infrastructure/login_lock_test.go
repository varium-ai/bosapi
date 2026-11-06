package infrastructure

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestLoginLockConcurrentFailuresShareRedisState(t *testing.T) {
	address := os.Getenv("BOS_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set BOS_TEST_REDIS_ADDR to run the shared Redis integration test")
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	defer client.Close()
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}
	store := NewLoginLockStore(client, "test:login-lock", 5, time.Minute, time.Minute)
	key := "test@example.com"
	if err := store.Unlock(context.Background(), key); err != nil {
		t.Fatalf("reset lock: %v", err)
	}

	var wg sync.WaitGroup
	for attempt := 0; attempt < 5; attempt++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := store.RecordFailure(context.Background(), key, time.Now())
			if err != nil {
				t.Errorf("record failure: %v", err)
			}
			_ = decision
		}()
	}
	wg.Wait()
	decision, err := store.Check(context.Background(), key, time.Now())
	if err != nil || !decision.Locked || decision.RetryAfter <= 0 {
		t.Fatalf("locked decision=%+v err=%v", decision, err)
	}
	if err := store.Unlock(context.Background(), key); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	decision, err = store.Check(context.Background(), key, time.Now())
	if err != nil || decision.Locked {
		t.Fatalf("unlocked decision=%+v err=%v", decision, err)
	}
}
