package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

// --- Tracking fakes for cancel protocol coverage ---

type cancelCleanupCall struct {
	dir        string
	baseBranch string
}

type cancelTrackingGitService struct {
	cancelCleanupCalls []cancelCleanupCall
	cancelCleanupErr   error
}

func (g *cancelTrackingGitService) CurrentBranch(string) (string, error) { return "main", nil }
func (g *cancelTrackingGitService) CreateBranch(string, string) error    { return nil }
func (g *cancelTrackingGitService) CurrentHEAD(string) (string, error)   { return "sha", nil }
func (g *cancelTrackingGitService) IsDirty(string) (bool, error)         { return false, nil }
func (g *cancelTrackingGitService) ValidateClean(string) error           { return nil }
func (g *cancelTrackingGitService) SetupWorkBranch(string, string, string) error {
	return nil
}
func (g *cancelTrackingGitService) SetupWorkBranchAllowDirty(string, string, string) error {
	return nil
}
func (g *cancelTrackingGitService) CancelCleanup(dir, baseBranch string) error {
	g.cancelCleanupCalls = append(g.cancelCleanupCalls, cancelCleanupCall{dir: dir, baseBranch: baseBranch})
	return g.cancelCleanupErr
}
func (g *cancelTrackingGitService) AddFiles(string, []string) error { return nil }
func (g *cancelTrackingGitService) Commit(string, string) (string, error) {
	return "commit-sha", nil
}
func (g *cancelTrackingGitService) ChangedFilesSince(string, string) ([]string, error) {
	return nil, nil
}
func (g *cancelTrackingGitService) DiffRange(string, string, string) (string, error) {
	return "", nil
}

type trackingContainerService struct {
	stopped []string
}

func (s *trackingContainerService) Start(context.Context, ContainerSpec) (string, error) {
	return "container-1", nil
}
func (s *trackingContainerService) Stop(_ context.Context, id string) error {
	s.stopped = append(s.stopped, id)
	return nil
}
func (s *trackingContainerService) ListRunning(context.Context) ([]string, error) {
	return nil, nil
}
func (s *trackingContainerService) Cleanup(context.Context) error { return nil }
func (s *trackingContainerService) Exec(context.Context, string, []string) (ExecResult, error) {
	return ExecResult{}, nil
}

// --- Helpers ---

func newCancelTestEngine(t *testing.T, git GitService, container ContainerService) *Engine {
	t.Helper()
	db := testDB(t)
	cfg := testConfig()
	dir := t.TempDir()

	if git == nil {
		git = &noopGitService{}
	}

	e, err := New(Options{
		Config:    cfg,
		DB:        db,
		RootDir:   dir,
		Log:       testLogger(),
		Git:       git,
		Container: container,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Stop() })
	return e
}

// driveRunToActive creates a project + run and transitions the run into the
// active state, returning the run record.
func driveRunToActive(t *testing.T, e *Engine) *state.ProjectRun {
	t.Helper()
	projectID := seedProject(t, e, "cancel-protocol")

	run, err := e.CreateRun(RunOptions{
		ProjectID:  projectID,
		BaseBranch: "main",
		BudgetUSD:  5.0,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := e.db.UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatalf("UpdateRunStatus awaiting: %v", err)
	}
	if err := e.db.UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatalf("UpdateRunStatus active: %v", err)
	}
	return run
}

// --- Tests ---

// TestCancelRun_CallsCancelCleanup pins the wiring: Engine.CancelRun must
// call git.CancelCleanup with the run's rootDir and base branch. This is the
// single most important regression guard against the Issue 06 drift where
// CancelCleanup had zero runtime callers.
func TestCancelRun_CallsCancelCleanup(t *testing.T) {
	gitSvc := &cancelTrackingGitService{}
	e := newCancelTestEngine(t, gitSvc, nil)
	run := driveRunToActive(t, e)

	if err := e.CancelRun(run.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	if len(gitSvc.cancelCleanupCalls) != 1 {
		t.Fatalf("CancelCleanup calls = %d, want 1", len(gitSvc.cancelCleanupCalls))
	}
	call := gitSvc.cancelCleanupCalls[0]
	if call.dir != e.RootDir() {
		t.Errorf("CancelCleanup dir = %q, want %q", call.dir, e.RootDir())
	}
	if call.baseBranch != "main" {
		t.Errorf("CancelCleanup baseBranch = %q, want main", call.baseBranch)
	}
}

// TestCancelRun_StopsActiveContainers verifies that cancelling a run with
// active container sessions stops each of them.
func TestCancelRun_StopsActiveContainers(t *testing.T) {
	gitSvc := &cancelTrackingGitService{}
	containerSvc := &trackingContainerService{}
	e := newCancelTestEngine(t, gitSvc, containerSvc)
	run := driveRunToActive(t, e)

	// Seed two active container sessions on the run.
	if err := e.db.CreateTask(&state.Task{
		ID:       "t1",
		RunID:    run.ID,
		Title:    "seed task",
		Status:   state.TaskInProgress,
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, id := range []string{"c-meeseeks-1", "c-reviewer-1"} {
		if err := e.db.CreateContainerSession(&state.ContainerSession{
			ID:            id,
			RunID:         run.ID,
			TaskID:        "t1",
			ContainerType: state.ContainerMeeseeks,
			Image:         "axiom/meeseeks:test",
		}); err != nil {
			t.Fatalf("CreateContainerSession: %v", err)
		}
	}

	if err := e.CancelRun(run.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	if len(containerSvc.stopped) != 2 {
		t.Fatalf("stopped = %v, want 2 containers stopped", containerSvc.stopped)
	}
	got := map[string]bool{}
	for _, id := range containerSvc.stopped {
		got[id] = true
	}
	if !got["c-meeseeks-1"] || !got["c-reviewer-1"] {
		t.Errorf("stopped set = %v, want both c-meeseeks-1 and c-reviewer-1", containerSvc.stopped)
	}
}

// TestCancelRun_ProceedsWhenGitCleanupFails pins the fail-open contract:
// a git cleanup failure is logged but the cancel still transitions the run
// to cancelled and emits the event. Per Architecture §22, the user's intent
// is absolute; leaked state is recoverable via the manual hint.
func TestCancelRun_ProceedsWhenGitCleanupFails(t *testing.T) {
	gitSvc := &cancelTrackingGitService{
		cancelCleanupErr: errors.New("git reset exploded"),
	}
	e := newCancelTestEngine(t, gitSvc, nil)
	run := driveRunToActive(t, e)

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	if err := e.CancelRun(run.ID); err != nil {
		t.Fatalf("CancelRun should not return the cleanup error; got %v", err)
	}

	got, err := e.db.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Status != state.RunCancelled {
		t.Errorf("status = %q, want cancelled even after git cleanup failure", got.Status)
	}
	if got.CancelledAt == nil {
		t.Error("cancelled_at should be set even after git cleanup failure")
	}

	select {
	case ev := <-ch:
		if ev.Type != events.RunCancelled {
			t.Errorf("event type = %q, want %q", ev.Type, events.RunCancelled)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RunCancelled event")
	}
}

// TestCancelRun_FromDraftSRS_WorksEndToEnd verifies that a user can start a
// run and immediately cancel it while the run is still sitting in draft_srs
// waiting for the external orchestrator. Before Issue 06, the state machine
// refused this transition.
func TestCancelRun_FromDraftSRS_WorksEndToEnd(t *testing.T) {
	gitSvc := &cancelTrackingGitService{}
	e := newCancelTestEngine(t, gitSvc, nil)

	projectID := seedProject(t, e, "draft-cancel")
	run, err := e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "abandoned prompt",
		Source:    "cli",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if run.Status != state.RunDraftSRS {
		t.Fatalf("precondition: expected draft_srs, got %q", run.Status)
	}

	if err := e.CancelRun(run.ID); err != nil {
		t.Fatalf("CancelRun from draft_srs: %v", err)
	}

	got, err := e.db.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Status != state.RunCancelled {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
	if got.CancelledAt == nil {
		t.Error("cancelled_at should be set")
	}
	// Cancel should still walk the full protocol — CancelCleanup is invoked
	// even for pre-active runs so that any artifacts written by StartRun
	// (work-branch checkout, scratch files, .axiom/ edits) are cleaned up.
	if len(gitSvc.cancelCleanupCalls) != 1 {
		t.Errorf("CancelCleanup calls = %d, want 1 (pre-active runs still walk the cleanup)", len(gitSvc.cancelCleanupCalls))
	}
}

// TestCancelRun_LoadsRunForBaseBranch guards against anyone hardcoding
// "main" in the cancel cleanup path. A run with BaseBranch "develop" must
// drive the git cleanup against "develop".
func TestCancelRun_LoadsRunForBaseBranch(t *testing.T) {
	gitSvc := &cancelTrackingGitService{}
	e := newCancelTestEngine(t, gitSvc, nil)

	projectID := seedProject(t, e, "develop-base")
	run, err := e.CreateRun(RunOptions{
		ProjectID:  projectID,
		BaseBranch: "develop",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	if err := e.CancelRun(run.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	if len(gitSvc.cancelCleanupCalls) != 1 {
		t.Fatalf("CancelCleanup calls = %d, want 1", len(gitSvc.cancelCleanupCalls))
	}
	if got := gitSvc.cancelCleanupCalls[0].baseBranch; got != "develop" {
		t.Errorf("CancelCleanup base branch = %q, want develop", got)
	}
}

// TestCancelRun_UnknownRun asserts that cancelling a non-existent run
// returns an error instead of silently no-op'ing.
func TestCancelRun_UnknownRun(t *testing.T) {
	e := newCancelTestEngine(t, &cancelTrackingGitService{}, nil)
	if err := e.CancelRun("does-not-exist"); err == nil {
		t.Fatal("expected error for unknown run id")
	}
}
