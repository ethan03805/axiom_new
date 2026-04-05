package inference

import (
	"testing"
)

func TestBudgetEnforcer_Authorize_WithinBudget(t *testing.T) {
	be := NewBudgetEnforcer(10.0, 80) // $10 budget, warn at 80%

	err := be.Authorize(100, ModelPricing{
		PromptCostPerToken:     0.000003,
		CompletionCostPerToken: 0.000015,
	})
	if err != nil {
		t.Fatalf("expected authorization to succeed, got: %v", err)
	}
}

func TestBudgetEnforcer_Authorize_ExceedsBudget(t *testing.T) {
	be := NewBudgetEnforcer(0.001, 80) // very small budget

	err := be.Authorize(10000, ModelPricing{
		PromptCostPerToken:     0.000003,
		CompletionCostPerToken: 0.000015,
	})
	if err != ErrBudgetExceeded {
		t.Fatalf("expected ErrBudgetExceeded, got: %v", err)
	}
}

func TestBudgetEnforcer_Authorize_ExactlyAtBudget(t *testing.T) {
	be := NewBudgetEnforcer(0.0015, 80) // budget = exactly the max cost of 100 tokens

	// max_tokens * completion_cost = 100 * 0.000015 = 0.0015
	err := be.Authorize(100, ModelPricing{
		PromptCostPerToken:     0.000003,
		CompletionCostPerToken: 0.000015,
	})
	if err != nil {
		t.Fatalf("expected authorization at exact budget limit, got: %v", err)
	}
}

func TestBudgetEnforcer_RecordAndDrain(t *testing.T) {
	be := NewBudgetEnforcer(1.0, 80)

	be.Record(0.50) // spend half the budget

	// Now max cost of 0.60 exceeds remaining 0.50
	err := be.Authorize(40000, ModelPricing{
		PromptCostPerToken:     0.000003,
		CompletionCostPerToken: 0.000015,
	})
	if err != ErrBudgetExceeded {
		t.Fatalf("expected ErrBudgetExceeded after spending, got: %v", err)
	}
}

func TestBudgetEnforcer_Remaining(t *testing.T) {
	be := NewBudgetEnforcer(10.0, 80)
	be.Record(3.50)

	remaining := be.Remaining()
	if remaining != 6.50 {
		t.Fatalf("expected remaining 6.50, got %f", remaining)
	}
}

func TestBudgetEnforcer_Spent(t *testing.T) {
	be := NewBudgetEnforcer(10.0, 80)
	be.Record(1.25)
	be.Record(2.75)

	spent := be.Spent()
	if spent != 4.0 {
		t.Fatalf("expected spent 4.0, got %f", spent)
	}
}

func TestBudgetEnforcer_WarnReached(t *testing.T) {
	be := NewBudgetEnforcer(10.0, 80)

	if be.WarnReached() {
		t.Fatal("warn should not be reached at zero spend")
	}

	be.Record(7.99)
	if be.WarnReached() {
		t.Fatal("warn should not be reached at 79.9%")
	}

	be.Record(0.01)
	if !be.WarnReached() {
		t.Fatal("warn should be reached at 80%")
	}
}

func TestBudgetEnforcer_Exceeded(t *testing.T) {
	be := NewBudgetEnforcer(5.0, 80)

	if be.Exceeded() {
		t.Fatal("should not be exceeded at zero spend")
	}

	be.Record(5.01)
	if !be.Exceeded() {
		t.Fatal("should be exceeded when spend > max")
	}
}

func TestBudgetEnforcer_ZeroBudget_RejectsAll(t *testing.T) {
	be := NewBudgetEnforcer(0, 80)

	err := be.Authorize(1, ModelPricing{
		PromptCostPerToken:     0.000001,
		CompletionCostPerToken: 0.000001,
	})
	if err != ErrBudgetExceeded {
		t.Fatalf("expected ErrBudgetExceeded with zero budget, got: %v", err)
	}
}

func TestBudgetEnforcer_ZeroCostModel_Allowed(t *testing.T) {
	be := NewBudgetEnforcer(0, 80)

	// BitNet has zero cost
	err := be.Authorize(1000, ModelPricing{
		PromptCostPerToken:     0,
		CompletionCostPerToken: 0,
	})
	if err != nil {
		t.Fatalf("zero-cost model should be allowed even with zero budget, got: %v", err)
	}
}

func TestBudgetEnforcer_ConcurrentRecords(t *testing.T) {
	be := NewBudgetEnforcer(100.0, 80)
	done := make(chan struct{})

	for i := 0; i < 100; i++ {
		go func() {
			be.Record(0.10)
			done <- struct{}{}
		}()
	}

	for i := 0; i < 100; i++ {
		<-done
	}

	spent := be.Spent()
	expected := 10.0
	if spent < expected-0.01 || spent > expected+0.01 {
		t.Fatalf("expected ~%.2f spent after concurrent records, got %f", expected, spent)
	}
}
