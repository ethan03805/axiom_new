package engine

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/mergequeue"
	"github.com/openaxiom/axiom/internal/state"
)

// TaskCreateSpec is the payload the control WebSocket and CLI use to request
// that the engine add a new task to a run. Per Architecture §8.6 the external
// orchestrator owns task creation; the engine's role is to persist the spec,
// emit a task_created event, and let the scheduler pick it up on its next tick.
//
// Fields are intentionally flat rather than mirroring ipc.TaskSpec so the
// control surface can evolve independently of the worker-facing spec.
type TaskCreateSpec struct {
	// Objective is the short task description; it becomes the DB row's Title
	// (and any extended content is folded into Description).
	Objective string `json:"objective"`

	// Description is an optional longer-form description of the task.
	Description string `json:"description,omitempty"`

	// ContextTier is the advisory context-tier hint per Architecture §17.2
	// (symbol|file|package|repo_map|indexed). Stored only in the event details
	// for now; the worker still resolves the concrete context blocks itself.
	ContextTier string `json:"context_tier,omitempty"`

	// Files are the target files the task is allowed to modify. They are
	// recorded as task_target_files rows so the scheduler's write-set lock
	// discipline applies.
	Files []string `json:"files,omitempty"`

	// Constraints are free-form textual constraints passed through in the
	// event details; workers will read them from the attempt spec at dispatch.
	Constraints []string `json:"constraints,omitempty"`

	// AcceptanceCriteria are the conditions the orchestrator gate will
	// check for. Persisted on the event for traceability.
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`

	// InterfaceContract is a free-form blob describing the API surface
	// the task must preserve.
	InterfaceContract map[string]any `json:"interface_contract,omitempty"`

	// OutputFormat is "patch" or "files"; defaults to "files".
	OutputFormat string `json:"output_format,omitempty"`

	// SRSRefs link this task to one or more SRS requirement identifiers.
	SRSRefs []string `json:"srs_refs,omitempty"`
}

// CreateTask inserts a single task row in status=queued for the given run and
// emits a task_created event. It is the public control-plane entrypoint for
// both the WebSocket dispatcher and the `axiom task create` CLI command.
func (e *Engine) CreateTask(runID string, spec TaskCreateSpec) (*state.Task, error) {
	if runID == "" {
		return nil, errors.New("run_id is required")
	}
	if spec.Objective == "" {
		return nil, errors.New("spec.objective is required")
	}
	if _, err := e.db.GetRun(runID); err != nil {
		return nil, fmt.Errorf("loading run %s: %w", runID, err)
	}

	task := &state.Task{
		ID:       "task-" + uuid.New().String(),
		RunID:    runID,
		Title:    spec.Objective,
		Status:   state.TaskQueued,
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
	}
	if spec.Description != "" {
		desc := spec.Description
		task.Description = &desc
	}

	if err := e.db.CreateTask(task); err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}

	for _, file := range spec.Files {
		if file == "" {
			continue
		}
		tf := &state.TaskTargetFile{
			TaskID:          task.ID,
			FilePath:        file,
			LockScope:       "file",
			LockResourceKey: "file:" + file,
		}
		if err := e.db.AddTaskTargetFile(tf); err != nil {
			e.log.Warn("failed to record task target file",
				"task_id", task.ID, "file", file, "error", err)
		}
	}

	for _, ref := range spec.SRSRefs {
		if ref == "" {
			continue
		}
		if err := e.db.AddTaskSRSRef(task.ID, ref); err != nil {
			e.log.Warn("failed to record task srs ref",
				"task_id", task.ID, "ref", ref, "error", err)
		}
	}

	outputFormat := spec.OutputFormat
	if outputFormat == "" {
		outputFormat = "files"
	}

	e.emitEvent(events.EngineEvent{
		Type:   events.TaskCreated,
		RunID:  runID,
		TaskID: task.ID,
		Details: map[string]any{
			"objective":           spec.Objective,
			"context_tier":        spec.ContextTier,
			"files":               spec.Files,
			"constraints":         spec.Constraints,
			"acceptance_criteria": spec.AcceptanceCriteria,
			"interface_contract":  spec.InterfaceContract,
			"output_format":       outputFormat,
			"srs_refs":            spec.SRSRefs,
		},
	})

	e.log.Info("task created via control plane",
		"task_id", task.ID,
		"run_id", runID,
		"objective", spec.Objective,
	)

	return task, nil
}

// CreateTaskBatch creates multiple tasks in sequence for the same run. If any
// individual insert fails, previously-created tasks are returned alongside the
// error so the caller can surface partial progress. The operation is not
// strictly transactional because task insertion has side-effects beyond the
// tasks table (target files, SRS refs, events) — see Architecture §8.6 which
// treats each task as an independently recoverable unit.
func (e *Engine) CreateTaskBatch(runID string, specs []TaskCreateSpec) ([]*state.Task, error) {
	if len(specs) == 0 {
		return nil, errors.New("tasks batch is empty")
	}

	created := make([]*state.Task, 0, len(specs))
	for i, spec := range specs {
		task, err := e.CreateTask(runID, spec)
		if err != nil {
			return created, fmt.Errorf("batch task %d: %w", i, err)
		}
		created = append(created, task)
	}
	return created, nil
}

// ApproveTaskOutput enqueues an approved attempt into the merge queue.
// Per Architecture §16.4 the merge queue is the single commit-serialization
// point; the orchestrator gate (external or embedded) feeds approvals here.
//
// The attempt must already be in phase queued_for_merge / awaiting_orchestrator_gate;
// this method does not re-run validation or review. The merge queue itself
// takes care of base-snapshot staleness, integration checks, and commit.
func (e *Engine) ApproveTaskOutput(taskID, attemptID string) error {
	if taskID == "" || attemptID == "" {
		return errors.New("task_id and attempt_id are required")
	}
	if e.mergeQueue == nil {
		return errors.New("merge queue not initialized")
	}

	attemptIDInt, err := strconv.ParseInt(attemptID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid attempt_id %q: %w", attemptID, err)
	}

	task, err := e.db.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("loading task %s: %w", taskID, err)
	}

	attempt, err := e.db.GetAttempt(attemptIDInt)
	if err != nil {
		return fmt.Errorf("loading attempt %d: %w", attemptIDInt, err)
	}
	if attempt.TaskID != taskID {
		return fmt.Errorf("attempt %d does not belong to task %s", attemptIDInt, taskID)
	}

	// Transition the attempt into queued_for_merge if it is not already there.
	if attempt.Phase != state.PhaseQueuedForMerge {
		if err := e.db.UpdateAttemptPhase(attempt.ID, state.PhaseQueuedForMerge); err != nil {
			// Non-fatal: the merge queue will still receive the item, but log loudly.
			e.log.Warn("could not transition attempt to queued_for_merge",
				"task_id", taskID,
				"attempt_id", attempt.ID,
				"phase", attempt.Phase,
				"error", err)
		}
	}

	baseSnapshot := ""
	if task.BaseSnapshot != nil {
		baseSnapshot = *task.BaseSnapshot
	}
	if baseSnapshot == "" {
		baseSnapshot = attempt.BaseSnapshot
	}

	refs, _ := e.db.GetTaskSRSRefs(task.ID)

	e.EnqueueMerge(mergequeue.MergeItem{
		TaskID:       task.ID,
		RunID:        task.RunID,
		AttemptID:    attempt.ID,
		BaseSnapshot: baseSnapshot,
		CommitInfo: mergequeue.CommitInfo{
			TaskTitle:     task.Title,
			TaskID:        task.ID,
			SRSRefs:       refs,
			MeeseeksModel: attempt.ModelID,
			AttemptNumber: attempt.AttemptNumber,
			MaxAttempts:   attempt.AttemptNumber,
			CostUSD:       attempt.CostUSD,
			BaseSnapshot:  baseSnapshot,
		},
	})

	e.log.Info("task output approved and enqueued for merge",
		"task_id", taskID,
		"attempt_id", attempt.ID,
	)
	return nil
}

// RejectTaskOutput marks an attempt as rejected, stores the rejection reason
// as attempt feedback, and emits a task_failed event with the reason so the
// orchestrator can react. The task itself is transitioned to failed so the
// scheduler's retry/block machinery takes over per Architecture §30.1.
func (e *Engine) RejectTaskOutput(taskID, attemptID, reason string) error {
	if taskID == "" || attemptID == "" {
		return errors.New("task_id and attempt_id are required")
	}

	attemptIDInt, err := strconv.ParseInt(attemptID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid attempt_id %q: %w", attemptID, err)
	}

	task, err := e.db.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("loading task %s: %w", taskID, err)
	}

	attempt, err := e.db.GetAttempt(attemptIDInt)
	if err != nil {
		return fmt.Errorf("loading attempt %d: %w", attemptIDInt, err)
	}
	if attempt.TaskID != taskID {
		return fmt.Errorf("attempt %d does not belong to task %s", attemptIDInt, taskID)
	}

	feedback := reason
	if feedback == "" {
		feedback = "rejected by orchestrator"
	}

	// Record feedback + failure reason on the attempt so the next retry (if
	// any) sees it in its prior-feedback context block (see buildPriorFeedback).
	if _, err := e.db.Exec(`UPDATE task_attempts SET failure_reason = ?, feedback = ? WHERE id = ?`,
		feedback, feedback, attempt.ID); err != nil {
		return fmt.Errorf("recording rejection feedback: %w", err)
	}

	// Best-effort phase/status transition.
	if attempt.Phase != state.PhaseFailed && attempt.Phase != state.PhaseEscalated {
		if err := e.db.UpdateAttemptPhase(attempt.ID, state.PhaseFailed); err != nil {
			e.log.Warn("could not transition attempt phase to failed",
				"attempt_id", attempt.ID, "error", err)
		}
	}
	if attempt.Status == state.AttemptRunning {
		if err := e.db.UpdateAttemptStatus(attempt.ID, state.AttemptFailed); err != nil {
			e.log.Warn("could not transition attempt status to failed",
				"attempt_id", attempt.ID, "error", err)
		}
	}
	if task.Status == state.TaskInProgress {
		if err := e.db.UpdateTaskStatus(task.ID, state.TaskFailed); err != nil {
			e.log.Warn("could not transition task to failed",
				"task_id", task.ID, "error", err)
		}
	}

	e.emitEvent(events.EngineEvent{
		Type:   events.TaskFailed,
		RunID:  task.RunID,
		TaskID: task.ID,
		Details: map[string]any{
			"attempt_id": attempt.ID,
			"reason":     feedback,
			"source":     "orchestrator_reject",
		},
	})

	e.log.Info("task output rejected",
		"task_id", taskID,
		"attempt_id", attempt.ID,
		"reason", feedback,
	)
	return nil
}
