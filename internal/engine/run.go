package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/state"

	"github.com/google/uuid"
)

// StartRunOptions configures a new project run via the high-level StartRun entrypoint.
// This is the intended public API for CLI, API, and WebSocket surfaces.
type StartRunOptions struct {
	ProjectID  string
	Prompt     string
	BaseBranch string
	BudgetUSD  float64
	Source     string // cli, tui, api, control-ws
	// AllowDirty bypasses the working-tree-clean check. Set only for
	// recovery scenarios where the user explicitly opts into resuming work
	// on a branch with uncommitted state. Architecture §28.2 requires a
	// clean tree by default; this flag is the documented escape hatch and
	// must trigger a loud WARN log when used.
	AllowDirty bool
}

// RunOptions configures a new project run (low-level helper).
type RunOptions struct {
	ProjectID  string
	BaseBranch string
	BudgetUSD  float64
}

// StartRun is the high-level entrypoint for beginning a new project run.
// It validates workspace preconditions, persists the prompt and handoff
// metadata, sets up the work branch, and leaves the run in draft_srs
// awaiting an external orchestrator to submit the initial SRS.
func (e *Engine) StartRun(opts StartRunOptions) (*state.ProjectRun, error) {
	if opts.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	source := opts.Source
	if source == "" {
		source = "cli"
	}

	// Validate workspace: working tree must be clean. AllowDirty is the
	// explicit recovery-mode escape hatch (Architecture §28.2 — dirty tree
	// is refused by default; this flag bypasses the check with a loud WARN
	// log so recovery scenarios can resume work on branches that
	// legitimately carry uncommitted state).
	if opts.AllowDirty {
		e.log.Warn("workspace clean check bypassed via AllowDirty",
			"source", source,
			"hint", "commit or stash before next run to avoid mixing state")
	} else {
		if err := e.git.ValidateClean(e.rootDir); err != nil {
			return nil, fmt.Errorf("workspace not ready: %w", err)
		}
	}

	// Create the run record via the low-level helper
	run, err := e.CreateRun(RunOptions{
		ProjectID:  opts.ProjectID,
		BaseBranch: opts.BaseBranch,
		BudgetUSD:  opts.BudgetUSD,
	})
	if err != nil {
		return nil, err
	}

	// Persist prompt and start source
	run.InitialPrompt = opts.Prompt
	run.StartSource = source
	run.OrchestratorMode = "external"
	if err := e.db.UpdateRunHandoff(run.ID, opts.Prompt, source, "external"); err != nil {
		return nil, fmt.Errorf("persisting handoff metadata: %w", err)
	}

	// Set up work branch. In AllowDirty mode the dirty tree was intentionally
	// preserved, so we route through the recovery variant that skips the
	// internal clean-tree check — otherwise SetupWorkBranch's defensive
	// ValidateClean would undo the user's explicit opt-in.
	if opts.AllowDirty {
		if err := e.git.SetupWorkBranchAllowDirty(e.rootDir, run.BaseBranch, run.WorkBranch); err != nil {
			return nil, fmt.Errorf("setting up work branch: %w", err)
		}
	} else {
		if err := e.git.SetupWorkBranch(e.rootDir, run.BaseBranch, run.WorkBranch); err != nil {
			return nil, fmt.Errorf("setting up work branch: %w", err)
		}
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.RunCreated,
		RunID: run.ID,
		Details: map[string]any{
			"prompt":            opts.Prompt,
			"start_source":     source,
			"orchestrator_mode": "external",
			"work_branch":      run.WorkBranch,
		},
	})

	e.log.Info("run started (external orchestration)",
		"run_id", run.ID,
		"source", source,
		"branch", run.WorkBranch,
	)

	return run, nil
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

// CancelRun transitions a run to cancelled and executes the architectural
// cancel protocol:
//
//  1. Flip the DB status (atomic barrier against scheduler dispatch — the
//     scheduler's findReadyTasks filters by run status).
//  2. Stop any containers still running for the run.
//  3. Revert uncommitted git changes and switch back to the base branch
//     (Architecture §23.4 — committed work on the work branch is preserved).
//  4. Emit the RunCancelled event.
//
// Container and git cleanup failures are logged but do not block the cancel.
// Per Architecture §22, the user's intent to cancel is absolute: leaked
// containers are recoverable via the next session's startup recovery pass,
// and a failed git cleanup leaves a clear log message with a manual-recovery
// command.
func (e *Engine) CancelRun(runID string) error {
	// Load the run record first so we have the BaseBranch for the git
	// cleanup call and so we enforce "run must exist" before we touch state.
	run, err := e.db.GetRun(runID)
	if err != nil {
		return fmt.Errorf("loading run %s: %w", runID, err)
	}

	// Step 1: atomic DB barrier. The scheduler's findReadyTasks filters by
	// run status, so flipping this first prevents any new task dispatch.
	if err := e.db.UpdateRunStatus(runID, state.RunCancelled); err != nil {
		return fmt.Errorf("cancelling run: %w", err)
	}

	// Step 2: best-effort container shutdown.
	if e.container != nil {
		active, listErr := e.db.ListActiveContainers(runID)
		if listErr != nil {
			e.log.Warn("listing active containers during cancel",
				"run_id", runID, "error", listErr)
		} else if len(active) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			for _, cs := range active {
				if stopErr := e.container.Stop(ctx, cs.ID); stopErr != nil {
					e.log.Warn("stopping container during cancel",
						"run_id", runID, "container_id", cs.ID, "error", stopErr)
				}
			}
			cancel()
		}
	}

	// Step 3: best-effort git cleanup.
	if e.git != nil {
		if cleanupErr := e.git.CancelCleanup(e.rootDir, run.BaseBranch); cleanupErr != nil {
			e.log.Warn("git cancel cleanup failed; manual recovery may be required",
				"run_id", runID,
				"base_branch", run.BaseBranch,
				"error", cleanupErr,
				"hint", "run 'git reset --hard && git checkout "+run.BaseBranch+"' to recover")
		}
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.RunCancelled,
		RunID: runID,
	})

	e.log.Info("run cancelled",
		"run_id", runID,
		"base_branch", run.BaseBranch,
	)
	return nil
}

// CompleteRun transitions a run to completed. Per Architecture §11.5, a run
// cannot complete while any implementation task has an open convergence pair:
// the feature is not done until the impl and its generated tests have merged
// and the pair is marked converged. CancelRun and FailRun bypass this gate on
// purpose — they record run outcomes that differ from "completed".
func (e *Engine) CompleteRun(runID string) error {
	pairs, err := e.db.ListConvergencePairsByRun(runID)
	if err != nil {
		return fmt.Errorf("listing convergence pairs for run %s: %w", runID, err)
	}
	var blocking []string
	for _, cp := range pairs {
		if cp.Status != state.ConvergenceConverged {
			blocking = append(blocking, fmt.Sprintf("%s(%s)", cp.ImplTaskID, cp.Status))
		}
	}
	if len(blocking) > 0 {
		return fmt.Errorf("cannot complete run %s: %d convergence pair(s) still open: %s",
			runID, len(blocking), strings.Join(blocking, ", "))
	}

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
