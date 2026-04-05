package engine

import (
	"encoding/json"
	"fmt"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/state"

	"github.com/google/uuid"
)

// RunOptions configures a new project run.
type RunOptions struct {
	ProjectID  string
	BaseBranch string
	BudgetUSD  float64
}

// CreateRun creates a new project run in draft_srs status and emits a run_created event.
func (e *Engine) CreateRun(opts RunOptions) (*state.ProjectRun, error) {
	proj, err := e.db.GetProject(opts.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("getting project: %w", err)
	}

	budget := opts.BudgetUSD
	if budget == 0 {
		budget = e.cfg.Budget.MaxUSD
	}

	baseBranch := opts.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	configData, err := marshalConfig(e.cfg)
	if err != nil {
		return nil, fmt.Errorf("serializing config: %w", err)
	}

	run := &state.ProjectRun{
		ID:                  uuid.New().String(),
		ProjectID:           proj.ID,
		Status:              state.RunDraftSRS,
		BaseBranch:          baseBranch,
		WorkBranch:          project.WorkBranch(proj.Slug),
		OrchestratorMode:    "embedded",
		OrchestratorRuntime: e.cfg.Orchestrator.Runtime,
		SRSApprovalDelegate: e.cfg.Orchestrator.SRSApprovalDelegate,
		BudgetMaxUSD:        budget,
		ConfigSnapshot:      string(configData),
	}

	if err := e.db.CreateRun(run); err != nil {
		return nil, fmt.Errorf("creating run: %w", err)
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.RunCreated,
		RunID: run.ID,
		Details: map[string]any{
			"project_id":  proj.ID,
			"base_branch": baseBranch,
			"work_branch": run.WorkBranch,
			"budget_usd":  budget,
		},
	})

	e.log.Info("run created",
		"run_id", run.ID,
		"project", proj.Name,
		"branch", run.WorkBranch,
	)

	return run, nil
}

// PauseRun transitions a run from active to paused.
func (e *Engine) PauseRun(runID string) error {
	if err := e.db.UpdateRunStatus(runID, state.RunPaused); err != nil {
		return fmt.Errorf("pausing run: %w", err)
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.RunPaused,
		RunID: runID,
	})

	e.log.Info("run paused", "run_id", runID)
	return nil
}

// ResumeRun transitions a run from paused to active.
func (e *Engine) ResumeRun(runID string) error {
	if err := e.db.UpdateRunStatus(runID, state.RunActive); err != nil {
		return fmt.Errorf("resuming run: %w", err)
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.RunResumed,
		RunID: runID,
	})

	e.log.Info("run resumed", "run_id", runID)
	return nil
}

// CancelRun transitions a run to cancelled.
func (e *Engine) CancelRun(runID string) error {
	if err := e.db.UpdateRunStatus(runID, state.RunCancelled); err != nil {
		return fmt.Errorf("cancelling run: %w", err)
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.RunCancelled,
		RunID: runID,
	})

	e.log.Info("run cancelled", "run_id", runID)
	return nil
}

// CompleteRun transitions a run to completed.
func (e *Engine) CompleteRun(runID string) error {
	if err := e.db.UpdateRunStatus(runID, state.RunCompleted); err != nil {
		return fmt.Errorf("completing run: %w", err)
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.RunCompleted,
		RunID: runID,
	})

	e.log.Info("run completed", "run_id", runID)
	return nil
}

// FailRun transitions a run to error status.
func (e *Engine) FailRun(runID string, reason string) error {
	if err := e.db.UpdateRunStatus(runID, state.RunError); err != nil {
		return fmt.Errorf("failing run: %w", err)
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.RunError,
		RunID: runID,
		Details: map[string]any{
			"reason": reason,
		},
	})

	e.log.Error("run failed", "run_id", runID, "reason", reason)
	return nil
}

// marshalConfig serializes config to JSON for the config_snapshot column.
func marshalConfig(cfg *config.Config) ([]byte, error) {
	return json.Marshal(cfg)
}
