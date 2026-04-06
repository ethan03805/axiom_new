package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/openaxiom/axiom/internal/state"
)

// Configuration constants per Architecture Section 30.1.
const (
	// MaxRetriesPerTier is the max retry attempts at the same model tier before escalation.
	MaxRetriesPerTier = 3
	// MaxEscalations is the max number of tier escalations before blocking.
	MaxEscalations = 2
)

// FailureAction indicates what action was taken for a failed task.
type FailureAction int

const (
	ActionRetry    FailureAction = iota // Same tier, fresh container
	ActionEscalate                      // Next higher tier
	ActionBlock                         // Orchestrator intervention required
)

// tierEscalation defines the escalation chain per Architecture Section 30.1.
// local → cheap → standard → premium
var tierEscalation = map[state.TaskTier]state.TaskTier{
	state.TierLocal:    state.TierCheap,
	state.TierCheap:    state.TierStandard,
	state.TierStandard: state.TierPremium,
}

// CreateTaskInput describes a task to be created.
type CreateTaskInput struct {
	ID           string
	RunID        string
	ParentID     *string
	Title        string
	Description  *string
	Tier         state.TaskTier
	TaskType     state.TaskType
	BaseSnapshot *string
	SRSRefs      []string
	TargetFiles  []TargetFileInput
	DependsOn    []string
}

// TargetFileInput describes a file targeted by a task.
type TargetFileInput struct {
	FilePath        string
	LockScope       string // file | package | module | schema
	LockResourceKey string // canonical identifier
}

// Service provides task lifecycle operations on top of the state layer.
type Service struct {
	db  *state.DB
	log *slog.Logger
}

// New creates a new task service.
func New(db *state.DB, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, log: log}
}

// CreateTask creates a single task with its associated metadata in a single transaction.
func (s *Service) CreateTask(_ context.Context, input CreateTaskInput) (*state.Task, error) {
	if err := validateInput(input); err != nil {
		return nil, err
	}

	task := &state.Task{
		ID:           input.ID,
		RunID:        input.RunID,
		ParentID:     input.ParentID,
		Title:        input.Title,
		Description:  input.Description,
		Status:       state.TaskQueued,
		Tier:         input.Tier,
		TaskType:     input.TaskType,
		BaseSnapshot: input.BaseSnapshot,
	}

	err := s.db.WithTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO tasks
			(id, run_id, parent_id, title, description, status, tier, task_type, base_snapshot, eco_ref)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			task.ID, task.RunID, task.ParentID, task.Title, task.Description,
			string(task.Status), string(task.Tier), string(task.TaskType), task.BaseSnapshot, task.ECORef)
		if err != nil {
			return fmt.Errorf("creating task: %w", err)
		}

		for _, ref := range input.SRSRefs {
			if _, err := tx.Exec(`INSERT INTO task_srs_refs (task_id, srs_ref) VALUES (?, ?)`,
				input.ID, ref); err != nil {
				return fmt.Errorf("adding SRS ref %q: %w", ref, err)
			}
		}

		for _, tf := range input.TargetFiles {
			if _, err := tx.Exec(`INSERT INTO task_target_files (task_id, file_path, lock_scope, lock_resource_key)
				VALUES (?, ?, ?, ?)`,
				input.ID, tf.FilePath, tf.LockScope, tf.LockResourceKey); err != nil {
				return fmt.Errorf("adding target file %q: %w", tf.FilePath, err)
			}
		}

		for _, dep := range input.DependsOn {
			if _, err := tx.Exec(`INSERT INTO task_dependencies (task_id, depends_on) VALUES (?, ?)`,
				input.ID, dep); err != nil {
				return fmt.Errorf("adding dependency %q: %w", dep, err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return task, nil
}

// CreateBatch creates multiple tasks transactionally with dependency validation.
// All tasks must be in the same run. Cycles are rejected.
func (s *Service) CreateBatch(ctx context.Context, inputs []CreateTaskInput) ([]*state.Task, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	// Validate all inputs
	for _, in := range inputs {
		if err := validateInput(in); err != nil {
			return nil, fmt.Errorf("task %q: %w", in.ID, err)
		}
	}

	// Build an ID set and validate dependencies
	if err := s.validateBatchDependencies(inputs); err != nil {
		return nil, err
	}

	// Create all tasks in a single transaction
	var tasks []*state.Task
	err := s.db.WithTx(func(tx *sql.Tx) error {
		for _, input := range inputs {
			task := &state.Task{
				ID:           input.ID,
				RunID:        input.RunID,
				ParentID:     input.ParentID,
				Title:        input.Title,
				Description:  input.Description,
				Status:       state.TaskQueued,
				Tier:         input.Tier,
				TaskType:     input.TaskType,
				BaseSnapshot: input.BaseSnapshot,
			}

			_, err := tx.Exec(`INSERT INTO tasks
				(id, run_id, parent_id, title, description, status, tier, task_type, base_snapshot, eco_ref)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				task.ID, task.RunID, task.ParentID, task.Title, task.Description,
				string(task.Status), string(task.Tier), string(task.TaskType), task.BaseSnapshot, task.ECORef)
			if err != nil {
				return fmt.Errorf("creating task %q: %w", input.ID, err)
			}

			for _, ref := range input.SRSRefs {
				if _, err := tx.Exec(`INSERT INTO task_srs_refs (task_id, srs_ref) VALUES (?, ?)`,
					input.ID, ref); err != nil {
					return fmt.Errorf("adding SRS ref %q for task %q: %w", ref, input.ID, err)
				}
			}

			for _, tf := range input.TargetFiles {
				if _, err := tx.Exec(`INSERT INTO task_target_files (task_id, file_path, lock_scope, lock_resource_key)
					VALUES (?, ?, ?, ?)`,
					input.ID, tf.FilePath, tf.LockScope, tf.LockResourceKey); err != nil {
					return fmt.Errorf("adding target file %q for task %q: %w", tf.FilePath, input.ID, err)
				}
			}

			for _, dep := range input.DependsOn {
				if _, err := tx.Exec(`INSERT INTO task_dependencies (task_id, depends_on) VALUES (?, ?)`,
					input.ID, dep); err != nil {
					return fmt.Errorf("adding dependency %q for task %q: %w", dep, input.ID, err)
				}
			}

			tasks = append(tasks, task)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return tasks, nil
}

// validateBatchDependencies validates that all dependencies are within the batch
// (or already exist in the same run) and that there are no cycles.
func (s *Service) validateBatchDependencies(inputs []CreateTaskInput) error {
	idSet := make(map[string]bool, len(inputs))
	for _, in := range inputs {
		idSet[in.ID] = true
	}

	// Check that all dependencies reference known IDs (within batch or existing in DB)
	for _, in := range inputs {
		for _, dep := range in.DependsOn {
			if dep == in.ID {
				return fmt.Errorf("task %q has self-dependency", in.ID)
			}
			if !idSet[dep] {
				// Check if it exists in the DB already
				_, err := s.db.GetTask(dep)
				if err != nil {
					return fmt.Errorf("task %q depends on unknown task %q", in.ID, dep)
				}
			}
		}
	}

	// Cycle detection via DFS on the batch graph
	return detectCycle(inputs)
}

// detectCycle uses DFS to find cycles in the dependency graph.
func detectCycle(inputs []CreateTaskInput) error {
	// Build adjacency list: task → dependencies
	adj := make(map[string][]string)
	for _, in := range inputs {
		adj[in.ID] = in.DependsOn
	}

	const (
		white = 0 // unvisited
		gray  = 1 // in current DFS path
		black = 2 // fully explored
	)

	color := make(map[string]int)
	for _, in := range inputs {
		color[in.ID] = white
	}

	var dfs func(node string) error
	dfs = func(node string) error {
		color[node] = gray
		for _, dep := range adj[node] {
			c, exists := color[dep]
			if !exists {
				// Dependency is outside the batch — no cycle possible through it
				continue
			}
			if c == gray {
				return fmt.Errorf("circular dependency detected involving task %q", dep)
			}
			if c == white {
				if err := dfs(dep); err != nil {
					return err
				}
			}
		}
		color[node] = black
		return nil
	}

	for _, in := range inputs {
		if color[in.ID] == white {
			if err := dfs(in.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// RetryTask requeues a failed task for retry at the same tier.
// Per Architecture Section 30.1 Tier 1: same model, fresh container.
func (s *Service) RetryTask(_ context.Context, taskID string, feedback string) error {
	task, err := s.db.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("getting task: %w", err)
	}
	if task.Status != state.TaskFailed {
		return fmt.Errorf("cannot retry task in status %s (must be failed)", task.Status)
	}

	// Transition failed → queued
	if err := s.db.UpdateTaskStatus(taskID, state.TaskQueued); err != nil {
		return fmt.Errorf("requeuing task: %w", err)
	}

	s.log.Info("task retried", "task_id", taskID, "feedback", feedback)
	return nil
}

// EscalateTask moves a failed task to the next higher model tier.
// Per Architecture Section 30.1 Tier 2: better model, fresh container.
func (s *Service) EscalateTask(_ context.Context, taskID string) error {
	task, err := s.db.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("getting task: %w", err)
	}
	if task.Status != state.TaskFailed {
		return fmt.Errorf("cannot escalate task in status %s (must be failed)", task.Status)
	}

	nextTier, ok := tierEscalation[task.Tier]
	if !ok {
		return fmt.Errorf("cannot escalate from tier %s (highest tier)", task.Tier)
	}

	// Update tier
	_, err = s.db.Exec(`UPDATE tasks SET tier = ? WHERE id = ?`, string(nextTier), taskID)
	if err != nil {
		return fmt.Errorf("updating tier: %w", err)
	}

	// Transition failed → queued
	if err := s.db.UpdateTaskStatus(taskID, state.TaskQueued); err != nil {
		return fmt.Errorf("requeuing escalated task: %w", err)
	}

	s.log.Info("task escalated", "task_id", taskID, "from_tier", task.Tier, "to_tier", nextTier)
	return nil
}

// BlockTask marks an in_progress task as blocked after exhausting retries and escalations.
// Per Architecture Section 30.1 Tier 3: orchestrator intervention.
func (s *Service) BlockTask(_ context.Context, taskID string) error {
	if err := s.db.UpdateTaskStatus(taskID, state.TaskBlocked); err != nil {
		return fmt.Errorf("blocking task: %w", err)
	}
	s.log.Info("task blocked", "task_id", taskID)
	return nil
}

// HandleTaskFailure determines the appropriate action for a failed task and executes it.
// Decision tree per Architecture Section 30.1:
//  1. If attempts at current tier < MaxRetriesPerTier → retry
//  2. If escalation possible and escalation count < MaxEscalations → escalate
//  3. Otherwise → block
func (s *Service) HandleTaskFailure(ctx context.Context, taskID string, feedback string) (FailureAction, error) {
	task, err := s.db.GetTask(taskID)
	if err != nil {
		return 0, fmt.Errorf("getting task: %w", err)
	}
	if task.Status != state.TaskFailed {
		return 0, fmt.Errorf("task %s is not in failed status", taskID)
	}

	attemptsAtTier, err := s.countAttemptsAtCurrentTier(taskID)
	if err != nil {
		return 0, fmt.Errorf("counting attempts: %w", err)
	}

	// Tier 1: Retry if under max retries at this tier
	if attemptsAtTier < MaxRetriesPerTier {
		if err := s.RetryTask(ctx, taskID, feedback); err != nil {
			return 0, err
		}
		return ActionRetry, nil
	}

	// Tier 2: Escalate if possible and under max escalation count
	_, canEscalate := tierEscalation[task.Tier]
	escalationCount, err := s.countEscalations(taskID)
	if err != nil {
		return 0, fmt.Errorf("counting escalations: %w", err)
	}
	if canEscalate && escalationCount < MaxEscalations {
		if err := s.EscalateTask(ctx, taskID); err != nil {
			return 0, err
		}
		return ActionEscalate, nil
	}

	// Tier 3: Block (failed → blocked is a valid direct transition)
	if err := s.db.UpdateTaskStatus(taskID, state.TaskBlocked); err != nil {
		return 0, fmt.Errorf("blocking task: %w", err)
	}
	s.log.Info("task blocked after exhaustion", "task_id", taskID)
	return ActionBlock, nil
}

// countEscalations counts how many tier escalations have occurred for a task
// by counting the number of distinct tiers in the attempt history minus one
// (the original tier).
func (s *Service) countEscalations(taskID string) (int, error) {
	attempts, err := s.db.ListAttemptsByTask(taskID)
	if err != nil {
		return 0, err
	}

	tiers := make(map[state.TaskTier]bool)
	for _, a := range attempts {
		if a.Tier != "" {
			tiers[a.Tier] = true
		}
	}

	count := len(tiers)
	if count > 0 {
		count-- // subtract the original tier
	}
	return count, nil
}

// RequestScopeExpansion handles a scope expansion request for an in_progress task.
// If the additional locks can be acquired, they are granted immediately.
// If not, the task moves to waiting_on_lock per Architecture Section 16.3.
//
// Lock acquisition uses the same atomic all-or-nothing approach as the scheduler
// per Architecture Section 16.3: all locks acquired in a single transaction, or
// none are acquired and the transaction rolls back.
func (s *Service) RequestScopeExpansion(_ context.Context, taskID string, additionalFiles []TargetFileInput) error {
	task, err := s.db.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("getting task: %w", err)
	}
	if task.Status != state.TaskInProgress {
		return fmt.Errorf("scope expansion only valid for in_progress tasks, got %s", task.Status)
	}

	// Attempt atomic lock acquisition for all additional files
	var conflictingHolder string
	acquired, err := s.tryAcquireExpansionLocks(taskID, additionalFiles, &conflictingHolder)
	if err != nil {
		return fmt.Errorf("acquiring expansion locks: %w", err)
	}

	if acquired {
		// Locks acquired atomically — record the additional target files
		for _, tf := range additionalFiles {
			if err := s.db.AddTaskTargetFile(&state.TaskTargetFile{
				TaskID:          taskID,
				FilePath:        tf.FilePath,
				LockScope:       tf.LockScope,
				LockResourceKey: tf.LockResourceKey,
			}); err != nil {
				return fmt.Errorf("recording target file: %w", err)
			}
		}
		return nil
	}

	// Lock conflict — move to waiting_on_lock (no partial locks to clean up)
	if err := s.db.UpdateTaskStatus(taskID, state.TaskWaitingOnLock); err != nil {
		return fmt.Errorf("moving to waiting_on_lock: %w", err)
	}

	// Build requested resources JSON
	type lockRes struct {
		ResourceType string `json:"resource_type"`
		ResourceKey  string `json:"resource_key"`
	}
	var requested []lockRes
	for _, tf := range additionalFiles {
		requested = append(requested, lockRes{
			ResourceType: tf.LockScope,
			ResourceKey:  tf.LockResourceKey,
		})
	}
	reqJSON, _ := json.Marshal(requested)

	var blockedByPtr *string
	if conflictingHolder != "" {
		blockedByPtr = &conflictingHolder
	}

	if err := s.db.AddLockWait(&state.TaskLockWait{
		TaskID:             taskID,
		WaitReason:         "scope_expansion",
		RequestedResources: string(reqJSON),
		BlockedByTaskID:    blockedByPtr,
	}); err != nil {
		return fmt.Errorf("adding lock wait: %w", err)
	}

	return nil
}

// errScopeConflict is a sentinel for rolling back scope expansion transactions.
var errScopeConflict = fmt.Errorf("scope expansion lock conflict")

// tryAcquireExpansionLocks attempts atomic all-or-nothing lock acquisition
// for scope expansion files. Returns true if all locks were acquired.
func (s *Service) tryAcquireExpansionLocks(taskID string, files []TargetFileInput, conflictHolder *string) (bool, error) {
	acquired := true
	txErr := s.db.WithTx(func(tx *sql.Tx) error {
		for _, tf := range files {
			var holder string
			err := tx.QueryRow(
				`SELECT task_id FROM task_locks WHERE resource_type = ? AND resource_key = ?`,
				tf.LockScope, tf.LockResourceKey,
			).Scan(&holder)

			if err == nil {
				if holder != taskID {
					acquired = false
					*conflictHolder = holder
					return errScopeConflict // rollback — no partial locks
				}
				continue // already held by us
			}
			if err != sql.ErrNoRows {
				return fmt.Errorf("checking lock: %w", err)
			}

			_, err = tx.Exec(
				`INSERT INTO task_locks (resource_type, resource_key, task_id) VALUES (?, ?, ?)`,
				tf.LockScope, tf.LockResourceKey, taskID,
			)
			if err != nil {
				return fmt.Errorf("acquiring lock: %w", err)
			}
		}
		return nil
	})

	if txErr == errScopeConflict {
		return false, nil
	}
	return acquired, txErr
}

// countAttemptsAtCurrentTier counts how many attempts exist for the task
// at its current tier. Per Architecture Section 30.1: max 3 retries at the
// same model tier before escalation. Each attempt records the tier it was
// dispatched at, so after escalation the counter resets for the new tier.
func (s *Service) countAttemptsAtCurrentTier(taskID string) (int, error) {
	task, err := s.db.GetTask(taskID)
	if err != nil {
		return 0, err
	}

	attempts, err := s.db.ListAttemptsByTask(taskID)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, a := range attempts {
		if a.Tier == task.Tier {
			count++
		}
	}
	return count, nil
}

// validateInput checks required fields on a CreateTaskInput.
func validateInput(input CreateTaskInput) error {
	if input.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if input.Title == "" {
		return fmt.Errorf("title is required")
	}
	if input.ID == "" {
		return fmt.Errorf("id is required")
	}
	return nil
}
