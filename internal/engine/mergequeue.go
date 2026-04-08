package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/ipc"
	"github.com/openaxiom/axiom/internal/mergequeue"
	"github.com/openaxiom/axiom/internal/scheduler"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/openaxiom/axiom/internal/testgen"
)

// mergeQueueLoop is the engine worker that processes the merge queue each tick.
// Per Architecture Section 16.4: the merge queue processes one approved output
// at a time, serializing all commits.
func (e *Engine) mergeQueueLoop(ctx context.Context) error {
	return e.mergeQueue.Tick(ctx)
}

// EnqueueMerge submits an approved task output to the merge queue.
// Called after the orchestrator gate approves the output.
func (e *Engine) EnqueueMerge(item mergequeue.MergeItem) {
	e.mergeQueue.Enqueue(item)
}

// MergeQueueLen returns the number of items waiting in the merge queue.
func (e *Engine) MergeQueueLen() int {
	return e.mergeQueue.Len()
}

// --- Adapters bridging engine interfaces to merge queue interfaces ---

// mergeQueueGitAdapter adapts the engine's GitService to the merge queue's GitOps interface.
type mergeQueueGitAdapter struct {
	git     GitService
	rootDir string
}

func (a *mergeQueueGitAdapter) CurrentHEAD(dir string) (string, error) {
	if a.git == nil {
		return "unknown", nil
	}
	if dir == "" {
		dir = a.rootDir
	}
	return a.git.CurrentHEAD(dir)
}

func (a *mergeQueueGitAdapter) AddFiles(dir string, files []string) error {
	if a.git == nil {
		return nil
	}
	if dir == "" {
		dir = a.rootDir
	}
	return a.git.AddFiles(dir, files)
}

func (a *mergeQueueGitAdapter) Commit(dir string, message string) (string, error) {
	if a.git == nil {
		return "unknown", nil
	}
	if dir == "" {
		dir = a.rootDir
	}
	return a.git.Commit(dir, message)
}

func (a *mergeQueueGitAdapter) ChangedFilesSince(dir, sinceRef string) ([]string, error) {
	if a.git == nil {
		return nil, nil
	}
	if dir == "" {
		dir = a.rootDir
	}
	return a.git.ChangedFilesSince(dir, sinceRef)
}

// mergeQueueValidatorAdapter runs project-wide integration checks.
// Per Architecture Section 23.3.
type mergeQueueValidatorAdapter struct {
	validation ValidationService
	cfg        *config.Config
	log        *slog.Logger
}

func (a *mergeQueueValidatorAdapter) RunIntegrationChecks(ctx context.Context, projectDir string) (bool, string, error) {
	if a.validation == nil {
		if a.log != nil {
			a.log.Warn("merge queue validation unavailable; falling back to pass-through")
		}
		return true, "", nil
	}
	var image string
	var validationCfg *config.ValidationConfig
	if a.cfg != nil {
		image = a.cfg.Docker.Image
		validationCfg = &a.cfg.Validation
	}
	results, err := a.validation.RunChecks(ctx, ValidationCheckRequest{
		TaskID:     "merge-queue",
		RunID:      "",
		Image:      image,
		StagingDir: "",
		ProjectDir: projectDir,
		Config:     validationCfg,
		Languages:  detectValidationLanguages(projectDir),
	})
	if err != nil {
		return false, "", err
	}
	return validationAllPassed(results), formatValidationResults(results), nil
}

// mergeQueueIndexAdapter adapts the engine's IndexService to the merge queue's Indexer interface.
type mergeQueueIndexAdapter struct {
	index IndexService
}

func (a *mergeQueueIndexAdapter) IndexFiles(ctx context.Context, dir string, paths []string) error {
	if a.index == nil {
		return nil
	}
	return a.index.IndexFiles(ctx, dir, paths)
}

// mergeQueueLockAdapter adapts the scheduler's lock release to the merge queue's LockReleaser.
type mergeQueueLockAdapter struct {
	sched *scheduler.Scheduler
}

func (a *mergeQueueLockAdapter) ReleaseLocks(ctx context.Context, taskID string) error {
	return a.sched.ReleaseLocks(ctx, taskID)
}

// mergeQueueTaskAdapter handles task lifecycle operations for the merge queue.
// Per Architecture Section 11.5 it is also the single engine-side hook point
// for test-generation lifecycle transitions: CompleteTask fires CreateTestTask /
// MarkConverged and RequeueTask fires HandleTestFailure for test-type tasks.
type mergeQueueTaskAdapter struct {
	db      *state.DB
	sched   *scheduler.Scheduler
	testGen *testgen.Service
	log     *slog.Logger
}

// CompleteTask marks a task as done and dispatches test-generation lifecycle
// hooks per Architecture Section 11.5:
//   - implementation merge  → spawn a test-generation task via testgen.CreateTestTask
//   - test-task merge       → mark the convergence pair converged
//   - fix-task merge        → mark the original impl's convergence pair converged
//
// Dependent tasks are automatically unblocked by the scheduler's findReadyTasks
// check (Architecture Section 15.5). Any hook error is logged but not returned:
// the merge itself already committed to git and must not be rolled back because
// of a downstream test-generation glitch.
func (a *mergeQueueTaskAdapter) CompleteTask(ctx context.Context, taskID string) error {
	if err := a.db.UpdateTaskStatus(taskID, state.TaskDone); err != nil {
		return err
	}
	if a.testGen == nil {
		return nil
	}

	task, err := a.db.GetTask(taskID)
	if err != nil {
		if a.log != nil {
			a.log.Warn("testgen hook: get task failed", "task_id", taskID, "error", err)
		}
		return nil
	}

	switch task.TaskType {
	case state.TaskTypeImplementation:
		a.dispatchImplementationMerge(ctx, task)
	case state.TaskTypeTest:
		a.dispatchTestMerge(ctx, taskID)
	}
	return nil
}

// dispatchImplementationMerge is called when an implementation-type task merges.
// It distinguishes regular impl tasks (which spawn a new test task) from fix
// tasks (which must mark the pre-existing convergence pair converged). Fix
// tasks are recognised by being the fix_task_id of an existing pair in the
// same run.
func (a *mergeQueueTaskAdapter) dispatchImplementationMerge(ctx context.Context, task *state.Task) {
	pairs, err := a.db.ListConvergencePairsByRun(task.RunID)
	if err != nil {
		if a.log != nil {
			a.log.Warn("testgen hook: list convergence pairs failed",
				"run_id", task.RunID, "error", err)
		}
		return
	}
	for _, cp := range pairs {
		if cp.FixTaskID != nil && *cp.FixTaskID == task.ID {
			if err := a.testGen.MarkConverged(ctx, cp.ImplTaskID); err != nil && a.log != nil {
				a.log.Warn("testgen MarkConverged (fix path) failed",
					"impl_task_id", cp.ImplTaskID,
					"fix_task_id", task.ID,
					"error", err)
			}
			return
		}
	}

	// Not a fix task — spawn test generation for this impl.
	if _, err := a.testGen.CreateTestTask(ctx, task.ID); err != nil && a.log != nil {
		a.log.Info("testgen CreateTestTask skipped or failed",
			"task_id", task.ID, "error", err)
	}
}

// dispatchTestMerge is called when a test-type task merges successfully —
// i.e. its generated tests passed the merge-queue integration checks against
// committed implementation code. The feature is now converged.
func (a *mergeQueueTaskAdapter) dispatchTestMerge(ctx context.Context, taskID string) {
	cp, err := a.db.GetConvergencePairByTestTask(taskID)
	if err != nil {
		if a.log != nil {
			a.log.Warn("testgen hook: no convergence pair for test task",
				"task_id", taskID, "error", err)
		}
		return
	}
	if err := a.testGen.MarkConverged(ctx, cp.ImplTaskID); err != nil && a.log != nil {
		a.log.Warn("testgen MarkConverged (test path) failed",
			"impl_task_id", cp.ImplTaskID,
			"test_task_id", taskID,
			"error", err)
	}
}

// RequeueTask handles merge-queue integration-check failures. For implementation
// and review tasks this is the standard in_progress → failed → queued transition
// with failure feedback captured on the latest attempt. For test-type tasks,
// Architecture Section 11.5 requires a different flow: the test task stays in
// `failed` and HandleTestFailure spawns an implementation-fix task that will
// receive the test code and failure output via its dependency chain.
func (a *mergeQueueTaskAdapter) RequeueTask(ctx context.Context, taskID string, feedback string) error {
	task, err := a.db.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("getting task for requeue: %w", err)
	}

	// Store feedback on the latest attempt so the next TaskSpec (or the fix
	// TaskSpec) includes it. Per Architecture Sections 23.3, 30.2, and 11.5.
	attempts, err := a.db.ListAttemptsByTask(taskID)
	if err == nil && len(attempts) > 0 {
		latest := attempts[len(attempts)-1]
		a.db.Exec(`UPDATE task_attempts SET feedback = ? WHERE id = ?`, feedback, latest.ID)
	}

	if a.log != nil {
		a.log.Info("handling merge-queue failure",
			"task_id", taskID,
			"task_type", task.TaskType,
			"feedback_len", len(feedback),
		)
	}

	// Architecture §11.5: a failed test-task merge does NOT retry the test
	// meeseeks. Instead, create an implementation-fix task via testgen.
	if task.TaskType == state.TaskTypeTest && a.testGen != nil {
		if task.Status == state.TaskInProgress {
			if err := a.db.UpdateTaskStatus(taskID, state.TaskFailed); err != nil {
				return fmt.Errorf("failing test task: %w", err)
			}
		}
		if _, err := a.testGen.HandleTestFailure(ctx, taskID, feedback); err != nil {
			if a.log != nil {
				a.log.Error("testgen HandleTestFailure failed",
					"task_id", taskID, "error", err)
			}
			return fmt.Errorf("handling test failure for %s: %w", taskID, err)
		}
		return nil
	}

	// Standard requeue path for implementation / review tasks.
	if task.Status == state.TaskInProgress {
		if err := a.db.UpdateTaskStatus(taskID, state.TaskFailed); err != nil {
			return fmt.Errorf("failing task: %w", err)
		}
	}
	if err := a.db.UpdateTaskStatus(taskID, state.TaskQueued); err != nil {
		return fmt.Errorf("requeuing task: %w", err)
	}
	return nil
}

// mergeQueueEventAdapter adapts the engine's event bus for merge queue events.
type mergeQueueEventAdapter struct {
	bus *events.Bus
}

func (a *mergeQueueEventAdapter) Emit(eventType string, taskID string, details map[string]any) {
	a.bus.Publish(events.EngineEvent{
		Type:    events.EventType(eventType),
		TaskID:  taskID,
		Details: details,
	})
}

// mergeQueueAttemptAdapter persists attempt phase/status updates during merge processing.
type mergeQueueAttemptAdapter struct {
	db      *state.DB
	rootDir string
	log     *slog.Logger
}

func (a *mergeQueueAttemptAdapter) MarkMerging(_ context.Context, attemptID int64) error {
	return a.db.UpdateAttemptPhase(attemptID, state.PhaseMerging)
}

func (a *mergeQueueAttemptAdapter) MarkSucceeded(_ context.Context, attemptID int64) error {
	if err := a.db.UpdateAttemptPhase(attemptID, state.PhaseSucceeded); err != nil {
		return err
	}
	if err := a.db.UpdateAttemptStatus(attemptID, state.AttemptPassed); err != nil {
		return err
	}
	return a.cleanupAttemptDirs(attemptID)
}

func (a *mergeQueueAttemptAdapter) MarkFailed(_ context.Context, attemptID int64, feedback string) error {
	attempt, err := a.db.GetAttempt(attemptID)
	if err != nil {
		return err
	}
	if err := a.db.UpdateAttemptPhase(attemptID, state.PhaseFailed); err != nil {
		return err
	}
	if err := a.db.UpdateAttemptStatus(attemptID, state.AttemptFailed); err != nil {
		return err
	}
	if _, err := a.db.Exec(`UPDATE task_attempts SET failure_reason = ?, feedback = ? WHERE id = ?`, feedback, feedback, attemptID); err != nil {
		return err
	}
	return ipc.CleanupTaskDirs(a.rootDir, attempt.TaskID)
}

func (a *mergeQueueAttemptAdapter) cleanupAttemptDirs(attemptID int64) error {
	attempt, err := a.db.GetAttempt(attemptID)
	if err != nil {
		return err
	}
	return ipc.CleanupTaskDirs(a.rootDir, attempt.TaskID)
}
