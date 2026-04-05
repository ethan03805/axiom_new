package inference

import "sync"

// BudgetEnforcer tracks inference spend against a ceiling and provides
// pre-authorization checks per Architecture Section 21.3.
// It is goroutine-safe.
type BudgetEnforcer struct {
	mu          sync.Mutex
	maxUSD      float64
	warnPercent int
	spent       float64
}

// NewBudgetEnforcer creates a BudgetEnforcer with a ceiling and warning threshold.
func NewBudgetEnforcer(maxUSD float64, warnPercent int) *BudgetEnforcer {
	return &BudgetEnforcer{
		maxUSD:      maxUSD,
		warnPercent: warnPercent,
	}
}

// Authorize checks whether a request with the given max_tokens and pricing
// can proceed without exceeding the remaining budget.
// Per Architecture Section 21.3: the engine calculates maximum possible cost
// (max_tokens * completion pricing) and verifies it fits within remaining budget.
// Zero-cost models (BitNet) are always authorized.
func (b *BudgetEnforcer) Authorize(maxTokens int, pricing ModelPricing) error {
	maxCost := float64(maxTokens) * pricing.CompletionCostPerToken
	if maxCost == 0 {
		return nil
	}

	b.mu.Lock()
	remaining := b.maxUSD - b.spent
	b.mu.Unlock()

	if maxCost > remaining {
		return ErrBudgetExceeded
	}
	return nil
}

// Record adds a completed request's actual cost to the running total.
func (b *BudgetEnforcer) Record(costUSD float64) {
	b.mu.Lock()
	b.spent += costUSD
	b.mu.Unlock()
}

// Remaining returns the remaining budget in USD.
func (b *BudgetEnforcer) Remaining() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.maxUSD - b.spent
	if r < 0 {
		return 0
	}
	return r
}

// Spent returns the total spend so far.
func (b *BudgetEnforcer) Spent() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent
}

// WarnReached returns true if spend has reached or exceeded the warning threshold.
func (b *BudgetEnforcer) WarnReached() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.maxUSD == 0 {
		return false
	}
	threshold := b.maxUSD * float64(b.warnPercent) / 100.0
	return b.spent >= threshold
}

// Exceeded returns true if spend has exceeded the budget ceiling.
func (b *BudgetEnforcer) Exceeded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent > b.maxUSD
}
