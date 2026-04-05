package inference

import "testing"

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	rl := NewRateLimiter(50)

	for i := 0; i < 50; i++ {
		if err := rl.Allow("task-1"); err != nil {
			t.Fatalf("request %d should be allowed: %v", i+1, err)
		}
	}
}

func TestRateLimiter_RejectsOverLimit(t *testing.T) {
	rl := NewRateLimiter(3)

	for i := 0; i < 3; i++ {
		if err := rl.Allow("task-1"); err != nil {
			t.Fatalf("request %d should be allowed: %v", i+1, err)
		}
	}

	if err := rl.Allow("task-1"); err != ErrRateLimitExceeded {
		t.Fatalf("expected ErrRateLimitExceeded, got: %v", err)
	}
}

func TestRateLimiter_IndependentTasks(t *testing.T) {
	rl := NewRateLimiter(2)

	// Fill up task-1
	rl.Allow("task-1")
	rl.Allow("task-1")
	if err := rl.Allow("task-1"); err != ErrRateLimitExceeded {
		t.Fatalf("task-1 should be limited: %v", err)
	}

	// task-2 should still work
	if err := rl.Allow("task-2"); err != nil {
		t.Fatalf("task-2 should be independent: %v", err)
	}
}

func TestRateLimiter_Count(t *testing.T) {
	rl := NewRateLimiter(50)

	rl.Allow("task-1")
	rl.Allow("task-1")
	rl.Allow("task-1")

	if c := rl.Count("task-1"); c != 3 {
		t.Fatalf("expected count 3, got %d", c)
	}

	if c := rl.Count("task-unknown"); c != 0 {
		t.Fatalf("expected count 0 for unknown task, got %d", c)
	}
}

func TestRateLimiter_Reset(t *testing.T) {
	rl := NewRateLimiter(2)

	rl.Allow("task-1")
	rl.Allow("task-1")
	// Now at limit

	rl.Reset("task-1")

	if err := rl.Allow("task-1"); err != nil {
		t.Fatalf("should be allowed after reset: %v", err)
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(1000)
	done := make(chan struct{})

	for i := 0; i < 100; i++ {
		go func() {
			rl.Allow("task-1")
			done <- struct{}{}
		}()
	}

	for i := 0; i < 100; i++ {
		<-done
	}

	if c := rl.Count("task-1"); c != 100 {
		t.Fatalf("expected 100 after concurrent access, got %d", c)
	}
}
