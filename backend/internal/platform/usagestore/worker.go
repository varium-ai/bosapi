package usagestore

import (
	"context"
	"sync"
	"time"

	"bosapi/backend/internal/platform/logging"
	"gorm.io/gorm"
)

// Start runs bounded legacy moves and daily-partition maintenance independently
// of HTTP startup. Shutdown cancels and joins every outstanding operation.
func Start(database *gorm.DB) func() {
	return start(func(ctx context.Context) error {
		_, err := MoveBatch(ctx, database)
		return err
	}, func(ctx context.Context) error {
		_, err := Maintain(ctx, database, time.Now().UTC())
		return err
	})
}

func start(move, maintain func(context.Context) error) func() {
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	for _, task := range []struct {
		name              string
		run               func(context.Context) error
		timeout, interval time.Duration
	}{
		{"move", move, 2 * time.Second, 500 * time.Millisecond},
		{"partitions", maintain, 3 * time.Second, time.Hour},
	} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			delay := 5 * time.Second
			for {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				operation, done := context.WithTimeout(ctx, task.timeout)
				err := task.run(operation)
				done()
				delay = task.interval
				if err != nil && ctx.Err() == nil {
					logging.Printf(ctx, "ERROR", "usage_logs."+task.name+".failed", "err=%v", err)
					delay = 5 * time.Second
				}
			}
		}()
	}
	var once sync.Once
	return func() { once.Do(func() { cancel(); workers.Wait() }) }
}
