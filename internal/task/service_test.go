package task

import (
	"context"
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

func testService(t *testing.T) (*Service, *state.DB) {
	t.Helper()
	db := testDB(t)
	svc := New(db, testLogger())
	return svc, db
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

// --- CreateTask tests ---

func TestCreateTask_Single(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID:       "task-1",
		RunID:    runID,
		Title:    "Implement auth module",
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
	}

	task, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	if task.ID != "task-1" {
		t.Errorf("expected ID task-1, got %s", task.ID)
	}
	if task.Status != state.TaskQueued {
		t.Errorf("expected status queued, got %s", task.Status)
	}
	if task.Tier != state.TierStandard {
		t.Errorf("expected tier standard, got %s", task.Tier)
	}

	// Verify persisted
	got, err := db.GetTask("task-1")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if got.Title != "Implement auth module" {
		t.Errorf("expected title 'Implement auth module', got %q", got.Title)
	}
}

func TestCreateTask_WithSRSRefs(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID:       "task-1",
		RunID:    runID,
		Title:    "Implement auth",
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
		SRSRefs:  []string{"FR-001", "AC-003"},
	}

	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	refs, err := db.GetTaskSRSRefs("task-1")
	if err != nil {
		t.Fatalf("GetTaskSRSRefs failed: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 SRS refs, got %d", len(refs))
	}
}

func TestCreateTask_WithTargetFiles(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID:       "task-1",
		RunID:    runID,
		Title:    "Implement auth",
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
		TargetFiles: []TargetFileInput{
			{FilePath: "pkg/auth/handler.go", LockScope: "file", LockResourceKey: "pkg/auth/handler.go"},
			{FilePath: "pkg/auth/middleware.go", LockScope: "file", LockResourceKey: "pkg/auth/middleware.go"},
		},
	}

	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	files, err := db.GetTaskTargetFiles("task-1")
	if err != nil {
		t.Fatalf("GetTaskTargetFiles failed: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 target files, got %d", len(files))
	}
}

func TestCreateTask_MissingRunID(t *testing.T) {
	svc, _ := testService(t)

	input := CreateTaskInput{
		ID:       "task-1",
		Title:    "Missing run",
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
	}

	_, err := svc.CreateTask(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for missing run_id")
	}
}

func TestCreateTask_MissingTitle(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID:       "task-1",
		RunID:    runID,
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
	}

	_, err := svc.CreateTask(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for missing title")
	}
}

// --- CreateBatch tests ---

func TestCreateBatch_MultipleTasks(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	inputs := []CreateTaskInput{
		{ID: "task-1", RunID: runID, Title: "Task A", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation},
		{ID: "task-2", RunID: runID, Title: "Task B", Tier: state.TierCheap, TaskType: state.TaskTypeImplementation},
		{ID: "task-3", RunID: runID, Title: "Task C", Tier: state.TierLocal, TaskType: state.TaskTypeTest},
	}

	tasks, err := svc.CreateBatch(context.Background(), inputs)
	if err != nil {
		t.Fatalf("CreateBatch failed: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}

	// Verify all persisted
	all, err := db.ListTasksByRun(runID)
	if err != nil {
		t.Fatalf("ListTasksByRun failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 tasks in DB, got %d", len(all))
	}
}

func TestCreateBatch_WithDependencies(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	inputs := []CreateTaskInput{
		{ID: "task-1", RunID: runID, Title: "Task A", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation},
		{ID: "task-2", RunID: runID, Title: "Task B", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation, DependsOn: []string{"task-1"}},
		{ID: "task-3", RunID: runID, Title: "Task C", Tier: state.TierStandard, TaskType: state.TaskTypeTest, DependsOn: []string{"task-1", "task-2"}},
	}

	_, err := svc.CreateBatch(context.Background(), inputs)
	if err != nil {
		t.Fatalf("CreateBatch failed: %v", err)
	}

	// Verify dependencies
	deps, err := db.GetTaskDependencies("task-2")
	if err != nil {
		t.Fatalf("GetTaskDependencies failed: %v", err)
	}
	if len(deps) != 1 || deps[0] != "task-1" {
		t.Errorf("expected task-2 depends on [task-1], got %v", deps)
	}

	deps3, err := db.GetTaskDependencies("task-3")
	if err != nil {
		t.Fatalf("GetTaskDependencies failed: %v", err)
	}
	if len(deps3) != 2 {
		t.Errorf("expected task-3 depends on 2 tasks, got %d", len(deps3))
	}
}

func TestCreateBatch_RejectsCyclicDependencies(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// Direct cycle: A → B → A
	inputs := []CreateTaskInput{
		{ID: "task-1", RunID: runID, Title: "Task A", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation, DependsOn: []string{"task-2"}},
		{ID: "task-2", RunID: runID, Title: "Task B", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation, DependsOn: []string{"task-1"}},
	}

	_, err := svc.CreateBatch(context.Background(), inputs)
	if err == nil {
		t.Fatal("expected error for cyclic dependencies")
	}
}

func TestCreateBatch_RejectsTransitiveCycle(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// Transitive cycle: A → B → C → A
	inputs := []CreateTaskInput{
		{ID: "task-1", RunID: runID, Title: "Task A", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation, DependsOn: []string{"task-3"}},
		{ID: "task-2", RunID: runID, Title: "Task B", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation, DependsOn: []string{"task-1"}},
		{ID: "task-3", RunID: runID, Title: "Task C", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation, DependsOn: []string{"task-2"}},
	}

	_, err := svc.CreateBatch(context.Background(), inputs)
	if err == nil {
		t.Fatal("expected error for transitive cyclic dependencies")
	}
}

func TestCreateBatch_RejectsSelfDependency(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	inputs := []CreateTaskInput{
		{ID: "task-1", RunID: runID, Title: "Task A", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation, DependsOn: []string{"task-1"}},
	}

	_, err := svc.CreateBatch(context.Background(), inputs)
	if err == nil {
		t.Fatal("expected error for self-dependency")
	}
}

func TestCreateBatch_RejectsMissingDependency(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	inputs := []CreateTaskInput{
		{ID: "task-1", RunID: runID, Title: "Task A", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation, DependsOn: []string{"nonexistent"}},
	}

	_, err := svc.CreateBatch(context.Background(), inputs)
	if err == nil {
		t.Fatal("expected error for missing dependency")
	}
}

func TestCreateBatch_TransactionalRollbackOnError(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// Second task has a cycle, so entire batch should fail
	inputs := []CreateTaskInput{
		{ID: "task-1", RunID: runID, Title: "Task A", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation, DependsOn: []string{"task-2"}},
		{ID: "task-2", RunID: runID, Title: "Task B", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation, DependsOn: []string{"task-1"}},
	}

	_, err := svc.CreateBatch(context.Background(), inputs)
	if err == nil {
		t.Fatal("expected error")
	}

	// Verify no tasks were persisted
	all, err := db.ListTasksByRun(runID)
	if err != nil {
		t.Fatalf("ListTasksByRun failed: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected 0 tasks after rollback, got %d", len(all))
	}
}

// --- Retry tests ---

func TestRetryTask_RequeuesFailedTask(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// Create and fail a task
	input := CreateTaskInput{
		ID: "task-1", RunID: runID, Title: "Task A",
		Tier: state.TierStandard, TaskType: state.TaskTypeImplementation,
	}
	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	// Move to in_progress then failed
	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus("task-1", state.TaskFailed); err != nil {
		t.Fatal(err)
	}

	// Create an attempt to represent the failed attempt
	_, err = db.CreateAttempt(&state.TaskAttempt{
		TaskID:        "task-1",
		AttemptNumber: 1,
		ModelID:       "anthropic/claude-4-sonnet",
		ModelFamily:   "anthropic",
		Tier:          state.TierStandard,
		BaseSnapshot:  "abc123",
		Status:        state.AttemptFailed,
		Phase:         state.PhaseFailed,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = svc.RetryTask(context.Background(), "task-1", "Fix the compile error")
	if err != nil {
		t.Fatalf("RetryTask failed: %v", err)
	}

	task, err := db.GetTask("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != state.TaskQueued {
		t.Errorf("expected task status queued after retry, got %s", task.Status)
	}
}

func TestRetryTask_RejectsNonFailedTask(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID: "task-1", RunID: runID, Title: "Task A",
		Tier: state.TierStandard, TaskType: state.TaskTypeImplementation,
	}
	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	// Task is queued, not failed
	err = svc.RetryTask(context.Background(), "task-1", "feedback")
	if err == nil {
		t.Fatal("expected error retrying non-failed task")
	}
}

// --- Escalation tests ---

func TestEscalateTask_MovesToNextTier(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID: "task-1", RunID: runID, Title: "Task A",
		Tier: state.TierLocal, TaskType: state.TaskTypeImplementation,
	}
	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	// Fail the task
	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus("task-1", state.TaskFailed); err != nil {
		t.Fatal(err)
	}

	err = svc.EscalateTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("EscalateTask failed: %v", err)
	}

	task, err := db.GetTask("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Tier != state.TierCheap {
		t.Errorf("expected tier cheap after escalation from local, got %s", task.Tier)
	}
	if task.Status != state.TaskQueued {
		t.Errorf("expected status queued after escalation, got %s", task.Status)
	}
}

func TestEscalateTask_FullEscalationChain(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID: "task-1", RunID: runID, Title: "Task A",
		Tier: state.TierLocal, TaskType: state.TaskTypeImplementation,
	}
	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	// Escalation 1: local → cheap
	failAndEscalate := func() error {
		task, _ := db.GetTask("task-1")
		if task.Status == state.TaskQueued {
			if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
				return err
			}
		}
		if err := db.UpdateTaskStatus("task-1", state.TaskFailed); err != nil {
			return err
		}
		return svc.EscalateTask(context.Background(), "task-1")
	}

	if err := failAndEscalate(); err != nil {
		t.Fatalf("first escalation failed: %v", err)
	}
	task, _ := db.GetTask("task-1")
	if task.Tier != state.TierCheap {
		t.Errorf("expected cheap after 1st escalation, got %s", task.Tier)
	}

	// Escalation 2: cheap → standard
	if err := failAndEscalate(); err != nil {
		t.Fatalf("second escalation failed: %v", err)
	}
	task, _ = db.GetTask("task-1")
	if task.Tier != state.TierStandard {
		t.Errorf("expected standard after 2nd escalation, got %s", task.Tier)
	}
}

func TestEscalateTask_PremiumTierCannotEscalate(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID: "task-1", RunID: runID, Title: "Task A",
		Tier: state.TierPremium, TaskType: state.TaskTypeImplementation,
	}
	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus("task-1", state.TaskFailed); err != nil {
		t.Fatal(err)
	}

	err = svc.EscalateTask(context.Background(), "task-1")
	if err == nil {
		t.Fatal("expected error escalating premium tier task")
	}
}

// --- Block tests ---

func TestBlockTask_MarksAsBlocked(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID: "task-1", RunID: runID, Title: "Task A",
		Tier: state.TierStandard, TaskType: state.TaskTypeImplementation,
	}
	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus("task-1", state.TaskFailed); err != nil {
		t.Fatal(err)
	}

	// Requeue so we can go to in_progress → blocked
	if err := db.UpdateTaskStatus("task-1", state.TaskQueued); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}

	err = svc.BlockTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("BlockTask failed: %v", err)
	}

	task, err := db.GetTask("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != state.TaskBlocked {
		t.Errorf("expected blocked, got %s", task.Status)
	}
}

// --- HandleTaskFailure tests ---

func TestHandleTaskFailure_RetriesFirst(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID: "task-1", RunID: runID, Title: "Task A",
		Tier: state.TierStandard, TaskType: state.TaskTypeImplementation,
	}
	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	// First failure — should retry (< MaxRetriesPerTier)
	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}

	// Create a failed attempt at the task's tier
	_, err = db.CreateAttempt(&state.TaskAttempt{
		TaskID: "task-1", AttemptNumber: 1, ModelID: "test/model",
		ModelFamily: "test", Tier: state.TierStandard, BaseSnapshot: "abc",
		Status: state.AttemptFailed, Phase: state.PhaseFailed,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateTaskStatus("task-1", state.TaskFailed); err != nil {
		t.Fatal(err)
	}

	action, err := svc.HandleTaskFailure(context.Background(), "task-1", "compile error")
	if err != nil {
		t.Fatalf("HandleTaskFailure failed: %v", err)
	}
	if action != ActionRetry {
		t.Errorf("expected ActionRetry, got %d", action)
	}

	task, _ := db.GetTask("task-1")
	if task.Status != state.TaskQueued {
		t.Errorf("expected queued after retry, got %s", task.Status)
	}
}

func TestHandleTaskFailure_EscalatesAfterMaxRetries(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID: "task-1", RunID: runID, Title: "Task A",
		Tier: state.TierLocal, TaskType: state.TaskTypeImplementation,
	}
	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	// Create MaxRetriesPerTier failed attempts at the local tier
	for i := 1; i <= MaxRetriesPerTier; i++ {
		_, err = db.CreateAttempt(&state.TaskAttempt{
			TaskID: "task-1", AttemptNumber: i, ModelID: "local/model",
			ModelFamily: "local", Tier: state.TierLocal, BaseSnapshot: "abc",
			Status: state.AttemptFailed, Phase: state.PhaseFailed,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus("task-1", state.TaskFailed); err != nil {
		t.Fatal(err)
	}

	action, err := svc.HandleTaskFailure(context.Background(), "task-1", "repeated failure")
	if err != nil {
		t.Fatalf("HandleTaskFailure failed: %v", err)
	}
	if action != ActionEscalate {
		t.Errorf("expected ActionEscalate, got %d", action)
	}

	task, _ := db.GetTask("task-1")
	if task.Tier != state.TierCheap {
		t.Errorf("expected tier cheap after escalation, got %s", task.Tier)
	}
}

func TestHandleTaskFailure_BlocksAfterExhaustion(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID: "task-1", RunID: runID, Title: "Task A",
		Tier: state.TierPremium, TaskType: state.TaskTypeImplementation,
	}
	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	// Premium tier can't escalate, so after max retries it should block
	for i := 1; i <= MaxRetriesPerTier; i++ {
		_, err = db.CreateAttempt(&state.TaskAttempt{
			TaskID: "task-1", AttemptNumber: i, ModelID: "premium/model",
			ModelFamily: "premium", Tier: state.TierPremium, BaseSnapshot: "abc",
			Status: state.AttemptFailed, Phase: state.PhaseFailed,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus("task-1", state.TaskFailed); err != nil {
		t.Fatal(err)
	}

	action, err := svc.HandleTaskFailure(context.Background(), "task-1", "exhausted")
	if err != nil {
		t.Fatalf("HandleTaskFailure failed: %v", err)
	}
	if action != ActionBlock {
		t.Errorf("expected ActionBlock, got %d", action)
	}

	task, _ := db.GetTask("task-1")
	// After blocking via HandleTaskFailure, we go failed → queued → in_progress → blocked
	// Actually, HandleTaskFailure should handle the transitions internally
	if task.Status != state.TaskBlocked {
		t.Errorf("expected blocked after exhaustion, got %s", task.Status)
	}
}

// --- Scope expansion tests ---

func TestRequestScopeExpansion_AddsLockWait(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// Create task and move to in_progress
	input := CreateTaskInput{
		ID: "task-1", RunID: runID, Title: "Task A",
		Tier: state.TierStandard, TaskType: state.TaskTypeImplementation,
		TargetFiles: []TargetFileInput{
			{FilePath: "pkg/auth/handler.go", LockScope: "file", LockResourceKey: "pkg/auth/handler.go"},
		},
	}
	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}

	// Lock the additional file with another task
	input2 := CreateTaskInput{
		ID: "task-2", RunID: runID, Title: "Task B",
		Tier: state.TierStandard, TaskType: state.TaskTypeImplementation,
		TargetFiles: []TargetFileInput{
			{FilePath: "pkg/auth/utils.go", LockScope: "file", LockResourceKey: "pkg/auth/utils.go"},
		},
	}
	_, err = svc.CreateTask(context.Background(), input2)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AcquireLock("file", "pkg/auth/utils.go", "task-2"); err != nil {
		t.Fatal(err)
	}

	// Task-1 requests scope expansion for the locked file
	additionalFiles := []TargetFileInput{
		{FilePath: "pkg/auth/utils.go", LockScope: "file", LockResourceKey: "pkg/auth/utils.go"},
	}

	err = svc.RequestScopeExpansion(context.Background(), "task-1", additionalFiles)
	if err != nil {
		t.Fatalf("RequestScopeExpansion failed: %v", err)
	}

	task, _ := db.GetTask("task-1")
	if task.Status != state.TaskWaitingOnLock {
		t.Errorf("expected waiting_on_lock, got %s", task.Status)
	}

	waits, err := db.ListLockWaits(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 {
		t.Fatalf("expected 1 lock wait, got %d", len(waits))
	}
	if waits[0].WaitReason != "scope_expansion" {
		t.Errorf("expected wait_reason scope_expansion, got %s", waits[0].WaitReason)
	}
}

func TestRequestScopeExpansion_GrantedWhenUnlocked(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	_ = runID

	// Create task and move to in_progress
	input := CreateTaskInput{
		ID: "task-1", RunID: runID, Title: "Task A",
		Tier: state.TierStandard, TaskType: state.TaskTypeImplementation,
		TargetFiles: []TargetFileInput{
			{FilePath: "pkg/auth/handler.go", LockScope: "file", LockResourceKey: "pkg/auth/handler.go"},
		},
	}
	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus("task-1", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}

	// Request expansion for an unlocked file — should succeed without waiting
	additionalFiles := []TargetFileInput{
		{FilePath: "pkg/auth/utils.go", LockScope: "file", LockResourceKey: "pkg/auth/utils.go"},
	}

	err = svc.RequestScopeExpansion(context.Background(), "task-1", additionalFiles)
	if err != nil {
		t.Fatalf("RequestScopeExpansion failed: %v", err)
	}

	// Task should remain in_progress since lock was granted
	task, _ := db.GetTask("task-1")
	if task.Status != state.TaskInProgress {
		t.Errorf("expected in_progress when lock is available, got %s", task.Status)
	}

	// Lock should now be held by task-1
	locks, err := db.GetTaskLocks("task-1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range locks {
		if l.ResourceKey == "pkg/auth/utils.go" {
			found = true
		}
	}
	if !found {
		t.Error("expected task-1 to hold lock on pkg/auth/utils.go")
	}

	// Target file should be recorded
	files, err := db.GetTaskTargetFiles("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 target files, got %d", len(files))
	}
}

// --- countAttemptsAtTier helper test ---

func TestCountAttemptsAtCurrentTier(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := CreateTaskInput{
		ID: "task-1", RunID: runID, Title: "Task A",
		Tier: state.TierStandard, TaskType: state.TaskTypeImplementation,
	}
	_, err := svc.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	// No attempts yet
	count, err := svc.countAttemptsAtCurrentTier("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 attempts, got %d", count)
	}

	// Add an attempt at the task's current tier (standard)
	_, err = db.CreateAttempt(&state.TaskAttempt{
		TaskID: "task-1", AttemptNumber: 1, ModelID: "standard/model",
		ModelFamily: "test", Tier: state.TierStandard, BaseSnapshot: "abc",
		Status: state.AttemptFailed, Phase: state.PhaseFailed,
	})
	if err != nil {
		t.Fatal(err)
	}

	count, err = svc.countAttemptsAtCurrentTier("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 attempt at current tier, got %d", count)
	}
}
