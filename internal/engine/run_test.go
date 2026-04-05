package engine

import (
	"testing"
	"time"

	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

func TestCreateRun(t *testing.T) {
	e := testEngine(t)

	// Seed a project first
	err := e.DB().CreateProject(&state.Project{
		ID:       "proj-1",
		RootPath: e.RootDir(),
		Name:     "test-project",
		Slug:     "test-project",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := e.CreateRun(RunOptions{
		ProjectID:  "proj-1",
		BaseBranch: "main",
		BudgetUSD:  5.0,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	if run.ID == "" {
		t.Error("expected non-empty run ID")
	}
	if run.Status != state.RunDraftSRS {
		t.Errorf("status = %q, want %q", run.Status, state.RunDraftSRS)
	}
	if run.BaseBranch != "main" {
		t.Errorf("base_branch = %q, want main", run.BaseBranch)
	}
	if run.BudgetMaxUSD != 5.0 {
		t.Errorf("budget = %v, want 5.0", run.BudgetMaxUSD)
	}

	// Verify persisted in DB
	got, err := e.DB().GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunDraftSRS {
		t.Errorf("persisted status = %q, want %q", got.Status, state.RunDraftSRS)
	}
}

func TestCreateRun_EmitsEvent(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test", Slug: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	_, err = e.CreateRun(RunOptions{
		ProjectID:  "proj-1",
		BaseBranch: "main",
		BudgetUSD:  5.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Type != events.RunCreated {
			t.Errorf("event type = %q, want %q", ev.Type, events.RunCreated)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run_created event")
	}
}

func TestCreateRun_UsesConfigDefaults(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test", Slug: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	// When BudgetUSD is 0, should use config default
	run, err := e.CreateRun(RunOptions{
		ProjectID:  "proj-1",
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	if run.BudgetMaxUSD != e.Config().Budget.MaxUSD {
		t.Errorf("budget = %v, want config default %v", run.BudgetMaxUSD, e.Config().Budget.MaxUSD)
	}
	if run.OrchestratorRuntime != e.Config().Orchestrator.Runtime {
		t.Errorf("runtime = %q, want config default %q", run.OrchestratorRuntime, e.Config().Orchestrator.Runtime)
	}
}

func TestCreateRun_GeneratesWorkBranch(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test", Slug: "test-project",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := e.CreateRun(RunOptions{
		ProjectID:  "proj-1",
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	if run.WorkBranch != "axiom/test-project" {
		t.Errorf("work_branch = %q, want %q", run.WorkBranch, "axiom/test-project")
	}
}

func TestPauseRun(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test", Slug: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := e.CreateRun(RunOptions{
		ProjectID: "proj-1", BaseBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Transition to active first (draft_srs -> awaiting_srs_approval -> active)
	if err := e.DB().UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := e.DB().UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	if err := e.PauseRun(run.ID); err != nil {
		t.Fatalf("PauseRun: %v", err)
	}

	// Verify status
	got, err := e.DB().GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunPaused {
		t.Errorf("status = %q, want paused", got.Status)
	}
	if got.PausedAt == nil {
		t.Error("expected paused_at to be set")
	}

	// Verify event
	select {
	case ev := <-ch:
		if ev.Type != events.RunPaused {
			t.Errorf("event type = %q, want %q", ev.Type, events.RunPaused)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestResumeRun(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test", Slug: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := e.CreateRun(RunOptions{
		ProjectID: "proj-1", BaseBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get to paused: draft_srs -> awaiting -> active -> paused
	if err := e.DB().UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := e.DB().UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}
	if err := e.PauseRun(run.ID); err != nil {
		t.Fatal(err)
	}

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	if err := e.ResumeRun(run.ID); err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}

	got, err := e.DB().GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunActive {
		t.Errorf("status = %q, want active", got.Status)
	}

	select {
	case ev := <-ch:
		if ev.Type != events.RunResumed {
			t.Errorf("event type = %q, want %q", ev.Type, events.RunResumed)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestCancelRun(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test", Slug: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := e.CreateRun(RunOptions{
		ProjectID: "proj-1", BaseBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get to active
	if err := e.DB().UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := e.DB().UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	if err := e.CancelRun(run.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	got, err := e.DB().GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunCancelled {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
	if got.CancelledAt == nil {
		t.Error("expected cancelled_at to be set")
	}

	select {
	case ev := <-ch:
		if ev.Type != events.RunCancelled {
			t.Errorf("event type = %q, want %q", ev.Type, events.RunCancelled)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestCompleteRun(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test", Slug: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := e.CreateRun(RunOptions{
		ProjectID: "proj-1", BaseBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get to active
	if err := e.DB().UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := e.DB().UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	if err := e.CompleteRun(run.ID); err != nil {
		t.Fatalf("CompleteRun: %v", err)
	}

	got, err := e.DB().GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}

	select {
	case ev := <-ch:
		if ev.Type != events.RunCompleted {
			t.Errorf("event type = %q, want %q", ev.Type, events.RunCompleted)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestFailRun(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test", Slug: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := e.CreateRun(RunOptions{
		ProjectID: "proj-1", BaseBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get to active
	if err := e.DB().UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := e.DB().UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	if err := e.FailRun(run.ID, "provider unavailable"); err != nil {
		t.Fatalf("FailRun: %v", err)
	}

	got, err := e.DB().GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunError {
		t.Errorf("status = %q, want error", got.Status)
	}

	select {
	case ev := <-ch:
		if ev.Type != events.RunError {
			t.Errorf("event type = %q, want %q", ev.Type, events.RunError)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestPauseRun_InvalidTransition(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test", Slug: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := e.CreateRun(RunOptions{
		ProjectID: "proj-1", BaseBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try to pause from draft_srs (invalid transition)
	err = e.PauseRun(run.ID)
	if err == nil {
		t.Fatal("expected error for invalid transition draft_srs -> paused")
	}
}
