package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/mergequeue"
	"github.com/openaxiom/axiom/internal/scheduler"
	"github.com/openaxiom/axiom/internal/state"
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
	log *slog.Logger
}

func (a *mergeQueueValidatorAdapter) RunIntegrationChecks(_ context.Context, _ string) (bool, string, error) {
	// Integration check execution will be fully wired when the validation
	// service is connected to real Docker containers in a future phase.
	if a.log != nil {
		a.log.Warn("merge queue integration checks using stub validator — no real checks running")
	}
	return true, "", nil
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
type mergeQueueTaskAdapter struct {
	db    *state.DB
	sched *scheduler.Scheduler
	log   *slog.Logger
}

// CompleteTask marks a task as done. Dependent tasks are automatically unblocked
// by the scheduler's findReadyTasks check, which verifies all dependencies have
// status "done" before dispatching a queued task (Architecture Section 15.5).
func (a *mergeQueueTaskAdapter) CompleteTask(_ context.Context, taskID string) error {
	return a.db.UpdateTaskStatus(taskID, state.TaskDone)
}

func (a *mergeQueueTaskAdapter) RequeueTask(_ context.Context, taskID string, feedback string) error {
	task, err := a.db.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("getting task for requeue: %w", err)
	}

	// Store feedback on the latest attempt so the next TaskSpec includes it.
	// Per Architecture Sections 23.3 and 30.2: requeued tasks include failure details.
	attempts, err := a.db.ListAttemptsByTask(taskID)
	if err == nil && len(attempts) > 0 {
		latest := attempts[len(attempts)-1]
		a.db.Exec(`UPDATE task_attempts SET feedback = ? WHERE id = ?`, feedback, latest.ID)
	}

	if a.log != nil {
		a.log.Info("requeuing task with integration failure feedback",
			"task_id", taskID,
			"feedback_len", len(feedback),
		)
	}

	// If task is in_progress, fail it first, then requeue
	if task.Status == state.TaskInProgress {
		if err := a.db.UpdateTaskStatus(taskID, state.TaskFailed); err != nil {
			return fmt.Errorf("failing task: %w", err)
		}
	}

	// Requeue: failed → queued
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
