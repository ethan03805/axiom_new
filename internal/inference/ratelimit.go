package inference

import "sync"

// RateLimiter enforces per-task request rate limits per Architecture Section 19.5.
// Default limit is 50 requests per task (configurable).
type RateLimiter struct {
	mu       sync.Mutex
	maxPerTask int
	counts   map[string]int
}

// NewRateLimiter creates a RateLimiter with the given per-task maximum.
func NewRateLimiter(maxPerTask int) *RateLimiter {
	return &RateLimiter{
		maxPerTask: maxPerTask,
		counts:     make(map[string]int),
	}
}

// Allow checks and increments the request count for a task.
// Returns ErrRateLimitExceeded if the task has exhausted its allowance.
func (rl *RateLimiter) Allow(taskID string) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.counts[taskID] >= rl.maxPerTask {
		return ErrRateLimitExceeded
	}
	rl.counts[taskID]++
	return nil
}

// Count returns the current request count for a task.
func (rl *RateLimiter) Count(taskID string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.counts[taskID]
}

// Reset clears the request count for a task (e.g. on retry with fresh container).
func (rl *RateLimiter) Reset(taskID string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.counts, taskID)
}
