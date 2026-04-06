package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	"github.com/openaxiom/axiom/internal/state"
)

// ModelSelector picks a model for a given tier.
// The excludeFamily parameter is used for reviewer diversification (Section 30.1).
type ModelSelector interface {
	SelectModel(ctx context.Context, tier state.TaskTier, excludeFamily string) (modelID, modelFamily string, err error)
}

// SnapshotProvider provides the current HEAD SHA for base_snapshot pinning.
// Per Architecture Section 16.2.
type SnapshotProvider interface {
	CurrentHEAD() (string, error)
}

// FamilyExcluder provides model family exclusion for test-generation separation.
// Per Architecture Section 11.5: test tasks must use a different model family
// than the implementation task that produced the code under test.
type FamilyExcluder interface {
	GetExcludeFamily(ctx context.Context, taskID string) (string, error)
}

// Options configures a new Scheduler.
type Options struct {
	DB               *state.DB
	Log              *slog.Logger
	MaxMeeseeks      int
	ModelSelector    ModelSelector
	SnapshotProvider SnapshotProvider
	FamilyExcluder   FamilyExcluder
}

// Scheduler manages the dispatch loop for task execution.
// Per Architecture Section 15 and the Phase 10 implementation plan:
// - Finds dependency-free queued tasks
// - Acquires write-set locks atomically in deterministic order
// - Moves tasks to in_progress and creates attempt records
// - Manages waiting_on_lock transitions and requeue on lock release
type Scheduler struct {
	db             *state.DB
	log            *slog.Logger
	maxMeeseeks    int
	modelSelector  ModelSelector
	snapshotProv   SnapshotProvider
	familyExcluder FamilyExcluder
}

// New creates a new Scheduler.
func New(opts Options) *Scheduler {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.MaxMeeseeks <= 0 {
		opts.MaxMeeseeks = 3
	}
	return &Scheduler{
		db:             opts.DB,
		log:            opts.Log,
		maxMeeseeks:    opts.MaxMeeseeks,
		modelSelector:  opts.ModelSelector,
		snapshotProv:   opts.SnapshotProvider,
		familyExcluder: opts.FamilyExcluder,
	}
}

// lockRequest describes a single lock to be acquired.
type lockRequest struct {
	ResourceType string
	ResourceKey  string
}

// Tick runs one scheduler iteration across all active runs.
// Per Architecture Section 15.4 and 16.3:
//  1. Find all active runs.
//  2. For each run, find queued tasks whose dependencies are all done.
//  3. Acquire lock sets atomically (deterministic order, all-or-nothing).
//  4. Dispatch ready tasks up to the concurrency limit.
func (s *Scheduler) Tick(ctx context.Context) error {
	runs, err := s.activeRuns()
	if err != nil {
		return fmt.Errorf("listing active runs: %w", err)
	}

	for _, runID := range runs {
		if err := s.tickRun(ctx, runID); err != nil {
			s.log.Warn("scheduler tick error for run", "run_id", runID, "error", err)
		}
	}
	return nil
}

// tickRun processes one active run.
func (s *Scheduler) tickRun(ctx context.Context, runID string) error {
	// Count currently in-progress tasks
	inProgress, err := s.db.ListTasksByStatus(runID, state.TaskInProgress)
	if err != nil {
		return fmt.Errorf("counting in-progress tasks: %w", err)
	}
	available := s.maxMeeseeks - len(inProgress)
	if available <= 0 {
		return nil
	}

	// Find queued tasks with all deps satisfied
	ready, err := s.findReadyTasks(runID)
	if err != nil {
		return fmt.Errorf("finding ready tasks: %w", err)
	}

	dispatched := 0
	for _, task := range ready {
		if dispatched >= available {
			break
		}

		ok, err := s.tryDispatch(ctx, &task)
		if err != nil {
			s.log.Warn("dispatch error", "task_id", task.ID, "error", err)
			continue
		}
		if ok {
			dispatched++
		}
	}

	return nil
}

// findReadyTasks returns queued tasks whose dependencies are all done.
// Per Architecture Section 15.5: a task SHALL NOT transition from queued
// to in_progress unless ALL tasks in its dependency set have status done.
func (s *Scheduler) findReadyTasks(runID string) ([]state.Task, error) {
	queued, err := s.db.ListTasksByStatus(runID, state.TaskQueued)
	if err != nil {
		return nil, err
	}

	var ready []state.Task
	for _, task := range queued {
		deps, err := s.db.GetTaskDependencies(task.ID)
		if err != nil {
			return nil, fmt.Errorf("getting deps for %s: %w", task.ID, err)
		}

		allDone := true
		for _, depID := range deps {
			dep, err := s.db.GetTask(depID)
			if err != nil {
				return nil, fmt.Errorf("getting dep task %s: %w", depID, err)
			}
			if dep.Status != state.TaskDone {
				allDone = false
				break
			}
		}

		if allDone {
			ready = append(ready, task)
		}
	}
	return ready, nil
}

// tryDispatch attempts to acquire locks and dispatch a single task.
// Returns true if the task was dispatched, false if it was moved to waiting_on_lock.
func (s *Scheduler) tryDispatch(ctx context.Context, task *state.Task) (bool, error) {
	// Get target files for lock acquisition
	targetFiles, err := s.db.GetTaskTargetFiles(task.ID)
	if err != nil {
		return false, fmt.Errorf("getting target files: %w", err)
	}

	// Build lock requests from target files
	var lockReqs []lockRequest
	for _, tf := range targetFiles {
		lockReqs = append(lockReqs, lockRequest{
			ResourceType: tf.LockScope,
			ResourceKey:  tf.LockResourceKey,
		})
	}

	// Try atomic lock acquisition
	if len(lockReqs) > 0 {
		ok, blockedBy, err := s.tryAcquireLocks(task.ID, lockReqs)
		if err != nil {
			return false, fmt.Errorf("acquiring locks: %w", err)
		}
		if !ok {
			// Move to waiting_on_lock
			if err := s.moveToWaitingOnLock(task, lockReqs, blockedBy); err != nil {
				return false, err
			}
			return false, nil
		}
	}

	// Dispatch the task
	if err := s.dispatch(ctx, task); err != nil {
		// Release any acquired locks on dispatch failure
		s.db.ReleaseTaskLocks(task.ID)
		return false, fmt.Errorf("dispatching: %w", err)
	}

	return true, nil
}

// errLockConflict is a sentinel error used to trigger transaction rollback
// when a lock conflict is detected during atomic acquisition.
var errLockConflict = fmt.Errorf("lock conflict")

// tryAcquireLocks attempts atomic lock acquisition in deterministic order.
// Per Architecture Section 16.3: all locks in alphabetical order by (resource_type, resource_key),
// acquire ALL or acquire NONE.
func (s *Scheduler) tryAcquireLocks(taskID string, reqs []lockRequest) (bool, string, error) {
	sorted := sortLockRequests(reqs)
	var conflictHolder string

	txErr := s.db.WithTx(func(tx *sql.Tx) error {
		for _, req := range sorted {
			var holder string
			err := tx.QueryRow(
				`SELECT task_id FROM task_locks WHERE resource_type = ? AND resource_key = ?`,
				req.ResourceType, req.ResourceKey,
			).Scan(&holder)

			if err == nil {
				// Lock is held by someone else
				if holder != taskID {
					conflictHolder = holder
					return errLockConflict // triggers rollback — atomic all-or-nothing
				}
				// Already held by us — skip
				continue
			}
			if err != sql.ErrNoRows {
				return fmt.Errorf("checking lock: %w", err)
			}

			// Lock is free — acquire it
			_, err = tx.Exec(
				`INSERT INTO task_locks (resource_type, resource_key, task_id) VALUES (?, ?, ?)`,
				req.ResourceType, req.ResourceKey, taskID,
			)
			if err != nil {
				return fmt.Errorf("acquiring lock: %w", err)
			}
		}
		return nil
	})

	if txErr == errLockConflict {
		return false, conflictHolder, nil
	}
	if txErr != nil {
		return false, "", txErr
	}
	return true, "", nil
}

// dispatch moves a task to in_progress and creates a new attempt record.
func (s *Scheduler) dispatch(ctx context.Context, task *state.Task) error {
	// Determine model family exclusion for test-generation separation (Section 11.5)
	var excludeFamily string
	if s.familyExcluder != nil {
		ef, err := s.familyExcluder.GetExcludeFamily(ctx, task.ID)
		if err != nil {
			s.log.Warn("failed to get exclude family", "task_id", task.ID, "error", err)
		} else {
			excludeFamily = ef
		}
	}

	// Select model for this tier
	modelID, modelFamily, err := s.modelSelector.SelectModel(ctx, task.Tier, excludeFamily)
	if err != nil {
		return fmt.Errorf("selecting model: %w", err)
	}

	// Get current HEAD for base_snapshot
	snapshot, err := s.snapshotProv.CurrentHEAD()
	if err != nil {
		return fmt.Errorf("getting current HEAD: %w", err)
	}

	// Determine attempt number
	attempts, err := s.db.ListAttemptsByTask(task.ID)
	if err != nil {
		return fmt.Errorf("listing attempts: %w", err)
	}
	attemptNum := len(attempts) + 1

	// Transition to in_progress
	if err := s.db.UpdateTaskStatus(task.ID, state.TaskInProgress); err != nil {
		return fmt.Errorf("updating task status: %w", err)
	}

	// Create attempt record with task's current tier for per-tier retry counting
	_, err = s.db.CreateAttempt(&state.TaskAttempt{
		TaskID:        task.ID,
		AttemptNumber: attemptNum,
		ModelID:       modelID,
		ModelFamily:   modelFamily,
		Tier:          task.Tier,
		BaseSnapshot:  snapshot,
		Status:        state.AttemptRunning,
		Phase:         state.PhaseExecuting,
	})
	if err != nil {
		return fmt.Errorf("creating attempt: %w", err)
	}

	s.log.Info("task dispatched",
		"task_id", task.ID,
		"attempt", attemptNum,
		"model", modelID,
		"snapshot", snapshot,
	)

	return nil
}

// moveToWaitingOnLock transitions a task to waiting_on_lock and records the wait.
func (s *Scheduler) moveToWaitingOnLock(task *state.Task, reqs []lockRequest, blockedBy string) error {
	if err := s.db.UpdateTaskStatus(task.ID, state.TaskWaitingOnLock); err != nil {
		return fmt.Errorf("moving to waiting_on_lock: %w", err)
	}

	// Build requested resources JSON
	type lockRes struct {
		ResourceType string `json:"resource_type"`
		ResourceKey  string `json:"resource_key"`
	}
	var requested []lockRes
	for _, r := range reqs {
		requested = append(requested, lockRes{
			ResourceType: r.ResourceType,
			ResourceKey:  r.ResourceKey,
		})
	}
	reqJSON, _ := json.Marshal(requested)

	var blockedByPtr *string
	if blockedBy != "" {
		blockedByPtr = &blockedBy
	}

	if err := s.db.AddLockWait(&state.TaskLockWait{
		TaskID:             task.ID,
		WaitReason:         "initial_dispatch",
		RequestedResources: string(reqJSON),
		BlockedByTaskID:    blockedByPtr,
	}); err != nil {
		return fmt.Errorf("adding lock wait: %w", err)
	}

	s.log.Info("task waiting on lock",
		"task_id", task.ID,
		"blocked_by", blockedBy,
	)

	return nil
}

// ReleaseLocks releases all locks held by a task and processes lock waiters.
// Called when a task completes, fails, or is cancelled.
func (s *Scheduler) ReleaseLocks(_ context.Context, taskID string) error {
	if err := s.db.ReleaseTaskLocks(taskID); err != nil {
		return fmt.Errorf("releasing locks: %w", err)
	}

	// Process waiters that may be unblocked
	return s.processLockWaiters()
}

// processLockWaiters checks all waiting tasks and requeues those whose locks are available.
// Per Architecture Section 22.3 step 3: rebuild lock waits.
func (s *Scheduler) processLockWaiters() error {
	// Get all lock waits across all runs
	// We need to query across runs, so we scan the task_lock_waits table directly
	rows, err := s.db.Query(`SELECT w.task_id, w.wait_reason, w.requested_resources, w.blocked_by_task_id
		FROM task_lock_waits w
		JOIN tasks t ON t.id = w.task_id
		WHERE t.status = ?`, string(state.TaskWaitingOnLock))
	if err != nil {
		return fmt.Errorf("listing lock waits: %w", err)
	}
	defer rows.Close()

	type waiterInfo struct {
		taskID    string
		resources string
	}
	var waiters []waiterInfo

	for rows.Next() {
		var taskID, reason, resources string
		var blockedBy *string
		if err := rows.Scan(&taskID, &reason, &resources, &blockedBy); err != nil {
			return fmt.Errorf("scanning lock wait: %w", err)
		}
		waiters = append(waiters, waiterInfo{taskID: taskID, resources: resources})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, w := range waiters {
		// Parse requested resources
		type lockRes struct {
			ResourceType string `json:"resource_type"`
			ResourceKey  string `json:"resource_key"`
		}
		var requested []lockRes
		if err := json.Unmarshal([]byte(w.resources), &requested); err != nil {
			s.log.Warn("failed to parse lock wait resources", "task_id", w.taskID, "error", err)
			continue
		}

		// Check if all requested resources are now available
		allFree := true
		for _, r := range requested {
			var holder string
			err := s.db.QueryRow(
				`SELECT task_id FROM task_locks WHERE resource_type = ? AND resource_key = ?`,
				r.ResourceType, r.ResourceKey,
			).Scan(&holder)
			if err == nil && holder != w.taskID {
				allFree = false
				break
			}
			if err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("checking lock for waiter: %w", err)
			}
		}

		if allFree {
			// Requeue the task
			if err := s.db.UpdateTaskStatus(w.taskID, state.TaskQueued); err != nil {
				s.log.Warn("failed to requeue waiter", "task_id", w.taskID, "error", err)
				continue
			}
			if err := s.db.RemoveLockWait(w.taskID); err != nil {
				s.log.Warn("failed to remove lock wait", "task_id", w.taskID, "error", err)
			}
			s.log.Info("lock waiter requeued", "task_id", w.taskID)
		}
	}

	return nil
}

// activeRuns returns the IDs of all runs with status "active".
func (s *Scheduler) activeRuns() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT id FROM project_runs WHERE status = ?`,
		string(state.RunActive),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// sortLockRequests returns a copy of requests sorted by (resource_type, resource_key).
// Per Architecture Section 16.3: deterministic lock acquisition order prevents deadlocks.
func sortLockRequests(reqs []lockRequest) []lockRequest {
	sorted := make([]lockRequest, len(reqs))
	copy(sorted, reqs)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ResourceType != sorted[j].ResourceType {
			return sorted[i].ResourceType < sorted[j].ResourceType
		}
		return sorted[i].ResourceKey < sorted[j].ResourceKey
	})
	return sorted
}
