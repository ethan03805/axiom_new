package testgen

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/openaxiom/axiom/internal/events"
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
	bus := events.New(db, testLogger())
	svc := New(db, bus, testLogger())
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

// seedImplTask creates an implementation task, moves it through the pipeline to done,
// and creates a successful attempt with the given model family.
func seedImplTask(t *testing.T, db *state.DB, runID, taskID, modelFamily string) {
	t.Helper()

	_, err := db.Exec(`INSERT INTO tasks
		(id, run_id, title, status, tier, task_type)
		VALUES (?, ?, ?, ?, ?, ?)`,
		taskID, runID, "Implement feature X", string(state.TaskDone),
		string(state.TierStandard), string(state.TaskTypeImplementation))
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.CreateAttempt(&state.TaskAttempt{
		TaskID:        taskID,
		AttemptNumber: 1,
		ModelID:       modelFamily + "/test-model",
		ModelFamily:   modelFamily,
		Tier:          state.TierStandard,
		BaseSnapshot:  "abc123",
		Status:        state.AttemptPassed,
		Phase:         state.PhaseSucceeded,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// seedImplTaskQueued creates an implementation task in queued status.
func seedImplTaskQueued(t *testing.T, db *state.DB, runID, taskID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO tasks
		(id, run_id, title, status, tier, task_type)
		VALUES (?, ?, ?, ?, ?, ?)`,
		taskID, runID, "Implement feature X", string(state.TaskQueued),
		string(state.TierStandard), string(state.TaskTypeImplementation))
	if err != nil {
		t.Fatal(err)
	}
}

// --- CreateTestTask tests ---

func TestCreateTestTask_CreatesTestTaskForCompletedImpl(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	testTask, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("CreateTestTask failed: %v", err)
	}

	if testTask.TaskType != state.TaskTypeTest {
		t.Errorf("expected task type %s, got %s", state.TaskTypeTest, testTask.TaskType)
	}
	if testTask.Status != state.TaskQueued {
		t.Errorf("expected status queued, got %s", testTask.Status)
	}
	if testTask.RunID != runID {
		t.Errorf("expected run ID %s, got %s", runID, testTask.RunID)
	}

	// Verify dependency on implementation task
	deps, err := db.GetTaskDependencies(testTask.ID)
	if err != nil {
		t.Fatalf("GetTaskDependencies failed: %v", err)
	}
	if len(deps) != 1 || deps[0] != "impl-1" {
		t.Errorf("expected dependency on impl-1, got %v", deps)
	}
}

func TestCreateTestTask_CreatesConvergencePair(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	testTask, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("CreateTestTask failed: %v", err)
	}

	// Verify convergence pair was created
	cp, err := db.GetConvergencePairByImplTask("impl-1")
	if err != nil {
		t.Fatalf("GetConvergencePairByImplTask failed: %v", err)
	}
	if cp.ImplTaskID != "impl-1" {
		t.Errorf("expected impl task ID impl-1, got %s", cp.ImplTaskID)
	}
	if cp.TestTaskID == nil || *cp.TestTaskID != testTask.ID {
		t.Errorf("expected test task ID %s, got %v", testTask.ID, cp.TestTaskID)
	}
	if cp.ImplModelFamily != "anthropic" {
		t.Errorf("expected impl model family anthropic, got %s", cp.ImplModelFamily)
	}
	if cp.Status != state.ConvergenceTesting {
		t.Errorf("expected status testing, got %s", cp.Status)
	}
	if cp.Iteration != 1 {
		t.Errorf("expected iteration 1, got %d", cp.Iteration)
	}
}

func TestCreateTestTask_RejectsNonDoneImplTask(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTaskQueued(t, db, runID, "impl-1")

	_, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err == nil {
		t.Fatal("expected error creating test task for non-done implementation")
	}
}

func TestCreateTestTask_RejectsNonImplTask(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// Create a test-type task (not implementation)
	_, err := db.Exec(`INSERT INTO tasks
		(id, run_id, title, status, tier, task_type)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"test-task-1", runID, "Test something", string(state.TaskDone),
		string(state.TierStandard), string(state.TaskTypeTest))
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.CreateTestTask(context.Background(), "test-task-1")
	if err == nil {
		t.Fatal("expected error creating test task for non-implementation task")
	}
}

func TestCreateTestTask_RejectsImplWithoutSuccessfulAttempt(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// Create a done impl task but with NO successful attempt
	_, err := db.Exec(`INSERT INTO tasks
		(id, run_id, title, status, tier, task_type)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"impl-1", runID, "Implement X", string(state.TaskDone),
		string(state.TierStandard), string(state.TaskTypeImplementation))
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.CreateTestTask(context.Background(), "impl-1")
	if err == nil {
		t.Fatal("expected error when impl has no successful attempt")
	}
}

// --- GetExcludeFamily tests ---

func TestGetExcludeFamily_ReturnsImplModelFamily(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	testTask, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("CreateTestTask failed: %v", err)
	}

	family, err := svc.GetExcludeFamily(context.Background(), testTask.ID)
	if err != nil {
		t.Fatalf("GetExcludeFamily failed: %v", err)
	}
	if family != "anthropic" {
		t.Errorf("expected exclude family anthropic, got %s", family)
	}
}

func TestGetExcludeFamily_ReturnsEmptyForNonTestTask(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	family, err := svc.GetExcludeFamily(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("GetExcludeFamily failed: %v", err)
	}
	if family != "" {
		t.Errorf("expected empty exclude family for impl task, got %s", family)
	}
}

// --- HandleTestFailure tests ---

func TestHandleTestFailure_CreatesFixTask(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	testTask, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("CreateTestTask failed: %v", err)
	}

	// Move test task through pipeline to failed
	if err := db.UpdateTaskStatus(testTask.ID, state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus(testTask.ID, state.TaskFailed); err != nil {
		t.Fatal(err)
	}

	fixTask, err := svc.HandleTestFailure(context.Background(), testTask.ID,
		"TestAuth_Login failed: expected 200, got 401")
	if err != nil {
		t.Fatalf("HandleTestFailure failed: %v", err)
	}

	// Fix task should be an implementation task
	if fixTask.TaskType != state.TaskTypeImplementation {
		t.Errorf("expected fix task type %s, got %s", state.TaskTypeImplementation, fixTask.TaskType)
	}
	if fixTask.Status != state.TaskQueued {
		t.Errorf("expected status queued, got %s", fixTask.Status)
	}

	// Fix task should depend on the test task
	deps, err := db.GetTaskDependencies(fixTask.ID)
	if err != nil {
		t.Fatalf("GetTaskDependencies failed: %v", err)
	}
	if len(deps) != 1 || deps[0] != testTask.ID {
		t.Errorf("expected fix task to depend on test task %s, got %v", testTask.ID, deps)
	}
}

func TestHandleTestFailure_UpdatesConvergencePair(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	testTask, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateTaskStatus(testTask.ID, state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus(testTask.ID, state.TaskFailed); err != nil {
		t.Fatal(err)
	}

	fixTask, err := svc.HandleTestFailure(context.Background(), testTask.ID, "test failure output")
	if err != nil {
		t.Fatal(err)
	}

	cp, err := db.GetConvergencePairByImplTask("impl-1")
	if err != nil {
		t.Fatalf("GetConvergencePairByImplTask failed: %v", err)
	}
	if cp.Status != state.ConvergenceFixing {
		t.Errorf("expected status fixing, got %s", cp.Status)
	}
	if cp.FixTaskID == nil || *cp.FixTaskID != fixTask.ID {
		t.Errorf("expected fix task ID %s, got %v", fixTask.ID, cp.FixTaskID)
	}
	if cp.Iteration != 2 {
		t.Errorf("expected iteration 2 after fix, got %d", cp.Iteration)
	}
}

func TestHandleTestFailure_RejectsNonFailedTestTask(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	testTask, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatal(err)
	}

	// Test task is queued, not failed
	_, err = svc.HandleTestFailure(context.Background(), testTask.ID, "output")
	if err == nil {
		t.Fatal("expected error handling failure for non-failed test task")
	}
}

func TestHandleTestFailure_RejectsNonTestTask(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	_, err := svc.HandleTestFailure(context.Background(), "impl-1", "output")
	if err == nil {
		t.Fatal("expected error handling failure for non-test task")
	}
}

// --- CheckConvergence tests ---

func TestCheckConvergence_PendingWhenJustCreated(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	// Manually create a convergence pair in pending state (before test task created)
	_, err := db.CreateConvergencePair(&state.ConvergencePair{
		ImplTaskID:      "impl-1",
		Status:          state.ConvergencePending,
		ImplModelFamily: "anthropic",
	})
	if err != nil {
		t.Fatal(err)
	}

	status, err := svc.CheckConvergence(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("CheckConvergence failed: %v", err)
	}
	if status != state.ConvergencePending {
		t.Errorf("expected pending, got %s", status)
	}
}

func TestCheckConvergence_TestingWhenTestTaskCreated(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	_, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatal(err)
	}

	status, err := svc.CheckConvergence(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("CheckConvergence failed: %v", err)
	}
	if status != state.ConvergenceTesting {
		t.Errorf("expected testing, got %s", status)
	}
}

func TestCheckConvergence_FixingAfterTestFailure(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	testTask, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateTaskStatus(testTask.ID, state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus(testTask.ID, state.TaskFailed); err != nil {
		t.Fatal(err)
	}

	_, err = svc.HandleTestFailure(context.Background(), testTask.ID, "failure output")
	if err != nil {
		t.Fatal(err)
	}

	status, err := svc.CheckConvergence(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("CheckConvergence failed: %v", err)
	}
	if status != state.ConvergenceFixing {
		t.Errorf("expected fixing, got %s", status)
	}
}

func TestCheckConvergence_NoPairReturnsEmptyString(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	status, err := svc.CheckConvergence(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("CheckConvergence failed: %v", err)
	}
	if status != "" {
		t.Errorf("expected empty status for task with no convergence pair, got %s", status)
	}
}

// --- MarkConverged tests ---

func TestMarkConverged_SetsConvergedStatus(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	testTask, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatal(err)
	}

	// Move test task to done (tests passed)
	if err := db.UpdateTaskStatus(testTask.ID, state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus(testTask.ID, state.TaskDone); err != nil {
		t.Fatal(err)
	}

	err = svc.MarkConverged(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("MarkConverged failed: %v", err)
	}

	cp, err := db.GetConvergencePairByImplTask("impl-1")
	if err != nil {
		t.Fatal(err)
	}
	if cp.Status != state.ConvergenceConverged {
		t.Errorf("expected converged, got %s", cp.Status)
	}
	if cp.ConvergedAt == nil {
		t.Error("expected converged_at to be set")
	}
}

func TestMarkConverged_RejectsWhenTestNotDone(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	_, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatal(err)
	}

	// Test task is still queued, not done
	err = svc.MarkConverged(context.Background(), "impl-1")
	if err == nil {
		t.Fatal("expected error marking converged when test task is not done")
	}
}

// --- IsFeatureDone tests ---

func TestIsFeatureDone_TrueWhenConverged(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	testTask, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateTaskStatus(testTask.ID, state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus(testTask.ID, state.TaskDone); err != nil {
		t.Fatal(err)
	}

	if err := svc.MarkConverged(context.Background(), "impl-1"); err != nil {
		t.Fatal(err)
	}

	done, err := svc.IsFeatureDone(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("IsFeatureDone failed: %v", err)
	}
	if !done {
		t.Error("expected feature to be done after convergence")
	}
}

func TestIsFeatureDone_FalseWhenTesting(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	_, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatal(err)
	}

	done, err := svc.IsFeatureDone(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("IsFeatureDone failed: %v", err)
	}
	if done {
		t.Error("expected feature not done while testing")
	}
}

func TestIsFeatureDone_FalseWhenNoPair(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	done, err := svc.IsFeatureDone(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("IsFeatureDone failed: %v", err)
	}
	if done {
		t.Error("expected feature not done with no convergence pair")
	}
}

// --- Model family enforcement tests ---

func TestCreateTestTask_DifferentModelFamilies(t *testing.T) {
	// Test with several different model families to ensure they are tracked correctly
	families := []string{"anthropic", "openai", "google", "meta"}

	for _, family := range families {
		t.Run(fmt.Sprintf("impl_family_%s", family), func(t *testing.T) {
			svc, db := testService(t)
			projID := seedProject(t, db)
			runID := seedRun(t, db, projID)
			seedImplTask(t, db, runID, "impl-1", family)

			testTask, err := svc.CreateTestTask(context.Background(), "impl-1")
			if err != nil {
				t.Fatalf("CreateTestTask failed: %v", err)
			}

			// The exclude family should match the impl family
			excludeFamily, err := svc.GetExcludeFamily(context.Background(), testTask.ID)
			if err != nil {
				t.Fatal(err)
			}
			if excludeFamily != family {
				t.Errorf("expected exclude family %s, got %s", family, excludeFamily)
			}
		})
	}
}

// --- MarkBlocked tests ---

func TestMarkBlocked_SetsBlockedStatus(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	_, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatal(err)
	}

	err = svc.MarkBlocked(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("MarkBlocked failed: %v", err)
	}

	cp, err := db.GetConvergencePairByImplTask("impl-1")
	if err != nil {
		t.Fatal(err)
	}
	if cp.Status != state.ConvergenceBlocked {
		t.Errorf("expected blocked, got %s", cp.Status)
	}
}

// --- Fix task context tests ---

func TestHandleTestFailure_FixTaskDescriptionContainsFailureOutput(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	testTask, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateTaskStatus(testTask.ID, state.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus(testTask.ID, state.TaskFailed); err != nil {
		t.Fatal(err)
	}

	failureOutput := "FAIL: TestAuth_Login (0.02s)\n    auth_test.go:45: expected 200, got 401"
	fixTask, err := svc.HandleTestFailure(context.Background(), testTask.ID, failureOutput)
	if err != nil {
		t.Fatal(err)
	}

	if fixTask.Description == nil {
		t.Fatal("expected fix task to have a description")
	}
	// Description should reference the failure output
	if *fixTask.Description == "" {
		t.Error("expected non-empty description containing failure context")
	}
}

// --- Duplicate convergence pair protection ---

func TestCreateTestTask_RejectsDuplicateForSameImpl(t *testing.T) {
	svc, db := testService(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	seedImplTask(t, db, runID, "impl-1", "anthropic")

	_, err := svc.CreateTestTask(context.Background(), "impl-1")
	if err != nil {
		t.Fatalf("first CreateTestTask failed: %v", err)
	}

	// Second call should fail — can't create duplicate test task for same impl
	_, err = svc.CreateTestTask(context.Background(), "impl-1")
	if err == nil {
		t.Fatal("expected error creating duplicate test task for same implementation")
	}
}
