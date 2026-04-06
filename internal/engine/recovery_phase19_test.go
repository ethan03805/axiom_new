package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/state"
)

type recoveryContainerService struct {
	cleanupCalls int
	cleanupErr   error
}

func (m *recoveryContainerService) Start(ctx context.Context, spec ContainerSpec) (string, error) {
	return spec.Name, nil
}

func (m *recoveryContainerService) Stop(ctx context.Context, id string) error { return nil }

func (m *recoveryContainerService) ListRunning(ctx context.Context) ([]string, error) {
	return nil, nil
}

func (m *recoveryContainerService) Cleanup(ctx context.Context) error {
	m.cleanupCalls++
	return m.cleanupErr
}

func newRecoveryEngine(t *testing.T, root string, containers *recoveryContainerService) *Engine {
	t.Helper()

	db := testDB(t)
	cfg := testConfig()

	if err := os.MkdirAll(filepath.Join(root, ".axiom", "containers", "staging"), 0o755); err != nil {
		t.Fatal(err)
	}

	e, err := New(Options{
		Config:    cfg,
		DB:        db,
		RootDir:   root,
		Log:       testLogger(),
		Git:       &noopGitService{},
		Container: containers,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Stop() })
	return e
}

func seedRecoveryRun(t *testing.T, e *Engine) string {
	t.Helper()

	projectID := "proj-recovery"
	if err := e.DB().CreateProject(&state.Project{
		ID:       projectID,
		RootPath: e.RootDir(),
		Name:     "recovery",
		Slug:     "recovery",
	}); err != nil {
		t.Fatal(err)
	}

	run := &state.ProjectRun{
		ID:                  "run-recovery",
		ProjectID:           projectID,
		Status:              state.RunActive,
		BaseBranch:          "main",
		WorkBranch:          "axiom/recovery",
		OrchestratorMode:    "embedded",
		OrchestratorRuntime: "codex",
		SRSApprovalDelegate: "user",
		BudgetMaxUSD:        10,
		ConfigSnapshot:      "{}",
	}
	if err := e.DB().CreateRun(run); err != nil {
		t.Fatal(err)
	}

	return run.ID
}

func TestEngineRecover_RequeuesStaleTasksRebuildsLockWaitsAndCleansStaging(t *testing.T) {
	root := t.TempDir()
	containers := &recoveryContainerService{}
	e := newRecoveryEngine(t, root, containers)
	runID := seedRecoveryRun(t, e)

	if err := e.DB().CreateTask(&state.Task{
		ID:       "task-stale",
		RunID:    runID,
		Title:    "stale task",
		Status:   state.TaskInProgress,
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
	}); err != nil {
		t.Fatal(err)
	}

	attemptID, err := e.DB().CreateAttempt(&state.TaskAttempt{
		TaskID:        "task-stale",
		AttemptNumber: 1,
		ModelID:       "anthropic/claude-4-sonnet",
		ModelFamily:   "anthropic",
		Tier:          state.TierStandard,
		BaseSnapshot:  "abc123",
		Status:        state.AttemptRunning,
		Phase:         state.PhaseExecuting,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := e.DB().CreateContainerSession(&state.ContainerSession{
		ID:            "axiom-task-stale-1",
		RunID:         runID,
		TaskID:        "task-stale",
		ContainerType: state.ContainerMeeseeks,
		Image:         "axiom:test",
	}); err != nil {
		t.Fatal(err)
	}

	if err := e.DB().AcquireLock("file", "internal/stale.go", "task-stale"); err != nil {
		t.Fatal(err)
	}

	if err := e.DB().CreateTask(&state.Task{
		ID:       "task-waiting",
		RunID:    runID,
		Title:    "waiting task",
		Status:   state.TaskWaitingOnLock,
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
	}); err != nil {
		t.Fatal(err)
	}

	requestedResources, err := json.Marshal([]map[string]string{
		{"resource_type": "file", "resource_key": "pkg/free.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.DB().AddLockWait(&state.TaskLockWait{
		TaskID:             "task-waiting",
		WaitReason:         "scope_expansion",
		RequestedResources: string(requestedResources),
	}); err != nil {
		t.Fatal(err)
	}

	stagingDir := filepath.Join(root, ".axiom", "containers", "staging", "task-stale")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "partial.patch"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := e.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}

	if containers.cleanupCalls != 1 {
		t.Fatalf("cleanupCalls = %d, want 1", containers.cleanupCalls)
	}
	if report.TasksRequeued != 2 {
		t.Fatalf("TasksRequeued = %d, want 2", report.TasksRequeued)
	}
	if report.StagingEntriesRemoved == 0 {
		t.Fatal("expected staging cleanup to remove at least one entry")
	}

	staleTask, err := e.DB().GetTask("task-stale")
	if err != nil {
		t.Fatal(err)
	}
	if staleTask.Status != state.TaskQueued {
		t.Fatalf("task-stale status = %q, want %q", staleTask.Status, state.TaskQueued)
	}

	attempt, err := e.DB().GetAttempt(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Status != state.AttemptFailed {
		t.Fatalf("attempt status = %q, want %q", attempt.Status, state.AttemptFailed)
	}
	if attempt.Phase != state.PhaseFailed {
		t.Fatalf("attempt phase = %q, want %q", attempt.Phase, state.PhaseFailed)
	}
	if attempt.FailureReason == nil || !strings.Contains(*attempt.FailureReason, "recovered") {
		t.Fatalf("failure_reason = %v, want recovery detail", attempt.FailureReason)
	}

	containerSession, err := e.DB().GetContainerSession("axiom-task-stale-1")
	if err != nil {
		t.Fatal(err)
	}
	if containerSession.StoppedAt == nil {
		t.Fatal("expected container session to be marked stopped during recovery")
	}
	if containerSession.ExitReason == nil || !strings.Contains(*containerSession.ExitReason, "recovered") {
		t.Fatalf("exit_reason = %v, want recovered marker", containerSession.ExitReason)
	}

	waitingTask, err := e.DB().GetTask("task-waiting")
	if err != nil {
		t.Fatal(err)
	}
	if waitingTask.Status != state.TaskQueued {
		t.Fatalf("task-waiting status = %q, want %q", waitingTask.Status, state.TaskQueued)
	}

	waits, err := e.DB().ListLockWaits(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(waits) != 0 {
		t.Fatalf("lock waits after recovery = %d, want 0", len(waits))
	}

	var lockCount int
	if err := e.DB().QueryRow(`SELECT COUNT(*) FROM task_locks`).Scan(&lockCount); err != nil {
		t.Fatal(err)
	}
	if lockCount != 0 {
		t.Fatalf("task_locks rows = %d, want 0", lockCount)
	}

	entries, err := os.ReadDir(filepath.Join(root, ".axiom", "containers", "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries = %d, want 0", len(entries))
	}
}

func TestEngineRecover_EmitsRecoveryEvents(t *testing.T) {
	root := t.TempDir()
	e := newRecoveryEngine(t, root, &recoveryContainerService{})

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	if _, err := e.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	var sawStarted, sawCompleted bool
	for i := 0; i < 8; i++ {
		select {
		case ev := <-ch:
			if ev.Type == events.RecoveryStarted {
				sawStarted = true
			}
			if ev.Type == events.RecoveryCompleted {
				sawCompleted = true
			}
		default:
		}
	}

	if !sawStarted {
		t.Fatal("expected recovery_started event")
	}
	if !sawCompleted {
		t.Fatal("expected recovery_completed event")
	}
}

func TestEngineRecover_FailsOnSRSIntegrityMismatch(t *testing.T) {
	root := t.TempDir()
	e := newRecoveryEngine(t, root, &recoveryContainerService{})
	runID := seedRecoveryRun(t, e)

	content := []byte("# SRS: Recovery\n\nbody")
	if err := project.WriteSRS(root, content); err != nil {
		t.Fatal(err)
	}
	if err := e.DB().UpdateRunSRSHash(runID, "deadbeef"); err != nil {
		t.Fatal(err)
	}

	if _, err := e.Recover(context.Background()); err == nil {
		t.Fatal("expected recovery to fail when the persisted SRS hash does not match")
	}
}
