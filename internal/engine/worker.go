package engine

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// WorkerFunc is a function executed periodically by the worker pool.
type WorkerFunc func(ctx context.Context) error

type workerEntry struct {
	name     string
	fn       WorkerFunc
	interval time.Duration
}

// WorkerPool manages background worker goroutines for the engine.
// Workers run periodically and are cancelled when the pool is stopped.
type WorkerPool struct {
	ctx     context.Context
	cancel  context.CancelFunc
	log     *slog.Logger
	workers []workerEntry
	wg      sync.WaitGroup
	once    sync.Once
}

// NewWorkerPool creates a new worker pool bound to the given context.
func NewWorkerPool(ctx context.Context, log *slog.Logger) *WorkerPool {
	poolCtx, cancel := context.WithCancel(ctx)
	return &WorkerPool{
		ctx:    poolCtx,
		cancel: cancel,
		log:    log,
	}
}

// Register adds a named worker that runs fn at the given interval.
// Must be called before Start.
func (wp *WorkerPool) Register(name string, fn WorkerFunc, interval time.Duration) {
	wp.workers = append(wp.workers, workerEntry{
		name:     name,
		fn:       fn,
		interval: interval,
	})
}

// Start launches all registered workers as goroutines.
func (wp *WorkerPool) Start() {
	for _, w := range wp.workers {
		wp.wg.Add(1)
		go wp.runWorker(w)
	}
}

// Stop cancels all workers and waits for them to finish.
// Safe to call multiple times.
func (wp *WorkerPool) Stop() {
	wp.once.Do(func() {
		wp.cancel()
	})
	wp.wg.Wait()
}

func (wp *WorkerPool) runWorker(w workerEntry) {
	defer wp.wg.Done()

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run immediately on start
	if err := w.fn(wp.ctx); err != nil {
		if wp.ctx.Err() == nil {
			wp.log.Warn("worker error", "worker", w.name, "error", err)
		}
	}

	for {
		select {
		case <-wp.ctx.Done():
			return
		case <-ticker.C:
			if err := w.fn(wp.ctx); err != nil {
				if wp.ctx.Err() == nil {
					wp.log.Warn("worker error", "worker", w.name, "error", err)
				}
			}
		}
	}
}
