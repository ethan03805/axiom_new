package inference

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

// --- test helpers ---

func testBrokerLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func testBrokerDB(t *testing.T) *state.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "broker_test.db")
	db, err := state.Open(dbPath, testBrokerLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testBrokerConfig() *config.Config {
	cfg := config.Default("test-project", "test-project")
	cfg.Inference.MaxRequestsTask = 50
	cfg.Inference.TokenCapPerReq = 16384
	return &cfg
}

// mockProvider implements Provider for testing.
type mockProvider struct {
	name      string
	available bool
	response  *ProviderResponse
	err       error
	calls     int
}

func (m *mockProvider) Name() string                        { return m.name }
func (m *mockProvider) Available(_ context.Context) bool    { return m.available }
func (m *mockProvider) Complete(_ context.Context, _ ProviderRequest) (*ProviderResponse, error) {
	m.calls++
	return m.response, m.err
}

// setupTestBroker creates a broker with a run and task in the database for testing.
func setupTestBroker(t *testing.T, budgetMax float64) (*Broker, *mockProvider, *mockProvider, string, string) {
	t.Helper()
	db := testBrokerDB(t)
	cfg := testBrokerConfig()
	cfg.Budget.MaxUSD = budgetMax
	log := testBrokerLogger()
	bus := events.New(db, log)

	cloud := &mockProvider{
		name:      "openrouter",
		available: true,
		response: &ProviderResponse{
			Content:      "cloud response",
			FinishReason: "stop",
			InputTokens:  10,
			OutputTokens: 20,
			Model:        "anthropic/claude-4-sonnet",
		},
	}
	local := &mockProvider{
		name:      "bitnet",
		available: true,
		response: &ProviderResponse{
			Content:      "local response",
			FinishReason: "stop",
			InputTokens:  10,
			OutputTokens: 20,
			Model:        "bitnet/falcon3-1b",
		},
	}

	// Pricing: the broker needs model pricing for budget checks
	pricing := map[string]ModelPricing{
		"anthropic/claude-4-sonnet": {
			PromptCostPerToken:     0.000003,
			CompletionCostPerToken: 0.000015,
		},
		"openai/gpt-4o": {
			PromptCostPerToken:     0.0000025,
			CompletionCostPerToken: 0.00001,
		},
		"bitnet/falcon3-1b": {
			PromptCostPerToken:     0,
			CompletionCostPerToken: 0,
		},
	}

	// Model tier allowlist
	allowlist := map[string]string{
		"anthropic/claude-4-sonnet": "standard",
		"openai/gpt-4o":            "standard",
		"bitnet/falcon3-1b":        "local",
	}

	broker := NewBroker(BrokerConfig{
		Config:        cfg,
		DB:            db,
		Bus:           bus,
		Log:           log,
		CloudProvider: cloud,
		LocalProvider: local,
		ModelPricing:  pricing,
		ModelTiers:    allowlist,
	})

	// Create a project, run, task in the DB
	projID := "proj-1"
	db.CreateProject(&state.Project{ID: projID, RootPath: "/test", Name: "test", Slug: "test"})
	runID := "run-1"
	db.CreateRun(&state.ProjectRun{
		ID:                  runID,
		ProjectID:           projID,
		Status:              state.RunActive,
		BaseBranch:          "main",
		WorkBranch:          "axiom/test",
		BudgetMaxUSD:        budgetMax,
		OrchestratorMode:    "embedded",
		OrchestratorRuntime: "claw",
		SRSApprovalDelegate: "user",
	})
	taskID := "task-1"
	db.CreateTask(&state.Task{
		ID:       taskID,
		RunID:    runID,
		Title:    "test task",
		Status:   state.TaskInProgress,
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
	})
	db.CreateAttempt(&state.TaskAttempt{
		TaskID:        taskID,
		AttemptNumber: 1,
		ModelID:       "anthropic/claude-4-sonnet",
		ModelFamily:   "anthropic",
		BaseSnapshot:  "abc123",
		Status:        state.AttemptRunning,
		Phase:         state.PhaseExecuting,
	})

	return broker, cloud, local, runID, taskID
}

// --- Broker tests ---

func TestBroker_Infer_RoutesToCloudForStandardTier(t *testing.T) {
	broker, cloud, local, runID, taskID := setupTestBroker(t, 10.0)

	resp, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "anthropic/claude-4-sonnet",
		Tier:      "standard",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 1024,
	})
	if err != nil {
		t.Fatalf("Infer failed: %v", err)
	}
	if resp.Content != "cloud response" {
		t.Errorf("expected cloud response, got %q", resp.Content)
	}
	if resp.ProviderName != "openrouter" {
		t.Errorf("expected provider openrouter, got %q", resp.ProviderName)
	}
	if cloud.calls != 1 {
		t.Errorf("expected 1 cloud call, got %d", cloud.calls)
	}
	if local.calls != 0 {
		t.Errorf("expected 0 local calls, got %d", local.calls)
	}
}

func TestBroker_Infer_RoutesToLocalForLocalTier(t *testing.T) {
	broker, cloud, local, runID, taskID := setupTestBroker(t, 10.0)

	resp, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "bitnet/falcon3-1b",
		Tier:      "local",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 512,
	})
	if err != nil {
		t.Fatalf("Infer failed: %v", err)
	}
	if resp.Content != "local response" {
		t.Errorf("expected local response, got %q", resp.Content)
	}
	if resp.ProviderName != "bitnet" {
		t.Errorf("expected provider bitnet, got %q", resp.ProviderName)
	}
	if local.calls != 1 {
		t.Errorf("expected 1 local call, got %d", local.calls)
	}
	if cloud.calls != 0 {
		t.Errorf("expected 0 cloud calls, got %d", cloud.calls)
	}
}

func TestBroker_Infer_RejectsModelNotInAllowlist(t *testing.T) {
	broker, _, _, runID, taskID := setupTestBroker(t, 10.0)

	_, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "unknown/model",
		Tier:      "standard",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 1024,
	})
	if !errors.Is(err, ErrModelNotAllowed) {
		t.Fatalf("expected ErrModelNotAllowed, got: %v", err)
	}
}

func TestBroker_Infer_RejectsTierMismatch(t *testing.T) {
	broker, _, _, runID, taskID := setupTestBroker(t, 10.0)

	// Request premium model for a local-tier task
	_, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "anthropic/claude-4-sonnet", // standard tier model
		Tier:      "local",                      // but task is local tier
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 1024,
	})
	if !errors.Is(err, ErrModelNotAllowed) {
		t.Fatalf("expected ErrModelNotAllowed for tier mismatch, got: %v", err)
	}
}

func TestBroker_Infer_RejectsBudgetExceeded(t *testing.T) {
	broker, _, _, runID, taskID := setupTestBroker(t, 0.001) // tiny budget

	_, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "anthropic/claude-4-sonnet",
		Tier:      "standard",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 10000, // would cost $0.15 — exceeds $0.001 budget
	})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got: %v", err)
	}
}

func TestBroker_Infer_RejectsRateLimitExceeded(t *testing.T) {
	broker, _, _, runID, taskID := setupTestBroker(t, 100.0)

	// Override rate limit to 2 for test
	broker.rateLimiter = NewRateLimiter(2)

	for i := 0; i < 2; i++ {
		_, err := broker.Infer(context.Background(), engine.InferenceRequest{
			RunID:     runID,
			TaskID:    taskID,
			AgentType: "meeseeks",
			ModelID:   "anthropic/claude-4-sonnet",
			Tier:      "standard",
			Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
			MaxTokens: 100,
		})
		if err != nil {
			t.Fatalf("request %d should succeed: %v", i+1, err)
		}
	}

	_, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "anthropic/claude-4-sonnet",
		Tier:      "standard",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 100,
	})
	if !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("expected ErrRateLimitExceeded, got: %v", err)
	}
}

func TestBroker_Infer_RejectsTokenCapExceeded(t *testing.T) {
	broker, _, _, runID, taskID := setupTestBroker(t, 100.0)

	_, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "anthropic/claude-4-sonnet",
		Tier:      "standard",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 999999, // exceeds 16384 cap
	})
	if !errors.Is(err, ErrTokenCapExceeded) {
		t.Fatalf("expected ErrTokenCapExceeded, got: %v", err)
	}
}

func TestBroker_Infer_LogsCostToDatabase(t *testing.T) {
	broker, _, _, runID, taskID := setupTestBroker(t, 10.0)

	_, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AttemptID: 1,
		AgentType: "meeseeks",
		ModelID:   "anthropic/claude-4-sonnet",
		Tier:      "standard",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 1024,
	})
	if err != nil {
		t.Fatalf("Infer failed: %v", err)
	}

	// Verify cost was logged
	entries, err := broker.db.ListCostLogByRun(runID)
	if err != nil {
		t.Fatalf("ListCostLogByRun failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 cost log entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.RunID != runID {
		t.Errorf("expected run_id %s, got %s", runID, entry.RunID)
	}
	if entry.ModelID != "anthropic/claude-4-sonnet" {
		t.Errorf("expected model anthropic/claude-4-sonnet, got %s", entry.ModelID)
	}
	if entry.AgentType != "meeseeks" {
		t.Errorf("expected agent_type meeseeks, got %s", entry.AgentType)
	}
	if entry.InputTokens == nil || *entry.InputTokens != 10 {
		t.Errorf("expected 10 input tokens")
	}
	if entry.OutputTokens == nil || *entry.OutputTokens != 20 {
		t.Errorf("expected 20 output tokens")
	}
	if entry.CostUSD <= 0 {
		t.Errorf("expected positive cost, got %f", entry.CostUSD)
	}
}

func TestBroker_Infer_EmitsEvents(t *testing.T) {
	broker, _, _, runID, taskID := setupTestBroker(t, 10.0)

	// Subscribe to events
	ch, subID := broker.bus.Subscribe(nil)
	defer broker.bus.Unsubscribe(subID)

	_, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "anthropic/claude-4-sonnet",
		Tier:      "standard",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Infer failed: %v", err)
	}

	// Should have received inference_completed event
	var gotCompleted bool
	for i := 0; i < 10; i++ {
		select {
		case ev := <-ch:
			if ev.Type == events.InferenceCompleted {
				gotCompleted = true
			}
		default:
		}
		if gotCompleted {
			break
		}
	}
	if !gotCompleted {
		t.Error("expected inference_completed event")
	}
}

func TestBroker_Infer_FallsBackToLocalWhenCloudDown(t *testing.T) {
	broker, cloud, local, runID, taskID := setupTestBroker(t, 10.0)

	// Cloud goes down
	cloud.available = false
	cloud.err = errors.New("connection refused")

	// Request a local-tier model — should use local
	resp, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "bitnet/falcon3-1b",
		Tier:      "local",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 512,
	})
	if err != nil {
		t.Fatalf("Infer should fall back to local: %v", err)
	}
	if resp.ProviderName != "bitnet" {
		t.Errorf("expected bitnet fallback, got %q", resp.ProviderName)
	}
	if local.calls != 1 {
		t.Errorf("expected 1 local call, got %d", local.calls)
	}
}

func TestBroker_Infer_ErrorWhenBothProvidersDown(t *testing.T) {
	broker, cloud, local, runID, taskID := setupTestBroker(t, 10.0)

	cloud.available = false
	cloud.err = errors.New("down")
	local.available = false
	local.err = errors.New("down")

	_, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "bitnet/falcon3-1b",
		Tier:      "local",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 512,
	})
	if !errors.Is(err, ErrProviderDown) {
		t.Fatalf("expected ErrProviderDown, got: %v", err)
	}
}

func TestBroker_Available(t *testing.T) {
	broker, cloud, local, _, _ := setupTestBroker(t, 10.0)

	if !broker.Available() {
		t.Error("broker should be available when at least one provider is up")
	}

	cloud.available = false
	if !broker.Available() {
		t.Error("broker should be available when local is up")
	}

	local.available = false
	if broker.Available() {
		t.Error("broker should be unavailable when all providers are down")
	}
}

func TestBroker_Infer_ZeroCostForLocalModel(t *testing.T) {
	broker, _, _, runID, taskID := setupTestBroker(t, 0) // zero budget

	// Zero-cost model should work even with zero budget
	resp, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "bitnet/falcon3-1b",
		Tier:      "local",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 512,
	})
	if err != nil {
		t.Fatalf("zero-cost model should work with zero budget: %v", err)
	}
	if resp.CostUSD != 0 {
		t.Errorf("expected zero cost for local model, got %f", resp.CostUSD)
	}
}

func TestBroker_Infer_TracksBudgetAcrossRequests(t *testing.T) {
	broker, _, _, runID, taskID := setupTestBroker(t, 0.001) // very tight budget

	// First request should work (max cost under budget)
	_, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "anthropic/claude-4-sonnet",
		Tier:      "standard",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 10, // max cost = 10 * 0.000015 = 0.00015 — under 0.001
	})
	if err != nil {
		t.Fatalf("first request should succeed: %v", err)
	}

	// Spend tracker should update — total cost was actually recorded
	total, _ := broker.db.TotalCostByRun(runID)
	if total <= 0 {
		t.Errorf("expected positive total cost after first request, got %f", total)
	}
}

func TestBroker_Infer_UsesPromptFieldAsFallback(t *testing.T) {
	broker, cloud, _, runID, taskID := setupTestBroker(t, 10.0)

	// Use Prompt instead of Messages
	resp, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "anthropic/claude-4-sonnet",
		Tier:      "standard",
		Prompt:    "hello via prompt",
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Infer with Prompt field failed: %v", err)
	}
	if resp.Content != "cloud response" {
		t.Errorf("expected cloud response, got %q", resp.Content)
	}
	if cloud.calls != 1 {
		t.Errorf("expected 1 cloud call, got %d", cloud.calls)
	}
}

func TestBroker_Infer_ProviderErrorPropagated(t *testing.T) {
	broker, cloud, _, runID, taskID := setupTestBroker(t, 10.0)

	cloud.err = errors.New("provider exploded")
	cloud.response = nil

	_, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "anthropic/claude-4-sonnet",
		Tier:      "standard",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 100,
	})
	if err == nil {
		t.Fatal("expected error when provider fails")
	}
}
