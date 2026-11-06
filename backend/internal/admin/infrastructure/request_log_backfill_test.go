package infrastructure

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestRequestLogBackfillThrottlesAndStopsAfterCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		var calls []time.Duration
		stop := startRequestLogBackfill(func(ctx context.Context) (bool, error) {
			deadline, ok := ctx.Deadline()
			if !ok || deadline.Sub(time.Now()) != 2*time.Second {
				t.Error("each batch needs its own two-second deadline")
			}
			calls = append(calls, time.Since(started))
			return len(calls) == 3, nil
		})
		time.Sleep(10 * time.Second)
		synctest.Wait()
		stop()
		stop()
		if !reflect.DeepEqual(calls, []time.Duration{5 * time.Second, 5500 * time.Millisecond, 6 * time.Second}) {
			t.Fatalf("unexpected batch schedule: %v", calls)
		}
	})
}

func TestRequestLogBackfillShutdownCancelsCurrentBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		exited := make(chan struct{})
		stop := startRequestLogBackfill(func(ctx context.Context) (bool, error) {
			close(entered)
			<-ctx.Done()
			close(exited)
			return false, ctx.Err()
		})
		<-entered
		started := time.Now()
		stop()
		<-exited
		if time.Since(started) != 0 {
			t.Fatal("shutdown must cancel the batch immediately")
		}
	})
}
