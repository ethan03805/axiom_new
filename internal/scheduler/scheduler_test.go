package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/openaxiom/axiom/internal/state"
)

// --- test helpers ---

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
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
	_, err := db.Exec(`INSERT INTO projects (id, root_path, name, slug) VALUES (?, ?, ?, ?)`,
		id, "/tmp/test-project", "test-project", "test-project")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func seedRun(t *testing.T, db *state.DB, projectID string) string {
	t.Helper()
	id := "run-test"
	_, err := db.Exec(`INSERT INTO project_runs
		(id, project_id, status, base_branch, work_branch, orchestrator_mode,
		 orchestrator_runtime, srs_approval_delegate, budget_max_usd, config_snapshot)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, projectID, string(state.RunActive), "main", "axiom/test-project",
		"embedded", "claw", "user", 10.0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func seedTask(t *testing.T, db *state.DB, runID, taskID, title string, tier state.TaskTier) {
	t.Helper()
	err := db.CreateTask(&state.Task{
		ID:       taskID,
		RunID:    runID,
		Title:    title,
		Status:   state.TaskQueued,
		Tier:     tier,
		TaskType: state.TaskTypeImplementation,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seedTaskWithTargetFiles(t *testing.T, db *state.DB, runID, taskID, title string, tier state.TaskTier, files []state.TaskTargetFile) {
	t.Helper()
	seedTask(t, db, runID, taskID, title, tier)
	for _, f := range files {
		f.TaskID = taskID
		if err := db.AddTaskTargetFile(&f); err != nil {
			t.Fatal(err)
		}
	}
}

// mockModelSelector is a test double for the ModelSelector interface.
type mockModelSelector struct {
	modelID     string
	modelFamily string
	err         error
}

func (m *mockModelSelector) SelectModel(_ context.Context, _ state.TaskTier, _ string) (string, string, error) {
	return m.modelID, m.modelFamily, m.err
}

// mockSnapshotProvider is a test double for the SnapshotProvider interface.
type mockSnapshotProvider struct {
	sha string
	err error
}

func (m *mockSnapshotProvider) CurrentHEAD() (string, error) {
	return m.sha, m.err
}

func testScheduler(t *testing.T, db *state.DB) *Scheduler {
	t.Helper()
	return New(Options{
		DB:          db,
		Log:         testLogger(),
		MaxMeeseeks: 3,
		ModelSelector: &mockModelSelector{
			modelID:     "test/standard-model",
			modelFamily: "test",
		},
		SnapshotProvider: &mockSnapshotProvider{
			sha: "abc123def456",
		},
	})
}

// --- Tick tests ---

func TestTick_DispatchesReadyTask(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedTask(t, db, runID, "task-1", "Build auth", state.TierStandard)
	sched := testScheduler(t, db)

	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	task, err := db.GetTask("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != state.TaskInProgress {
		t.Errorf("expected in_progress, got %s", task.Status)
	}

	// Verify attempt was created
	attempts, err := db.ListAttemptsByTask("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(attempts))
	}
	if attempts[0].AttemptNumber != 1 {
		t.Errorf("expected attempt_number 1, got %d", attempts[0].AttemptNumber)
	}
	if attempts[0].Status != state.AttemptRunning {
		t.Errorf("expected attempt running, got %s", attempts[0].Status)
	}
	if attempts[0].Phase != state.PhaseExecuting {
		t.Errorf("expected phase executing, got %s", attempts[0].Phase)
	}
	if attempts[0].BaseSnapshot != "abc123def456" {
		t.Errorf("expected base_snapshot abc123def456, got %s", attempts[0].BaseSnapshot)
	}
	if attempts[0].ModelID != "test/standard-model" {
		t.Errorf("expected model_id test/standard-model, got %s", attempts[0].ModelID)
	}
}

func TestTick_SkipsTasksWithUnfinishedDeps(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	seedTask(t, db, runID, "task-1", "Build auth", state.TierStandard)
	seedTask(t, db, runID, "task-2", "Build tests", state.TierStandard)
	if err := db.AddTaskDependency("task-2", "task-1"); err != nil {
		t.Fatal(err)
	}

	sched := testScheduler(t, db)
	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	// task-1 should be dispatched
	task1, _ := db.GetTask("task-1")
	if task1.Status != state.TaskInProgress {
		t.Errorf("expected task-1 in_progress, got %s", task1.Status)
	}

	// task-2 should still be queued (depends on task-1 which is not done)
	task2, _ := db.GetTask("task-2")
	if task2.Status != state.TaskQueued {
		t.Errorf("expected task-2 queued, got %s", task2.Status)
	}
}

func TestTick_DispatchesTaskWhenDepsAreDone(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	seedTask(t, db, runID, "task-1", "Build auth", state.TierStandard)
	seedTask(t, db, runID, "task-2", "Build tests", state.TierStandard)
	if err := db.AddTaskDependency("task-2", "task-1"); err != nil {
		t.Fatal(err)
	}

	// Mark task-1 as done
	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus("task-1", state.TaskDone); err != nil {
		t.Fatal(err)
	}

	sched := testScheduler(t, db)
	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	task2, _ := db.GetTask("task-2")
	if task2.Status != state.TaskInProgress {
		t.Errorf("expected task-2 in_progress (deps done), got %s", task2.Status)
	}
}

func TestTick_AcquiresLocksBeforeDispatch(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	seedTaskWithTargetFiles(t, db, runID, "task-1", "Build auth", state.TierStandard,
		[]state.TaskTargetFile{
			{FilePath: "pkg/auth/handler.go", LockScope: "file", LockResourceKey: "pkg/auth/handler.go"},
			{FilePath: "pkg/auth/middleware.go", LockScope: "file", LockResourceKey: "pkg/auth/middleware.go"},
		})

	sched := testScheduler(t, db)
	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	locks, err := db.GetTaskLocks("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 2 {
		t.Fatalf("expected 2 locks, got %d", len(locks))
	}
}

func TestTick_LockConflictMovesToWaitingOnLock(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// task-1 is already running and holds a lock
	seedTaskWithTargetFiles(t, db, runID, "task-1", "Build auth", state.TierStandard,
		[]state.TaskTargetFile{
			{FilePath: "pkg/auth/handler.go", LockScope: "file", LockResourceKey: "pkg/auth/handler.go"},
		})
	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.AcquireLock("file", "pkg/auth/handler.go", "task-1"); err != nil {
		t.Fatal(err)
	}

	// task-2 wants the same file
	seedTaskWithTargetFiles(t, db, runID, "task-2", "Update auth", state.TierStandard,
		[]state.TaskTargetFile{
			{FilePath: "pkg/auth/handler.go", LockScope: "file", LockResourceKey: "pkg/auth/handler.go"},
		})

	sched := testScheduler(t, db)
	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	task2, _ := db.GetTask("task-2")
	if task2.Status != state.TaskWaitingOnLock {
		t.Errorf("expected task-2 waiting_on_lock, got %s", task2.Status)
	}

	// Verify lock wait record
	waits, err := db.ListLockWaits(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 {
		t.Fatalf("expected 1 lock wait, got %d", len(waits))
	}
	if waits[0].TaskID != "task-2" {
		t.Errorf("expected lock wait for task-2, got %s", waits[0].TaskID)
	}
	if waits[0].WaitReason != "initial_dispatch" {
		t.Errorf("expected wait_reason initial_dispatch, got %s", waits[0].WaitReason)
	}
}

func TestTick_RespectsMaxMeeseeksLimit(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// Create 5 independent tasks, but max_meeseeks is 3
	for i := 1; i <= 5; i++ {
		seedTask(t, db, runID, taskID(i), taskTitle(i), state.TierStandard)
	}

	sched := testScheduler(t, db) // max_meeseeks = 3
	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	// Count in_progress tasks
	inProgress, err := db.ListTasksByStatus(runID, state.TaskInProgress)
	if err != nil {
		t.Fatal(err)
	}
	if len(inProgress) != 3 {
		t.Errorf("expected 3 in_progress tasks (max_meeseeks), got %d", len(inProgress))
	}

	// Remaining 2 should still be queued
	queued, err := db.ListTasksByStatus(runID, state.TaskQueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 2 {
		t.Errorf("expected 2 queued tasks, got %d", len(queued))
	}
}

func TestTick_CountsExistingInProgressTasks(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// 2 tasks already in_progress
	seedTask(t, db, runID, "task-1", "Task 1", state.TierStandard)
	seedTask(t, db, runID, "task-2", "Task 2", state.TierStandard)
	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus("task-2", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}

	// 3 more queued tasks, max_meeseeks = 3
	for i := 3; i <= 5; i++ {
		seedTask(t, db, runID, taskID(i), taskTitle(i), state.TierStandard)
	}

	sched := testScheduler(t, db) // max_meeseeks = 3
	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	// Only 1 more should be dispatched (3 - 2 existing = 1)
	inProgress, _ := db.ListTasksByStatus(runID, state.TaskInProgress)
	if len(inProgress) != 3 {
		t.Errorf("expected 3 total in_progress, got %d", len(inProgress))
	}
}

func TestTick_MultipleIndependentTasksDispatch(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// 3 independent tasks with non-overlapping files
	seedTaskWithTargetFiles(t, db, runID, "task-1", "Task 1", state.TierStandard,
		[]state.TaskTargetFile{{FilePath: "a.go", LockScope: "file", LockResourceKey: "a.go"}})
	seedTaskWithTargetFiles(t, db, runID, "task-2", "Task 2", state.TierStandard,
		[]state.TaskTargetFile{{FilePath: "b.go", LockScope: "file", LockResourceKey: "b.go"}})
	seedTaskWithTargetFiles(t, db, runID, "task-3", "Task 3", state.TierStandard,
		[]state.TaskTargetFile{{FilePath: "c.go", LockScope: "file", LockResourceKey: "c.go"}})

	sched := testScheduler(t, db)
	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	inProgress, _ := db.ListTasksByStatus(runID, state.TaskInProgress)
	if len(inProgress) != 3 {
		t.Errorf("expected 3 in_progress, got %d", len(inProgress))
	}
}

func TestTick_AtomicLockAcquisition_AllOrNothing(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// task-1 holds lock on file-b
	seedTask(t, db, runID, "task-1", "Task 1", state.TierStandard)
	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.AcquireLock("file", "file-b", "task-1"); err != nil {
		t.Fatal(err)
	}

	// task-2 needs both file-a and file-b — should fail atomically
	seedTaskWithTargetFiles(t, db, runID, "task-2", "Task 2", state.TierStandard,
		[]state.TaskTargetFile{
			{FilePath: "file-a", LockScope: "file", LockResourceKey: "file-a"},
			{FilePath: "file-b", LockScope: "file", LockResourceKey: "file-b"},
		})

	sched := testScheduler(t, db)
	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	// task-2 should be waiting_on_lock
	task2, _ := db.GetTask("task-2")
	if task2.Status != state.TaskWaitingOnLock {
		t.Errorf("expected waiting_on_lock, got %s", task2.Status)
	}

	// file-a should NOT be locked by task-2 (atomic = all or nothing)
	locks, _ := db.GetTaskLocks("task-2")
	if len(locks) != 0 {
		t.Errorf("expected 0 locks for task-2 (atomic failure), got %d", len(locks))
	}
}

// --- ProcessLockWaiters tests ---

func TestProcessLockWaiters_RequeuesWhenLockReleased(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// task-1 holds a lock, task-2 is waiting
	seedTaskWithTargetFiles(t, db, runID, "task-1", "Task 1", state.TierStandard,
		[]state.TaskTargetFile{{FilePath: "a.go", LockScope: "file", LockResourceKey: "a.go"}})
	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.AcquireLock("file", "a.go", "task-1"); err != nil {
		t.Fatal(err)
	}

	seedTaskWithTargetFiles(t, db, runID, "task-2", "Task 2", state.TierStandard,
		[]state.TaskTargetFile{{FilePath: "a.go", LockScope: "file", LockResourceKey: "a.go"}})
	if err := db.UpdateTaskStatus("task-2", state.TaskWaitingOnLock); err != nil {
		t.Fatal(err)
	}
	if err := db.AddLockWait(&state.TaskLockWait{
		TaskID:             "task-2",
		WaitReason:         "initial_dispatch",
		RequestedResources: `[{"resource_type":"file","resource_key":"a.go"}]`,
		BlockedByTaskID:    strPtr("task-1"),
	}); err != nil {
		t.Fatal(err)
	}

	// Release task-1's locks
	sched := testScheduler(t, db)
	err := sched.ReleaseLocks(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("ReleaseLocks failed: %v", err)
	}

	// task-2 should be requeued
	task2, _ := db.GetTask("task-2")
	if task2.Status != state.TaskQueued {
		t.Errorf("expected task-2 queued after lock release, got %s", task2.Status)
	}

	// Lock wait should be removed
	waits, _ := db.ListLockWaits(runID)
	if len(waits) != 0 {
		t.Errorf("expected 0 lock waits, got %d", len(waits))
	}
}

func TestProcessLockWaiters_StaysWaitingIfStillBlocked(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// task-1 holds lock on a.go, task-3 also holds lock on b.go
	seedTask(t, db, runID, "task-1", "Task 1", state.TierStandard)
	seedTask(t, db, runID, "task-3", "Task 3", state.TierStandard)
	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus("task-3", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.AcquireLock("file", "a.go", "task-1"); err != nil {
		t.Fatal(err)
	}
	if err := db.AcquireLock("file", "b.go", "task-3"); err != nil {
		t.Fatal(err)
	}

	// task-2 needs both a.go and b.go
	seedTaskWithTargetFiles(t, db, runID, "task-2", "Task 2", state.TierStandard,
		[]state.TaskTargetFile{
			{FilePath: "a.go", LockScope: "file", LockResourceKey: "a.go"},
			{FilePath: "b.go", LockScope: "file", LockResourceKey: "b.go"},
		})
	if err := db.UpdateTaskStatus("task-2", state.TaskWaitingOnLock); err != nil {
		t.Fatal(err)
	}
	if err := db.AddLockWait(&state.TaskLockWait{
		TaskID:             "task-2",
		WaitReason:         "initial_dispatch",
		RequestedResources: `[{"resource_type":"file","resource_key":"a.go"},{"resource_type":"file","resource_key":"b.go"}]`,
		BlockedByTaskID:    strPtr("task-1"),
	}); err != nil {
		t.Fatal(err)
	}

	// Release only task-1's locks (b.go still locked by task-3)
	sched := testScheduler(t, db)
	err := sched.ReleaseLocks(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("ReleaseLocks failed: %v", err)
	}

	// task-2 should still be waiting (b.go still locked)
	task2, _ := db.GetTask("task-2")
	if task2.Status != state.TaskWaitingOnLock {
		t.Errorf("expected task-2 still waiting_on_lock, got %s", task2.Status)
	}
}

// --- Lock ordering tests ---

func TestDeterministicLockOrder(t *testing.T) {
	// Verify sortLockRequests returns sorted order
	requests := []lockRequest{
		{ResourceType: "package", ResourceKey: "pkg/z"},
		{ResourceType: "file", ResourceKey: "b.go"},
		{ResourceType: "file", ResourceKey: "a.go"},
		{ResourceType: "package", ResourceKey: "pkg/a"},
	}

	sorted := sortLockRequests(requests)
	expected := []lockRequest{
		{ResourceType: "file", ResourceKey: "a.go"},
		{ResourceType: "file", ResourceKey: "b.go"},
		{ResourceType: "package", ResourceKey: "pkg/a"},
		{ResourceType: "package", ResourceKey: "pkg/z"},
	}

	if len(sorted) != len(expected) {
		t.Fatalf("expected %d entries, got %d", len(expected), len(sorted))
	}
	for i, s := range sorted {
		if s.ResourceType != expected[i].ResourceType || s.ResourceKey != expected[i].ResourceKey {
			t.Errorf("position %d: expected (%s, %s), got (%s, %s)",
				i, expected[i].ResourceType, expected[i].ResourceKey, s.ResourceType, s.ResourceKey)
		}
	}
}

// --- Tick with no active runs ---

func TestTick_NoActiveRuns(t *testing.T) {
	db := testDB(t)
	sched := testScheduler(t, db)

	// No runs at all — should be a no-op
	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick with no runs should not error: %v", err)
	}
}

func TestTick_PausedRunSkipped(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	seedTask(t, db, runID, "task-1", "Task 1", state.TierStandard)

	// Pause the run
	if err := db.UpdateRunStatus(runID, state.RunPaused); err != nil {
		t.Fatal(err)
	}

	sched := testScheduler(t, db)
	err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	// Task should remain queued
	task, _ := db.GetTask("task-1")
	if task.Status != state.TaskQueued {
		t.Errorf("expected queued (run paused), got %s", task.Status)
	}
}

// --- Attempt numbering ---

func TestTick_CorrectAttemptNumber(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	seedTask(t, db, runID, "task-1", "Task 1", state.TierStandard)

	// Add a previous failed attempt at the task's tier
	_, err := db.CreateAttempt(&state.TaskAttempt{
		TaskID: "task-1", AttemptNumber: 1, ModelID: "test/model",
		ModelFamily: "test", Tier: state.TierStandard, BaseSnapshot: "old",
		Status: state.AttemptFailed, Phase: state.PhaseFailed,
	})
	if err != nil {
		t.Fatal(err)
	}

	sched := testScheduler(t, db)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	attempts, _ := db.ListAttemptsByTask("task-1")
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if attempts[1].AttemptNumber != 2 {
		t.Errorf("expected attempt_number 2, got %d", attempts[1].AttemptNumber)
	}
}

// --- helpers ---

func taskID(n int) string {
	return fmt.Sprintf("task-%d", n)
}

func taskTitle(n int) string {
	return fmt.Sprintf("Task %d", n)
}

func strPtr(s string) *string {
	return &s
}
