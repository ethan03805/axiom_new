package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/openaxiom/axiom/internal/testgen"
)

// --- Shared helpers for merge-queue-adapter unit tests ---

// seedTestgenProjectAndRun inserts a project and an active run suitable for
// testing the mergeQueueTaskAdapter's testgen dispatch branches.
func seedTestgenProjectAndRun(t *testing.T, db *state.DB, projectID, runID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO projects (id, root_path, name, slug) VALUES (?, ?, ?, ?)`,
		projectID, t.TempDir(), projectID, projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO project_runs
		(id, project_id, status, base_branch, work_branch, orchestrator_mode,
		 orchestrator_runtime, srs_approval_delegate, budget_max_usd, config_snapshot)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, projectID, string(state.RunActive), "main", "axiom/"+projectID,
		"embedded", "claw", "user", 10.0, "{}"); err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

// seedImplTaskInProgress inserts an implementation task in in_progress status
// along with a passed attempt for the given model family. This is the typical
// state of a task right before the merge queue calls CompleteTask on it.
func seedImplTaskInProgress(t *testing.T, db *state.DB, runID, taskID, modelFamily string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO tasks
		(id, run_id, title, status, tier, task_type)
		VALUES (?, ?, ?, ?, ?, ?)`,
		taskID, runID, "impl "+taskID, string(state.TaskInProgress),
		string(state.TierStandard), string(state.TaskTypeImplementation)); err != nil {
		t.Fatalf("seed impl task: %v", err)
	}
	if _, err := db.CreateAttempt(&state.TaskAttempt{
		TaskID:        taskID,
		AttemptNumber: 1,
		ModelID:       modelFamily + "/claude",
		ModelFamily:   modelFamily,
		Tier:          state.TierStandard,
		BaseSnapshot:  "base-sha",
		Status:        state.AttemptPassed,
		Phase:         state.PhaseSucceeded,
	}); err != nil {
		t.Fatalf("seed passed attempt: %v", err)
	}
}

// seedImplTaskDone inserts an already-done implementation task plus its
// passed attempt. This skips the CompleteTask->TaskDone transition so a test
// can set up a post-impl-merge state directly.
func seedImplTaskDone(t *testing.T, db *state.DB, runID, taskID, modelFamily string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO tasks
		(id, run_id, title, status, tier, task_type)
		VALUES (?, ?, ?, ?, ?, ?)`,
		taskID, runID, "impl "+taskID, string(state.TaskDone),
		string(state.TierStandard), string(state.TaskTypeImplementation)); err != nil {
		t.Fatalf("seed impl task: %v", err)
	}
	if _, err := db.CreateAttempt(&state.TaskAttempt{
		TaskID:        taskID,
		AttemptNumber: 1,
		ModelID:       modelFamily + "/claude",
		ModelFamily:   modelFamily,
		Tier:          state.TierStandard,
		BaseSnapshot:  "base-sha",
		Status:        state.AttemptPassed,
		Phase:         state.PhaseSucceeded,
	}); err != nil {
		t.Fatalf("seed passed attempt: %v", err)
	}
}

func newAdapterWithTestGen(t *testing.T, db *state.DB) (*mergeQueueTaskAdapter, *testgen.Service) {
	t.Helper()
	bus := events.New(db, testLogger())
	svc := testgen.New(db, bus, testLogger())
	return &mergeQueueTaskAdapter{db: db, testGen: svc, log: testLogger()}, svc
}

// --- Task 1 compile-time assertion: the adapter carries a testGen field ---

func TestMergeQueueTaskAdapter_HasTestGenField(t *testing.T) {
	a := &mergeQueueTaskAdapter{testGen: (*testgen.Service)(nil)}
	_ = a
}

// --- Task 2: implementation merge spawns a test task + convergence pair ---

func TestMergeQueueCompleteTask_ImplMerge_SpawnsTestTask(t *testing.T) {
	db := testDB(t)
	seedTestgenProjectAndRun(t, db, "proj-1", "run-1")
	seedImplTaskInProgress(t, db, "run-1", "impl-1", "anthropic")

	adapter, _ := newAdapterWithTestGen(t, db)
	if err := adapter.CompleteTask(context.Background(), "impl-1"); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	after, err := db.GetTask("impl-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != state.TaskDone {
		t.Fatalf("impl status = %q, want done", after.Status)
	}

	testTask, err := db.GetTask("impl-1-test")
	if err != nil {
		t.Fatalf("expected test task impl-1-test to exist: %v", err)
	}
	if testTask.TaskType != state.TaskTypeTest {
		t.Fatalf("spawned task type = %q, want test", testTask.TaskType)
	}
	if testTask.Status != state.TaskQueued {
		t.Fatalf("spawned task status = %q, want queued", testTask.Status)
	}

	cp, err := db.GetConvergencePairByImplTask("impl-1")
	if err != nil {
		t.Fatalf("expected convergence pair: %v", err)
	}
	if cp.ImplModelFamily != "anthropic" {
		t.Fatalf("impl family = %q, want anthropic", cp.ImplModelFamily)
	}
	if cp.Status != state.ConvergenceTesting {
		t.Fatalf("pair status = %q, want testing", cp.Status)
	}
	if cp.TestTaskID == nil || *cp.TestTaskID != "impl-1-test" {
		t.Fatalf("test task id in pair = %v, want impl-1-test", cp.TestTaskID)
	}

	deps, err := db.GetTaskDependencies("impl-1-test")
	if err != nil {
		t.Fatalf("GetTaskDependencies: %v", err)
	}
	if len(deps) != 1 || deps[0] != "impl-1" {
		t.Fatalf("test deps = %v, want [impl-1]", deps)
	}
}

// --- Task 3: test-task merge marks convergence pair converged ---

func TestMergeQueueCompleteTask_TestMerge_MarksConverged(t *testing.T) {
	db := testDB(t)
	seedTestgenProjectAndRun(t, db, "proj-1", "run-1")
	seedImplTaskDone(t, db, "run-1", "impl-1", "anthropic")

	adapter, svc := newAdapterWithTestGen(t, db)

	if _, err := svc.CreateTestTask(context.Background(), "impl-1"); err != nil {
		t.Fatalf("seed CreateTestTask: %v", err)
	}
	if err := db.UpdateTaskStatus("impl-1-test", state.TaskInProgress); err != nil {
		t.Fatalf("mark test task in_progress: %v", err)
	}

	if err := adapter.CompleteTask(context.Background(), "impl-1-test"); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	cp, err := db.GetConvergencePairByImplTask("impl-1")
	if err != nil {
		t.Fatalf("GetConvergencePairByImplTask: %v", err)
	}
	if cp.Status != state.ConvergenceConverged {
		t.Fatalf("pair status = %q, want converged", cp.Status)
	}
	if cp.ConvergedAt == nil {
		t.Fatalf("converged_at not set")
	}
}

// --- Task 4: fix-task merge marks convergence pair converged ---

func TestMergeQueueCompleteTask_FixMerge_MarksConverged(t *testing.T) {
	db := testDB(t)
	seedTestgenProjectAndRun(t, db, "proj-1", "run-1")
	seedImplTaskDone(t, db, "run-1", "impl-1", "anthropic")

	adapter, svc := newAdapterWithTestGen(t, db)

	if _, err := svc.CreateTestTask(context.Background(), "impl-1"); err != nil {
		t.Fatalf("seed CreateTestTask: %v", err)
	}

	// Simulate test failure → fix task via testgen.HandleTestFailure.
	if err := db.UpdateTaskStatus("impl-1-test", state.TaskInProgress); err != nil {
		t.Fatalf("test in_progress: %v", err)
	}
	if err := db.UpdateTaskStatus("impl-1-test", state.TaskFailed); err != nil {
		t.Fatalf("test failed: %v", err)
	}
	fixTask, err := svc.HandleTestFailure(context.Background(), "impl-1-test", "TestFoo FAIL")
	if err != nil {
		t.Fatalf("HandleTestFailure: %v", err)
	}
	// Drive the fix task through the pipeline to in_progress so CompleteTask's
	// UpdateTaskStatus call is a valid transition.
	if err := db.UpdateTaskStatus(fixTask.ID, state.TaskInProgress); err != nil {
		t.Fatalf("fix task in_progress: %v", err)
	}
	// The test task still needs to be marked done so MarkConverged's
	// TaskDone invariant check passes. In a real run the fix task merge comes
	// after the test task has previously merged (or is retried as part of the
	// fix loop), but for this unit test we transition directly.
	if err := db.UpdateTaskStatus("impl-1-test", state.TaskQueued); err != nil {
		t.Fatalf("test requeue: %v", err)
	}
	if err := db.UpdateTaskStatus("impl-1-test", state.TaskInProgress); err != nil {
		t.Fatalf("test in_progress (second): %v", err)
	}
	if err := db.UpdateTaskStatus("impl-1-test", state.TaskDone); err != nil {
		t.Fatalf("test done: %v", err)
	}

	if err := adapter.CompleteTask(context.Background(), fixTask.ID); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	cp, err := db.GetConvergencePairByImplTask("impl-1")
	if err != nil {
		t.Fatalf("GetConvergencePairByImplTask: %v", err)
	}
	if cp.Status != state.ConvergenceConverged {
		t.Fatalf("pair status = %q, want converged", cp.Status)
	}
}

// --- Task 5: failing test-task merge routes through HandleTestFailure ---

func TestMergeQueueRequeueTask_TestTaskFailure_SpawnsFix(t *testing.T) {
	db := testDB(t)
	seedTestgenProjectAndRun(t, db, "proj-1", "run-1")
	seedImplTaskDone(t, db, "run-1", "impl-1", "anthropic")

	adapter, svc := newAdapterWithTestGen(t, db)

	if _, err := svc.CreateTestTask(context.Background(), "impl-1"); err != nil {
		t.Fatalf("seed CreateTestTask: %v", err)
	}
	if err := db.UpdateTaskStatus("impl-1-test", state.TaskInProgress); err != nil {
		t.Fatalf("test in_progress: %v", err)
	}

	if err := adapter.RequeueTask(context.Background(), "impl-1-test",
		"TestFoo FAIL at line 42"); err != nil {
		t.Fatalf("RequeueTask: %v", err)
	}

	// The test task should NOT have been requeued; it should stay in failed.
	after, err := db.GetTask("impl-1-test")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != state.TaskFailed {
		t.Fatalf("test task status = %q, want failed (not requeued)", after.Status)
	}

	cp, err := db.GetConvergencePairByImplTask("impl-1")
	if err != nil {
		t.Fatalf("GetConvergencePairByImplTask: %v", err)
	}
	if cp.Status != state.ConvergenceFixing {
		t.Fatalf("pair status = %q, want fixing", cp.Status)
	}
	if cp.FixTaskID == nil || *cp.FixTaskID == "" {
		t.Fatalf("fix_task_id not set on pair")
	}

	fix, err := db.GetTask(*cp.FixTaskID)
	if err != nil {
		t.Fatalf("expected fix task %s: %v", *cp.FixTaskID, err)
	}
	if fix.TaskType != state.TaskTypeImplementation {
		t.Fatalf("fix task type = %q, want implementation", fix.TaskType)
	}
	if fix.Description == nil || !strings.Contains(*fix.Description, "TestFoo FAIL at line 42") {
		t.Fatalf("fix task description missing failure output: %v", fix.Description)
	}
}

// TestMergeQueueCompleteTask_ImplMerge_EndToEnd_FamilyExclusion is the
// engine-level end-to-end regression for Issue 05. It drives the same path
// the merge queue takes when an implementation task merges and verifies the
// complete post-merge state the architecture requires:
//
//  1. The impl task is TaskDone.
//  2. A test task (impl-1-test) exists in queued status with type "test"
//     and dependency on impl-1.
//  3. A convergence pair exists for impl-1 in state "testing" with the
//     impl's model family recorded.
//  4. Querying the engine's testgen service for the test task's exclude
//     family returns the impl family — proving the scheduler's next
//     dispatch tick would pick a *different* family, per Architecture §11.5.
func TestMergeQueueCompleteTask_ImplMerge_EndToEnd_FamilyExclusion(t *testing.T) {
	db := testDB(t)
	seedTestgenProjectAndRun(t, db, "proj-1", "run-1")
	seedImplTaskInProgress(t, db, "run-1", "impl-1", "anthropic")

	adapter, svc := newAdapterWithTestGen(t, db)

	if err := adapter.CompleteTask(context.Background(), "impl-1"); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	// (1) Impl task is done.
	implAfter, err := db.GetTask("impl-1")
	if err != nil {
		t.Fatalf("GetTask(impl-1): %v", err)
	}
	if implAfter.Status != state.TaskDone {
		t.Fatalf("impl status = %q, want done", implAfter.Status)
	}

	// (2) Test task exists with correct shape.
	testTask, err := db.GetTask("impl-1-test")
	if err != nil {
		t.Fatalf("GetTask(impl-1-test): %v", err)
	}
	if testTask.TaskType != state.TaskTypeTest {
		t.Fatalf("test task type = %q, want test", testTask.TaskType)
	}
	if testTask.Status != state.TaskQueued {
		t.Fatalf("test task status = %q, want queued", testTask.Status)
	}
	deps, err := db.GetTaskDependencies("impl-1-test")
	if err != nil {
		t.Fatalf("GetTaskDependencies: %v", err)
	}
	if len(deps) != 1 || deps[0] != "impl-1" {
		t.Fatalf("test task deps = %v, want [impl-1]", deps)
	}

	// (3) Convergence pair in testing state with family recorded.
	cp, err := db.GetConvergencePairByImplTask("impl-1")
	if err != nil {
		t.Fatalf("GetConvergencePairByImplTask: %v", err)
	}
	if cp.Status != state.ConvergenceTesting {
		t.Fatalf("pair status = %q, want testing", cp.Status)
	}
	if cp.ImplModelFamily != "anthropic" {
		t.Fatalf("impl family = %q, want anthropic", cp.ImplModelFamily)
	}

	// (4) The scheduler's next dispatch tick must be forced onto a
	//     different family. This is the GetExcludeFamily contract the
	//     scheduler's engineFamilyExcluder uses at dispatch time.
	excl, err := svc.GetExcludeFamily(context.Background(), "impl-1-test")
	if err != nil {
		t.Fatalf("GetExcludeFamily: %v", err)
	}
	if excl != "anthropic" {
		t.Fatalf("exclude family = %q, want anthropic", excl)
	}
}

// --- Sanity: normal impl-task requeue still works when testgen is wired ---

func TestMergeQueueRequeueTask_ImplTaskFailure_RequeuesNormally(t *testing.T) {
	db := testDB(t)
	seedTestgenProjectAndRun(t, db, "proj-1", "run-1")
	seedImplTaskInProgress(t, db, "run-1", "impl-1", "anthropic")

	adapter, _ := newAdapterWithTestGen(t, db)

	if err := adapter.RequeueTask(context.Background(), "impl-1",
		"merge-queue compile failure"); err != nil {
		t.Fatalf("RequeueTask: %v", err)
	}

	after, err := db.GetTask("impl-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != state.TaskQueued {
		t.Fatalf("impl task status = %q, want queued", after.Status)
	}

	// No convergence pair should have been created for an impl-task failure.
	if _, err := db.GetConvergencePairByImplTask("impl-1"); err == nil {
		t.Fatalf("convergence pair should not exist for requeued impl task")
	}
}
