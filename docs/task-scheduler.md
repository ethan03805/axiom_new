# Task System, Scheduler, and Locking

Phase 10 implementation of the task creation API, dependency validation, execution scheduler, write-set locking, retry/escalation/blocking, and scope expansion.

Architecture references: Sections 15 (Task System), 16 (Concurrency, Snapshots & Merge Queue), 22 (Crash Recovery), and 30 (Error Handling & Escalation).

## Overview

The task system moves Axiom from an approved SRS to safe concurrent execution. It has two main components:

1. **Task Service** (`internal/task/`) — Task creation (single and batch), dependency validation with cycle detection, retry/escalation/blocking decision tree, and scope expansion requests.
2. **Scheduler** (`internal/scheduler/`) — Periodic dispatch loop that finds ready tasks, acquires write-set locks atomically, creates attempt records, and manages lock waiters.

The scheduler is registered as an engine background worker and runs every 500ms.

## Task Service

### Package: `internal/task/`

#### Service Creation

```go
import "github.com/openaxiom/axiom/internal/task"

svc := task.New(db, logger)
```

### Task Creation

#### Single Task

```go
t, err := svc.CreateTask(ctx, task.CreateTaskInput{
    ID:       "task-001",
    RunID:    runID,
    Title:    "Implement auth module",
    Tier:     state.TierStandard,
    TaskType: state.TaskTypeImplementation,
    SRSRefs:  []string{"FR-001", "AC-003"},
    TargetFiles: []task.TargetFileInput{
        {FilePath: "pkg/auth/handler.go", LockScope: "file", LockResourceKey: "pkg/auth/handler.go"},
    },
    DependsOn: []string{"task-000"},
})
```

All metadata (task record, SRS refs, target files, dependencies) is persisted in a single transaction. If any step fails, the entire operation rolls back.

Required fields: `ID`, `RunID`, `Title`. Tasks are always created in `queued` status.

#### Batch Creation

```go
tasks, err := svc.CreateBatch(ctx, []task.CreateTaskInput{
    {ID: "task-001", RunID: runID, Title: "Build API", Tier: state.TierStandard, TaskType: state.TaskTypeImplementation},
    {ID: "task-002", RunID: runID, Title: "Build tests", Tier: state.TierCheap, TaskType: state.TaskTypeTest, DependsOn: []string{"task-001"}},
})
```

`CreateBatch` wraps all inserts in a single transaction. Before any writes, it validates:

1. All required fields are present on every input.
2. No self-dependencies.
3. All dependency targets exist (either within the batch or already persisted in the same run).
4. No circular dependencies (detected via DFS — see below).

If validation fails, no tasks are created.

### Dependency Validation and Cycle Detection

Cycle detection uses depth-first search with three-color marking (white/gray/black):

- **White**: unvisited node
- **Gray**: node in the current DFS path (back-edge to a gray node = cycle)
- **Black**: fully explored node

The algorithm detects direct cycles (`A → B → A`), transitive cycles (`A → B → C → A`), and self-dependencies (`A → A`). Dependencies referencing tasks outside the current batch are checked for existence in the database but are not traversed for cycles (they are already committed and cycle-free by induction).

### Retry / Escalation / Blocking

Per Architecture Section 30.1, task failure follows a three-tier escalation:

```
Tier 1: RETRY (same model tier, fresh container)
  - Max 3 retries at the same tier (MaxRetriesPerTier = 3)
  - Task transitions: failed → queued

Tier 2: ESCALATE (next higher model tier, fresh container)
  - Max 2 escalations total (MaxEscalations = 2)
  - Tier chain: local → cheap → standard → premium
  - Task transitions: failed → queued (with tier updated)

Tier 3: BLOCK (orchestrator intervention required)
  - Task transitions: failed → blocked
  - Orchestrator may restructure, add context, or file an ECO
```

#### HandleTaskFailure

The main entry point for failure routing:

```go
action, err := svc.HandleTaskFailure(ctx, taskID, "compile error in handler.go")
// action is one of: ActionRetry, ActionEscalate, ActionBlock
```

Decision tree:
1. Count attempts at the task's current tier (uses the `tier` column on `task_attempts`).
2. If count < `MaxRetriesPerTier` (3) → `RetryTask` (requeue at same tier).
3. Else if escalation possible AND escalation count < `MaxEscalations` (2) → `EscalateTask` (bump tier, requeue).
4. Else → block the task (`failed → blocked`).

Escalation count is determined by counting distinct tiers across all attempts for the task, minus one (the original tier).

#### Individual Methods

```go
// Retry: requeue a failed task at the same tier
err := svc.RetryTask(ctx, taskID, "feedback for next attempt")

// Escalate: move to next higher tier and requeue
err := svc.EscalateTask(ctx, taskID)

// Block: mark as requiring orchestrator intervention
err := svc.BlockTask(ctx, taskID)
```

### Scope Expansion

When an in-progress task discovers it needs to modify additional files beyond its original target set, it requests scope expansion:

```go
err := svc.RequestScopeExpansion(ctx, taskID, []task.TargetFileInput{
    {FilePath: "pkg/auth/utils.go", LockScope: "file", LockResourceKey: "pkg/auth/utils.go"},
})
```

Lock acquisition uses the same atomic all-or-nothing approach as the scheduler (Section 16.3). All additional locks are attempted in a single database transaction:

- **All locks acquired**: target files are recorded and the task continues in `in_progress`.
- **Any conflict**: the transaction rolls back (no partial locks), the task moves to `waiting_on_lock`, and a `task_lock_waits` record is created with `wait_reason = "scope_expansion"`.

## Scheduler

### Package: `internal/scheduler/`

#### Interfaces

The scheduler depends on two pluggable interfaces:

```go
// ModelSelector picks a model for a given tier.
type ModelSelector interface {
    SelectModel(ctx context.Context, tier state.TaskTier, excludeFamily string) (modelID, modelFamily string, err error)
}

// SnapshotProvider provides the current HEAD SHA for base_snapshot pinning.
type SnapshotProvider interface {
    CurrentHEAD() (string, error)
}
```

The engine provides adapters that bridge `ModelService` and `GitService` to these interfaces.

#### Scheduler Creation

```go
sched := scheduler.New(scheduler.Options{
    DB:               db,
    Log:              logger,
    MaxMeeseeks:      cfg.Concurrency.MaxMeeseeks,
    ModelSelector:    modelSelector,
    SnapshotProvider: snapshotProvider,
})
```

### Tick Loop

The scheduler's `Tick` method runs one dispatch iteration:

```
For each active run:
  1. Count in-progress tasks.
  2. Compute available slots (MaxMeeseeks - in_progress count).
  3. Find queued tasks whose dependencies are ALL done.
  4. For each ready task (up to available slots):
     a. Build lock requests from target files.
     b. Attempt atomic lock acquisition (deterministic order).
     c. If locks acquired → dispatch (create attempt, move to in_progress).
     d. If lock conflict → move to waiting_on_lock.
```

Paused, cancelled, completed, and error runs are skipped entirely.

### Lock Acquisition

Per Architecture Section 16.3:

- **Deterministic order**: Locks are sorted alphabetically by `(resource_type, resource_key)` before acquisition. This prevents deadlocks when multiple tasks require overlapping lock sets.
- **Atomic (all-or-nothing)**: All locks are acquired in a single database transaction. If any lock is held by another task, the entire transaction rolls back — no partial locks are ever left behind.
- **Lock granularity**: file, package, module, or schema (as declared in `task_target_files`).

```
Lock scope hierarchy (from Section 16.3):
  file    — task modifies only implementation internals
  package — task modifies exported symbols/interfaces
  module  — task modifies API schemas or route contracts
  schema  — task involves database migrations
```

### Dispatch

When a task is dispatched:

1. A model is selected for the task's tier via `ModelSelector`.
2. The current HEAD SHA is captured via `SnapshotProvider` for base_snapshot pinning (Section 16.2).
3. The attempt number is computed from existing attempts (previous count + 1).
4. The task transitions `queued → in_progress`.
5. A new `task_attempts` record is created with `status = running`, `phase = executing`, and the task's current tier.

### Lock Release and Waiter Processing

When a task completes, fails, or is cancelled, its locks are released:

```go
err := sched.ReleaseLocks(ctx, taskID)
```

After releasing locks, the scheduler scans all `waiting_on_lock` tasks. For each waiter:

1. Parse the `requested_resources` JSON from `task_lock_waits`.
2. Check if ALL requested resources are now free.
3. If all free → transition `waiting_on_lock → queued` and remove the lock wait record.
4. If still blocked → leave in `waiting_on_lock`.

On the next tick, requeued tasks will be picked up for dispatch.

## Engine Integration

The scheduler is wired into the engine in `internal/engine/scheduler.go`:

```go
// Engine.Start() registers the scheduler as a background worker:
e.workers.Register("scheduler", e.schedulerLoop, 500*time.Millisecond)
```

Two adapter types bridge engine services to scheduler interfaces:

- `engineModelSelector` — wraps `ModelService.List()` to select the first available model at the requested tier.
- `engineSnapshotProvider` — wraps `GitService.CurrentHEAD()` with the project root directory.

## Database Changes

### Migration 005: Attempt Tier Column

```sql
ALTER TABLE task_attempts ADD COLUMN tier TEXT NOT NULL DEFAULT 'standard';
```

The tier column records which model tier an attempt was dispatched at. This enables accurate per-tier retry counting — after escalation from local to cheap, the 3-retry counter resets for the cheap tier.

### State Transition Updates

Two new state transitions were added to `validTaskTransitions` in `state/models.go`:

| From | To | Reason |
|------|----|--------|
| `in_progress` | `waiting_on_lock` | Scope expansion lock conflict (Section 16.3) |
| `failed` | `blocked` | Retry/escalation exhaustion (Section 30.1 Tier 3) |

The full task state machine is now:

```
queued → in_progress → done
  │         │  │
  │         │  ├→ failed → queued       (retry or escalation)
  │         │  │         → blocked      (exhaustion)
  │         │  ├→ blocked
  │         │  └→ waiting_on_lock       (scope expansion conflict)
  │         └→ cancelled_eco
  │
  └→ waiting_on_lock → queued           (locks released)
                     → in_progress      (already valid)
                     → cancelled_eco
```

## Test Coverage

| Test | What it verifies |
|------|-----------------|
| **Task Service (24 tests)** | |
| `TestCreateTask_Single` | Basic task creation and persistence |
| `TestCreateTask_WithSRSRefs` | SRS reference junction table population |
| `TestCreateTask_WithTargetFiles` | Target file metadata persistence |
| `TestCreateTask_MissingRunID` | Validation rejects missing run_id |
| `TestCreateTask_MissingTitle` | Validation rejects missing title |
| `TestCreateBatch_MultipleTasks` | Transactional batch creation |
| `TestCreateBatch_WithDependencies` | Dependency edge persistence |
| `TestCreateBatch_RejectsCyclicDependencies` | Direct cycle detection (A → B → A) |
| `TestCreateBatch_RejectsTransitiveCycle` | Transitive cycle detection (A → B → C → A) |
| `TestCreateBatch_RejectsSelfDependency` | Self-dependency rejection |
| `TestCreateBatch_RejectsMissingDependency` | Unknown dependency rejection |
| `TestCreateBatch_TransactionalRollbackOnError` | No partial writes on failure |
| `TestRetryTask_RequeuesFailedTask` | failed → queued transition |
| `TestRetryTask_RejectsNonFailedTask` | Non-failed task cannot be retried |
| `TestEscalateTask_MovesToNextTier` | local → cheap tier escalation |
| `TestEscalateTask_FullEscalationChain` | local → cheap → standard chain |
| `TestEscalateTask_PremiumTierCannotEscalate` | Premium has no higher tier |
| `TestBlockTask_MarksAsBlocked` | in_progress → blocked |
| `TestHandleTaskFailure_RetriesFirst` | Under max retries → retry |
| `TestHandleTaskFailure_EscalatesAfterMaxRetries` | At max retries → escalate |
| `TestHandleTaskFailure_BlocksAfterExhaustion` | Premium at max retries → block |
| `TestRequestScopeExpansion_AddsLockWait` | Conflict → waiting_on_lock with lock wait record |
| `TestRequestScopeExpansion_GrantedWhenUnlocked` | No conflict → locks acquired, stays in_progress |
| `TestCountAttemptsAtCurrentTier` | Per-tier attempt counting accuracy |
| **Scheduler (15 tests)** | |
| `TestTick_DispatchesReadyTask` | Queued task with no deps → in_progress + attempt |
| `TestTick_SkipsTasksWithUnfinishedDeps` | Task with pending dep stays queued |
| `TestTick_DispatchesTaskWhenDepsAreDone` | Task dispatched when all deps done |
| `TestTick_AcquiresLocksBeforeDispatch` | Locks acquired for target files |
| `TestTick_LockConflictMovesToWaitingOnLock` | Conflicting lock → waiting_on_lock |
| `TestTick_RespectsMaxMeeseeksLimit` | Concurrency capped at max_meeseeks |
| `TestTick_CountsExistingInProgressTasks` | Existing in_progress tasks count toward limit |
| `TestTick_MultipleIndependentTasksDispatch` | Parallel dispatch of non-conflicting tasks |
| `TestTick_AtomicLockAcquisition_AllOrNothing` | Partial conflict → zero locks acquired |
| `TestProcessLockWaiters_RequeuesWhenLockReleased` | Released locks unblock waiters |
| `TestProcessLockWaiters_StaysWaitingIfStillBlocked` | Partial release doesn't unblock |
| `TestDeterministicLockOrder` | Locks sorted by (type, key) |
| `TestTick_NoActiveRuns` | No-op when no active runs exist |
| `TestTick_PausedRunSkipped` | Paused runs are not processed |
| `TestTick_CorrectAttemptNumber` | Attempt number increments correctly |

## API Reference

### task.Service

| Method | Signature | Description |
|--------|-----------|-------------|
| `New` | `New(db *state.DB, log *slog.Logger) *Service` | Create a new task service |
| `CreateTask` | `(ctx, CreateTaskInput) (*state.Task, error)` | Create single task (transactional) |
| `CreateBatch` | `(ctx, []CreateTaskInput) ([]*state.Task, error)` | Create multiple tasks (transactional, validates cycles) |
| `RetryTask` | `(ctx, taskID, feedback string) error` | Requeue failed task at same tier |
| `EscalateTask` | `(ctx, taskID string) error` | Move failed task to next tier and requeue |
| `BlockTask` | `(ctx, taskID string) error` | Mark in_progress task as blocked |
| `HandleTaskFailure` | `(ctx, taskID, feedback string) (FailureAction, error)` | Route failure to retry/escalate/block |
| `RequestScopeExpansion` | `(ctx, taskID string, files []TargetFileInput) error` | Expand lock set or move to waiting |

### scheduler.Scheduler

| Method | Signature | Description |
|--------|-----------|-------------|
| `New` | `New(Options) *Scheduler` | Create a new scheduler |
| `Tick` | `(ctx) error` | Run one dispatch iteration across all active runs |
| `ReleaseLocks` | `(ctx, taskID string) error` | Release locks and process waiters |

### Constants

| Constant | Value | Source |
|----------|-------|--------|
| `MaxRetriesPerTier` | 3 | Architecture Section 30.1 |
| `MaxEscalations` | 2 | Architecture Section 30.1 |
| Scheduler interval | 500ms | Engine worker registration |
| Merge queue interval | 500ms | Engine worker registration |

## Integration with Merge Queue

After a task's output passes through the approval pipeline (manifest validation, validation sandbox, reviewer, orchestrator gate), the output is submitted to the serialized merge queue via `engine.EnqueueMerge()`. The merge queue runs as a separate 500ms background worker alongside the scheduler.

When the merge queue successfully commits a task's output:
1. It calls `scheduler.ReleaseLocks()` to release write-set locks and process any waiters
2. It marks the task as `done` via `state.DB.UpdateTaskStatus()`
3. The scheduler's next `Tick` automatically unblocks dependent tasks — `findReadyTasks` checks all dependencies have status `done` before dispatching

When the merge queue rejects a task (conflict or integration failure):
1. It stores failure feedback on the latest attempt record
2. It releases write-set locks
3. It transitions the task `in_progress → failed → queued` for a fresh attempt
4. The scheduler picks it up on the next dispatch cycle

See [Approval Pipeline Reference](approval-pipeline.md) for the full merge queue documentation.
