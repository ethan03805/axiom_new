package engine

import (
	"errors"
	"fmt"

	"github.com/openaxiom/axiom/internal/state"
)

// RunStatusProjection is the top-level status view model used by
// `axiom status`, the TUI, and the future GUI (Architecture Section 26).
type RunStatusProjection struct {
	ProjectID   string
	ProjectName string
	ProjectSlug string
	RootDir     string
	Run         *state.ProjectRun
	Tasks       TaskSummary
	Budget      BudgetSummary
}

// TaskSummary counts tasks by status for a run.
type TaskSummary struct {
	Total        int
	Queued       int
	InProgress   int
	WaitingLock  int
	Done         int
	Failed       int
	Blocked      int
	CancelledECO int
}

// BudgetSummary summarizes cost and budget for a run.
type BudgetSummary struct {
	MaxUSD       float64
	SpentUSD     float64
	RemainingUSD float64
	WarnPercent  int
	WarnReached  bool
}

// GetRunStatus builds a status projection for the given project.
// If no active run exists, Run will be nil but project info is still populated.
func (e *Engine) GetRunStatus(projectID string) (*RunStatusProjection, error) {
	proj, err := e.db.GetProject(projectID)
	if err != nil {
		return nil, fmt.Errorf("getting project: %w", err)
	}

	projection := &RunStatusProjection{
		ProjectID:   proj.ID,
		ProjectName: proj.Name,
		ProjectSlug: proj.Slug,
		RootDir:     e.rootDir,
	}

	run, err := e.db.GetActiveRun(projectID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return projection, nil
		}
		return nil, fmt.Errorf("getting active run: %w", err)
	}

	projection.Run = run
	projection.Budget = e.buildBudgetSummary(run)

	tasks, err := e.db.ListTasksByRun(run.ID)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	projection.Tasks = buildTaskSummary(tasks)

	return projection, nil
}

func (e *Engine) buildBudgetSummary(run *state.ProjectRun) BudgetSummary {
	spent, err := e.db.TotalCostByRun(run.ID)
	if err != nil {
		e.log.Warn("failed to get total cost", "run_id", run.ID, "error", err)
		spent = 0
	}

	remaining := run.BudgetMaxUSD - spent
	if remaining < 0 {
		remaining = 0
	}

	warnPercent := e.cfg.Budget.WarnAtPercent
	warnThreshold := run.BudgetMaxUSD * float64(warnPercent) / 100.0

	return BudgetSummary{
		MaxUSD:       run.BudgetMaxUSD,
		SpentUSD:     spent,
		RemainingUSD: remaining,
		WarnPercent:  warnPercent,
		WarnReached:  run.BudgetMaxUSD > 0 && spent >= warnThreshold,
	}
}

func buildTaskSummary(tasks []state.Task) TaskSummary {
	var s TaskSummary
	s.Total = len(tasks)
	for _, t := range tasks {
		switch t.Status {
		case state.TaskQueued:
			s.Queued++
		case state.TaskInProgress:
			s.InProgress++
		case state.TaskWaitingOnLock:
			s.WaitingLock++
		case state.TaskDone:
			s.Done++
		case state.TaskFailed:
			s.Failed++
		case state.TaskBlocked:
			s.Blocked++
		case state.TaskCancelledECO:
			s.CancelledECO++
		}
	}
	return s
}
