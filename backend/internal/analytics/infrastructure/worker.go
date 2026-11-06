package infrastructure

import (
	"context"
	"sync"
	"time"

	"bosapi/backend/internal/analytics/application"
	"bosapi/backend/internal/platform/logging"
)

type Worker struct {
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
	service *application.Service
}

func NewWorker(service *application.Service) *Worker {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Worker{cancel: cancel, done: make(chan struct{}), service: service}
	go w.run(ctx)
	return w
}

func (w *Worker) run(ctx context.Context) {
	defer close(w.done)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			_ = w.service.Refresh(ctx)
			_ = w.service.Cleanup(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var lastLog time.Time
	for {
		err := w.service.Generate(ctx)
		if sendErr := w.service.Deliver(ctx); err == nil {
			err = sendErr
		}
		if err != nil && ctx.Err() == nil && time.Since(lastLog) >= time.Minute {
			logging.Event(ctx, "WARN", "usage_reporting.worker_failed", "usage_reporting_operation_failed")
			lastLog = time.Now()
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) Close() {
	w.once.Do(func() { w.cancel(); w.service.Close() })
	<-w.done
}
