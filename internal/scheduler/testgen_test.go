package scheduler

import (
	"context"
	"testing"

	"github.com/openaxiom/axiom/internal/state"
)

// recordingModelSelector captures the excludeFamily parameter for verification.
type recordingModelSelector struct {
	modelID       string
	modelFamily   string
	excludeCalls  []string // records excludeFamily values passed
}

func (m *recordingModelSelector) SelectModel(_ context.Context, _ state.TaskTier, excludeFamily string) (string, string, error) {
	m.excludeCalls = append(m.excludeCalls, excludeFamily)
	return m.modelID, m.modelFamily, nil
}

// stubFamilyExcluder returns a fixed exclude family for a given task.
type stubFamilyExcluder struct {
	families map[string]string // taskID → excludeFamily
}

func (f *stubFamilyExcluder) GetExcludeFamily(_ context.Context, taskID string) (string, error) {
	return f.families[taskID], nil
}

func seedTestTypeTask(t *testing.T, db *state.DB, runID, taskID, title string, tier state.TaskTier) {
	t.Helper()
	err := db.CreateTask(&state.Task{
		ID:       taskID,
		RunID:    runID,
		Title:    title,
		Status:   state.TaskQueued,
		Tier:     tier,
		TaskType: state.TaskTypeTest,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTick_TestTaskUsesExcludeFamily verifies that when the scheduler dispatches
// a test-type task, it passes the implementation's model family as excludeFamily
// to the ModelSelector. Per Architecture Section 11.5.
func TestTick_TestTaskUsesExcludeFamily(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// Create a test-type task
	seedTestTypeTask(t, db, runID, "test-1", "Test auth module", state.TierStandard)

	recorder := &recordingModelSelector{
		modelID:     "openai/gpt-5-mini",
		modelFamily: "openai",
	}

	sched := New(Options{
		DB:          db,
		Log:         testLogger(),
		MaxMeeseeks: 3,
		ModelSelector: recorder,
		SnapshotProvider: &mockSnapshotProvider{sha: "abc123"},
		FamilyExcluder: &stubFamilyExcluder{
			families: map[string]string{"test-1": "anthropic"},
		},
	})

	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	// Verify the task was dispatched
	task, _ := db.GetTask("test-1")
	if task.Status != state.TaskInProgress {
		t.Fatalf("expected in_progress, got %s", task.Status)
	}

	// Verify ModelSelector was called with correct excludeFamily
	if len(recorder.excludeCalls) != 1 {
		t.Fatalf("expected 1 SelectModel call, got %d", len(recorder.excludeCalls))
	}
	if recorder.excludeCalls[0] != "anthropic" {
		t.Errorf("expected excludeFamily 'anthropic', got %q", recorder.excludeCalls[0])
	}
}

// TestTick_ImplTaskUsesEmptyExcludeFamily verifies that implementation tasks
// do not pass any excludeFamily to the ModelSelector.
func TestTick_ImplTaskUsesEmptyExcludeFamily(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	seedTask(t, db, runID, "impl-1", "Implement auth", state.TierStandard)

	recorder := &recordingModelSelector{
		modelID:     "anthropic/claude-sonnet",
		modelFamily: "anthropic",
	}

	sched := New(Options{
		DB:          db,
		Log:         testLogger(),
		MaxMeeseeks: 3,
		ModelSelector: recorder,
		SnapshotProvider: &mockSnapshotProvider{sha: "abc123"},
		FamilyExcluder: &stubFamilyExcluder{
			families: map[string]string{}, // no exclusions for impl tasks
		},
	})

	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	if len(recorder.excludeCalls) != 1 {
		t.Fatalf("expected 1 SelectModel call, got %d", len(recorder.excludeCalls))
	}
	if recorder.excludeCalls[0] != "" {
		t.Errorf("expected empty excludeFamily for impl task, got %q", recorder.excludeCalls[0])
	}
}

// TestTick_NilFamilyExcluder verifies the scheduler works without a FamilyExcluder
// (backwards compatible — passes empty excludeFamily).
func TestTick_NilFamilyExcluder(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	seedTestTypeTask(t, db, runID, "test-1", "Test auth", state.TierStandard)

	recorder := &recordingModelSelector{
		modelID:     "openai/gpt-5-mini",
		modelFamily: "openai",
	}

	sched := New(Options{
		DB:               db,
		Log:              testLogger(),
		MaxMeeseeks:      3,
		ModelSelector:    recorder,
		SnapshotProvider: &mockSnapshotProvider{sha: "abc123"},
		// No FamilyExcluder — should default to empty excludeFamily
	})

	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	if len(recorder.excludeCalls) != 1 {
		t.Fatalf("expected 1 SelectModel call, got %d", len(recorder.excludeCalls))
	}
	if recorder.excludeCalls[0] != "" {
		t.Errorf("expected empty excludeFamily with nil FamilyExcluder, got %q", recorder.excludeCalls[0])
	}
}
