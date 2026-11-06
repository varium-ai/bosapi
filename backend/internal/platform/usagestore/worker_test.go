package usagestore

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestWorkerThrottlesAndJoinsBothTasks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		began := time.Now()
		var moves, maintenance []time.Duration
		stop := start(func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok || deadline.Sub(time.Now()) != 2*time.Second {
				t.Error("move deadline missing")
			}
			moves = append(moves, time.Since(began))
			return nil
		}, func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok || deadline.Sub(time.Now()) != 3*time.Second {
				t.Error("maintenance deadline missing")
			}
			maintenance = append(maintenance, time.Since(began))
			return nil
		})
		time.Sleep(6250 * time.Millisecond)
		synctest.Wait()
		stop()
		stop()
		if !reflect.DeepEqual(moves, []time.Duration{5 * time.Second, 5500 * time.Millisecond, 6 * time.Second}) ||
			!reflect.DeepEqual(maintenance, []time.Duration{5 * time.Second}) {
			t.Fatalf("moves=%v maintenance=%v", moves, maintenance)
		}
	})
}

func TestWorkerShutdownCancelsBothOperations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, 2)
		exited := make(chan struct{}, 2)
		run := func(ctx context.Context) error {
			entered <- struct{}{}
			<-ctx.Done()
			exited <- struct{}{}
			return ctx.Err()
		}
		stop := start(run, run)
		<-entered
		<-entered
		stop()
		if len(exited) != 2 {
			t.Fatal("shutdown did not join both transactions")
		}
	})
}
