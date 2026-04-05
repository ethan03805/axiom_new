package events

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openaxiom/axiom/internal/state"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func testDB(t *testing.T) *state.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := state.Open(dbPath, testLogger())
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

func seedProject(t *testing.T, db *state.DB) string {
	t.Helper()
	id := "proj-test"
	err := db.CreateProject(&state.Project{
		ID:       id,
		RootPath: "/tmp/test-project",
		Name:     "test-project",
		Slug:     "test-project",
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func seedRun(t *testing.T, db *state.DB, projectID string) string {
	t.Helper()
	id := "run-test"
	err := db.CreateRun(&state.ProjectRun{
		ID:                  id,
		ProjectID:           projectID,
		Status:              state.RunActive,
		BaseBranch:          "main",
		WorkBranch:          "axiom/test-project",
		OrchestratorMode:    "embedded",
		OrchestratorRuntime: "claw",
		SRSApprovalDelegate: "user",
		BudgetMaxUSD:        10.0,
		ConfigSnapshot:      "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNewBus(t *testing.T) {
	db := testDB(t)
	log := testLogger()

	bus := New(db, log)
	if bus == nil {
		t.Fatal("expected non-nil bus")
	}
}

func TestPublish_PersistsToDatabase(t *testing.T) {
	db := testDB(t)
	log := testLogger()
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	bus := New(db, log)

	err := bus.Publish(EngineEvent{
		Type:  RunStarted,
		RunID: runID,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Verify event was persisted to SQLite
	events, err := db.ListEventsByRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventType != string(RunStarted) {
		t.Errorf("event type = %q, want %q", events[0].EventType, RunStarted)
	}
}

func TestPublish_FansOutToSubscribers(t *testing.T) {
	db := testDB(t)
	log := testLogger()
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	bus := New(db, log)

	// Subscribe
	ch, id := bus.Subscribe(nil)
	defer bus.Unsubscribe(id)

	err := bus.Publish(EngineEvent{
		Type:  RunStarted,
		RunID: runID,
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Type != RunStarted {
			t.Errorf("event type = %q, want %q", ev.Type, RunStarted)
		}
		if ev.RunID != runID {
			t.Errorf("run ID = %q, want %q", ev.RunID, runID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestPublish_FilteredSubscriber(t *testing.T) {
	db := testDB(t)
	log := testLogger()
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	bus := New(db, log)

	// Subscribe only to task events
	filter := func(e EngineEvent) bool {
		return e.Type == TaskStarted
	}
	ch, id := bus.Subscribe(filter)
	defer bus.Unsubscribe(id)

	// Publish a run event (should be filtered out)
	err := bus.Publish(EngineEvent{
		Type:  RunStarted,
		RunID: runID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Publish a task event (should pass through)
	taskID := "task-1"
	err = bus.Publish(EngineEvent{
		Type:   TaskStarted,
		RunID:  runID,
		TaskID: taskID,
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Type != TaskStarted {
			t.Errorf("event type = %q, want %q", ev.Type, TaskStarted)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task event")
	}

	// Verify filtered event was NOT received
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event: %v", ev)
	case <-time.After(50 * time.Millisecond):
		// expected: no more events
	}
}

func TestMultipleSubscribers(t *testing.T) {
	db := testDB(t)
	log := testLogger()
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	bus := New(db, log)

	ch1, id1 := bus.Subscribe(nil)
	defer bus.Unsubscribe(id1)
	ch2, id2 := bus.Subscribe(nil)
	defer bus.Unsubscribe(id2)

	err := bus.Publish(EngineEvent{
		Type:  RunStarted,
		RunID: runID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Both subscribers should receive the event
	for i, ch := range []<-chan EngineEvent{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Type != RunStarted {
				t.Errorf("subscriber %d: event type = %q, want %q", i, ev.Type, RunStarted)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out", i)
		}
	}
}

func TestUnsubscribe(t *testing.T) {
	db := testDB(t)
	log := testLogger()
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	bus := New(db, log)

	ch, id := bus.Subscribe(nil)
	bus.Unsubscribe(id)

	err := bus.Publish(EngineEvent{
		Type:  RunStarted,
		RunID: runID,
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		t.Fatalf("should not receive event after unsubscribe, got: %v", ev)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestPublish_WithDetails(t *testing.T) {
	db := testDB(t)
	log := testLogger()
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	bus := New(db, log)

	taskID := "task-1"
	agentType := "meeseeks"
	err := bus.Publish(EngineEvent{
		Type:      TaskStarted,
		RunID:     runID,
		TaskID:    taskID,
		AgentType: agentType,
		Details:   map[string]any{"model": "anthropic/claude-4-sonnet"},
	})
	if err != nil {
		t.Fatal(err)
	}

	events, err := db.ListEventsByRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].TaskID == nil || *events[0].TaskID != taskID {
		t.Errorf("task ID mismatch")
	}
	if events[0].AgentType == nil || *events[0].AgentType != agentType {
		t.Errorf("agent type mismatch")
	}
	if events[0].Details == nil {
		t.Error("expected details to be persisted")
	}
}

func TestPublish_ViewModelEvent_NotPersisted(t *testing.T) {
	db := testDB(t)
	log := testLogger()
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	bus := New(db, log)

	ch, id := bus.Subscribe(nil)
	defer bus.Unsubscribe(id)

	// View-model events (TUI-specific) should be fanned out but NOT persisted to SQLite
	err := bus.Publish(EngineEvent{
		Type:  TaskProjectionUpdated,
		RunID: runID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should still be received by subscriber
	select {
	case ev := <-ch:
		if ev.Type != TaskProjectionUpdated {
			t.Errorf("type = %q, want %q", ev.Type, TaskProjectionUpdated)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	// Should NOT be in the database
	events, err := db.ListEventsByRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("view-model events should not be persisted, got %d events", len(events))
	}
}

func TestPublish_ConcurrentSafety(t *testing.T) {
	db := testDB(t)
	log := testLogger()
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	bus := New(db, log)

	ch, id := bus.Subscribe(nil)
	defer bus.Unsubscribe(id)

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			bus.Publish(EngineEvent{
				Type:  RunStarted,
				RunID: runID,
			})
		}()
	}
	wg.Wait()

	// Drain subscriber channel
	received := 0
	timeout := time.After(2 * time.Second)
	for received < n {
		select {
		case <-ch:
			received++
		case <-timeout:
			t.Fatalf("received only %d/%d events", received, n)
		}
	}

	// All should be persisted
	events, err := db.ListEventsByRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != n {
		t.Errorf("expected %d persisted events, got %d", n, len(events))
	}
}

func TestEventTypeConstants(t *testing.T) {
	// Verify key event types exist and have expected string values
	types := map[EventType]string{
		RunCreated:             "run_created",
		RunStarted:             "run_started",
		RunPaused:              "run_paused",
		RunResumed:             "run_resumed",
		RunCancelled:           "run_cancelled",
		RunCompleted:           "run_completed",
		RunError:               "run_error",
		TaskCreated:            "task_created",
		TaskStarted:            "task_started",
		TaskCompleted:          "task_completed",
		TaskFailed:             "task_failed",
		TaskBlocked:            "task_blocked",
		StartupSummary:         "startup_summary",
		SessionModeChanged:     "session_mode_changed",
		TaskProjectionUpdated:  "task_projection_updated",
		ApprovalRequested:      "approval_requested",
		ApprovalResolved:       "approval_resolved",
		DiffPreviewReady:       "diff_preview_ready",
		TranscriptCompacted:    "transcript_compacted",
		PromptSuggestion:       "prompt_suggestion",
	}
	for et, want := range types {
		if string(et) != want {
			t.Errorf("EventType %q != %q", et, want)
		}
	}
}

func TestIsViewModelEvent(t *testing.T) {
	viewModel := []EventType{
		StartupSummary, SessionModeChanged, PromptSuggestion,
		TaskProjectionUpdated, ApprovalRequested, ApprovalResolved,
		DiffPreviewReady, TranscriptCompacted,
	}
	for _, et := range viewModel {
		if !IsViewModelEvent(et) {
			t.Errorf("%q should be a view-model event", et)
		}
	}

	persisted := []EventType{
		RunCreated, RunStarted, RunPaused, TaskCreated, TaskStarted,
	}
	for _, et := range persisted {
		if IsViewModelEvent(et) {
			t.Errorf("%q should NOT be a view-model event", et)
		}
	}
}
