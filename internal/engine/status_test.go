package engine

import (
	"testing"

	"github.com/openaxiom/axiom/internal/state"
)

func TestGetRunStatus_NoActiveRun(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test", Slug: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	proj, err := e.GetRunStatus("proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if proj.Run != nil {
		t.Error("expected nil run when no active run")
	}
	if proj.ProjectName != "test" {
		t.Errorf("project name = %q, want test", proj.ProjectName)
	}
}

func TestGetRunStatus_WithActiveRun(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test-project", Slug: "test-project",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := e.CreateRun(RunOptions{
		ProjectID: "proj-1", BaseBranch: "main", BudgetUSD: 10.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	proj, err := e.GetRunStatus("proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if proj.Run == nil {
		t.Fatal("expected non-nil run")
	}
	if proj.Run.ID != run.ID {
		t.Errorf("run ID = %q, want %q", proj.Run.ID, run.ID)
	}
	if proj.Budget.MaxUSD != 10.0 {
		t.Errorf("budget max = %v, want 10.0", proj.Budget.MaxUSD)
	}
}

func TestGetRunStatus_TaskSummary(t *testing.T) {
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

	// Move to active
	if err := e.DB().UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := e.DB().UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}

	// Add some tasks with different statuses
	// Create all tasks as queued (valid initial state), then transition as needed
	for _, id := range []string{"t1", "t2", "t3"} {
		err := e.DB().CreateTask(&state.Task{
			ID:       id,
			RunID:    run.ID,
			Title:    "task " + id,
			Status:   state.TaskQueued,
			Tier:     state.TierStandard,
			TaskType: state.TaskTypeImplementation,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Transition t3 to in_progress (queued -> in_progress)
	if err := e.DB().UpdateTaskStatus("t3", state.TaskInProgress); err != nil {
		t.Fatal(err)
	}

	proj, err := e.GetRunStatus("proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if proj.Tasks.Total != 3 {
		t.Errorf("total = %d, want 3", proj.Tasks.Total)
	}
	if proj.Tasks.Queued != 2 {
		t.Errorf("queued = %d, want 2", proj.Tasks.Queued)
	}
	if proj.Tasks.InProgress != 1 {
		t.Errorf("in_progress = %d, want 1", proj.Tasks.InProgress)
	}
}

func TestGetRunStatus_BudgetSummary(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test", Slug: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := e.CreateRun(RunOptions{
		ProjectID: "proj-1", BaseBranch: "main", BudgetUSD: 10.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add some costs
	for _, cost := range []float64{2.0, 3.0} {
		_, err := e.DB().CreateCostLog(&state.CostLogEntry{
			RunID:     run.ID,
			AgentType: "meeseeks",
			ModelID:   "model",
			CostUSD:   cost,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	proj, err := e.GetRunStatus("proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if proj.Budget.MaxUSD != 10.0 {
		t.Errorf("max = %v, want 10.0", proj.Budget.MaxUSD)
	}
	if proj.Budget.SpentUSD != 5.0 {
		t.Errorf("spent = %v, want 5.0", proj.Budget.SpentUSD)
	}
	if proj.Budget.RemainingUSD != 5.0 {
		t.Errorf("remaining = %v, want 5.0", proj.Budget.RemainingUSD)
	}
}

func TestGetRunStatus_BudgetWarning(t *testing.T) {
	e := testEngine(t)

	err := e.DB().CreateProject(&state.Project{
		ID: "proj-1", RootPath: e.RootDir(), Name: "test", Slug: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := e.CreateRun(RunOptions{
		ProjectID: "proj-1", BaseBranch: "main", BudgetUSD: 10.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Spend 85% of budget (warn_at_percent default is 80)
	_, err = e.DB().CreateCostLog(&state.CostLogEntry{
		RunID: run.ID, AgentType: "meeseeks", ModelID: "model", CostUSD: 8.5,
	})
	if err != nil {
		t.Fatal(err)
	}

	proj, err := e.GetRunStatus("proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if !proj.Budget.WarnReached {
		t.Error("expected budget warning to be reached at 85% spend")
	}
}
