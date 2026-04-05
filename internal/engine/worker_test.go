package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPool_RegisterAndStart(t *testing.T) {
	log := testLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wp := NewWorkerPool(ctx, log)

	var count atomic.Int32
	wp.Register("test-worker", func(ctx context.Context) error {
		count.Add(1)
		return nil
	}, 10*time.Millisecond)

	wp.Start()
	time.Sleep(60 * time.Millisecond)
	wp.Stop()

	got := count.Load()
	if got < 2 {
		t.Errorf("worker ran %d times, expected at least 2", got)
	}
}

func TestWorkerPool_StopCancelsWorkers(t *testing.T) {
	log := testLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wp := NewWorkerPool(ctx, log)

	var running atomic.Bool
	wp.Register("blocking-worker", func(ctx context.Context) error {
		running.Store(true)
		<-ctx.Done()
		running.Store(false)
		return nil
	}, time.Hour) // long interval, runs once immediately

	wp.Start()
	time.Sleep(20 * time.Millisecond) // let worker start

	wp.Stop()
	time.Sleep(20 * time.Millisecond) // let goroutines clean up

	if running.Load() {
		t.Error("worker should have stopped after pool Stop()")
	}
}

func TestWorkerPool_MultipleWorkers(t *testing.T) {
	log := testLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wp := NewWorkerPool(ctx, log)

	var countA, countB atomic.Int32
	wp.Register("worker-a", func(ctx context.Context) error {
		countA.Add(1)
		return nil
	}, 10*time.Millisecond)

	wp.Register("worker-b", func(ctx context.Context) error {
		countB.Add(1)
		return nil
	}, 10*time.Millisecond)

	wp.Start()
	time.Sleep(60 * time.Millisecond)
	wp.Stop()

	if countA.Load() < 2 {
		t.Errorf("worker-a ran %d times, expected at least 2", countA.Load())
	}
	if countB.Load() < 2 {
		t.Errorf("worker-b ran %d times, expected at least 2", countB.Load())
	}
}

func TestWorkerPool_StopIdempotent(t *testing.T) {
	log := testLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wp := NewWorkerPool(ctx, log)
	wp.Register("noop", func(ctx context.Context) error { return nil }, time.Second)
	wp.Start()
	wp.Stop()
	wp.Stop() // should not panic
}

func TestWorkerPool_ErrorDoesNotCrash(t *testing.T) {
	log := testLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wp := NewWorkerPool(ctx, log)

	var count atomic.Int32
	wp.Register("failing-worker", func(ctx context.Context) error {
		count.Add(1)
		return context.DeadlineExceeded // simulate error
	}, 10*time.Millisecond)

	wp.Start()
	time.Sleep(60 * time.Millisecond)
	wp.Stop()

	// Worker should keep running despite errors
	if count.Load() < 2 {
		t.Errorf("worker ran %d times despite errors, expected at least 2", count.Load())
	}
}
