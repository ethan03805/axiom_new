# Test-Generation Separation and Convergence Logic

Phase 13 implementation of test-generation separation, model family enforcement, convergence tracking, and post-test failure recovery.

Architecture references: Section 11.5 (Test Authorship Separation), Section 11.3 (Model Family Diversification), Section 30.1 (Error Handling & Escalation).

## Overview

The test-generation system enforces architecture-mandated independence between implementation and test authorship. It has three main components:

1. **Test-Generation Service** (`internal/testgen/`) -- Creates test tasks for merged implementations, enforces model family separation, handles test failure recovery, and tracks convergence state.
2. **Convergence State Layer** (`internal/state/convergence.go`) -- Persists the relationship between implementation, test, and fix tasks with lifecycle tracking.
3. **Scheduler Integration** (`internal/scheduler/`) -- The `FamilyExcluder` interface ensures test-type tasks are dispatched with a different model family than the implementation.

Runtime wiring: the engine's merge-queue adapter (`internal/engine/mergequeue.go`) automatically calls `CreateTestTask` after an implementation task merges, `MarkConverged` after a test task or fix task merges, and `HandleTestFailure` when a test task's merge-queue integration checks fail against committed code. `MarkBlocked` is invoked from `internal/engine/executor.go` (`failAttempt`) when a test meeseeks exhausts retries and the task service returns `TaskFailureBlock`. `Engine.CompleteRun` refuses to complete a run while any convergence pair is non-converged (`internal/engine/run.go`).

## Architecture Principles

Per Architecture Section 11.5:

1. **Tests SHALL NOT be authored by the same Meeseeks that wrote the implementation.** Test generation is a separate task assigned to a Meeseeks from a different model family.
2. **Dependency ordering is strict:** implementation executes and merges first, then test generation spawns with the semantic index of the committed implementation.
3. **Convergence is required:** A feature is not considered done until both the implementation and its generated tests converge (all tests green).
4. **Fix loops are recoverable:** When generated tests fail, an implementation-fix task is created with full failure context, going through the normal approval pipeline.

## Test-Generation Service

### Package: `internal/testgen/`

#### Service Creation

```go
import "github.com/openaxiom/axiom/internal/testgen"

svc := testgen.New(db, eventBus, logger)
```

### Creating Test Tasks

When orchestration code decides to start test generation for a completed implementation task:

```go
testTask, err := svc.CreateTestTask(ctx, "impl-task-001")
```

`CreateTestTask` performs the following in a single atomic transaction:

1. Validates the implementation task exists, is type `implementation`, and has status `done`.
2. Checks no convergence pair already exists for this implementation (prevents duplicates).
3. Retrieves the model family from the successful (passed) attempt.
4. Creates a test-type task with `queued` status at the same tier as the implementation.
5. Adds a dependency from the test task to the implementation task.
6. Creates a `convergence_pairs` record linking impl to test task with status `testing`.
7. Emits a `testgen_created` event.

The test task ID is derived from the implementation task ID: `<impl-task-id>-test`.

### Model Family Exclusion

When the scheduler dispatches a test-type task, it queries the testgen service for the exclude family:

```go
excludeFamily, err := svc.GetExcludeFamily(ctx, testTaskID)
// Returns "anthropic" if the implementation used an Anthropic model
// Returns "" for non-test tasks or tasks without convergence pairs
```

The scheduler passes this to `ModelSelector.SelectModel(ctx, tier, excludeFamily)`, which selects a model from a different family. For example, if the implementation used Claude (anthropic family), the test task will use GPT (openai family) or another available family.

### Handling Test Failures

When generated tests fail against the committed implementation:

```go
fixTask, err := svc.HandleTestFailure(ctx, testTaskID,
    "FAIL: TestAuth_Login (0.02s)\n    expected 200, got 401")
```

`HandleTestFailure` atomically:

1. Validates the test task exists, is type `test`, and has status `failed`.
2. Looks up the convergence pair via the test task.
3. Creates an implementation-fix task with:
   - Type `implementation`
   - A description containing the original implementation reference, failing test reference, and full failure output
   - A dependency on the test task (for context)
4. Updates the convergence pair: sets fix task, transitions to `fixing` status, increments iteration.
5. Emits a `testgen_fix_created` event.

The fix task ID follows the pattern: `<impl-task-id>-fix-<iteration>`.

### Convergence Checking

```go
status, err := svc.CheckConvergence(ctx, "impl-task-001")
// Returns: "", "pending", "testing", "fixing", "converged", or "blocked"
// Empty string means no convergence pair exists for this task
```

### Marking Convergence

After the test task completes successfully (status = done):

```go
err := svc.MarkConverged(ctx, "impl-task-001")
```

Validates the test task is done before transitioning to `converged`. Sets the `converged_at` timestamp. Emits a `testgen_converged` event.

Runtime hook: `mergeQueueTaskAdapter.CompleteTask` in `internal/engine/mergequeue.go` invokes this automatically after a test-type task merges and after a fix-type task merges (resolving the convergence pair that owned the fix task).

### Feature Completion Check

```go
done, err := svc.IsFeatureDone(ctx, "impl-task-001")
// true only when convergence pair status is "converged"
```

Per Architecture Section 11.5: a feature is not considered `done` until this returns true.

### Blocking Convergence

When a test-type meeseeks exhausts all its retries and escalations, the task service's decision tree (see `internal/task/service.go` and Architecture §30.1) returns `TaskFailureBlock`:

```go
err := svc.MarkBlocked(ctx, "impl-task-001")
```

Emits a `testgen_blocked` event. A blocked pair counts as non-converged for the `Engine.CompleteRun` gate, so the run cannot silently pass completion while a blocked pair exists — an operator must intervene (restructure the task, cancel the run, or escalate).

Runtime hook: `Engine.failAttempt` in `internal/engine/executor.go` invokes `MarkBlocked` automatically when the task service's `HandleTaskFailure` returns `TaskFailureBlock` for a test-type task. Meeseeks-level failures on any other task type still funnel through the standard retry/escalate/block decision tree without touching the convergence pair.

## Convergence Lifecycle

Runtime lifecycle, fully engine-wired as of Issue 05:

```text
Implementation task has merged successfully (mergeQueueTaskAdapter.CompleteTask)
  |
  +-> dispatchImplementationMerge calls testgen.CreateTestTask(implTaskID)
  |    Creates test task + convergence pair (status: testing)
  |
  +-> Scheduler dispatches test task via engineFamilyExcluder
  |    (different model family from implementation)
  |
  +-> Test task merges successfully (mergeQueueTaskAdapter.CompleteTask)
  |    +-> dispatchTestMerge calls testgen.MarkConverged(implTaskID)
  |         Convergence achieved (status: converged)
  |         Feature is done
  |
  +-> Test task merge-queue integration checks fail
  |    (mergeQueueTaskAdapter.RequeueTask)
  |    +-> Test task transitioned to TaskFailed (not requeued)
  |    +-> testgen.HandleTestFailure spawns fix task
  |         Convergence pair transitions to fixing, iteration++
  |    +-> Fix task runs through normal pipeline and merges
  |         +-> dispatchImplementationMerge recognises it is a fix task
  |              (task ID matches an existing pair.fix_task_id)
  |         +-> testgen.MarkConverged(implTaskID)
  |              Convergence achieved
  |
  +-> Test meeseeks exhausts all retries (Engine.failAttempt)
       +-> taskService.HandleTaskFailure returns TaskFailureBlock
       +-> testgen.MarkBlocked(implTaskID)
            Convergence blocked
            Engine.CompleteRun will refuse to complete the run until
            this pair is resolved (blocked status is not converged).
```

## Database Schema

### Migration 006: Convergence Pairs

```sql
CREATE TABLE convergence_pairs (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    impl_task_id      TEXT NOT NULL REFERENCES tasks(id),
    test_task_id      TEXT REFERENCES tasks(id),
    fix_task_id       TEXT REFERENCES tasks(id),
    status            TEXT NOT NULL DEFAULT 'pending',
    impl_model_family TEXT NOT NULL,
    iteration         INTEGER NOT NULL DEFAULT 1,
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    converged_at      DATETIME
);

CREATE INDEX idx_convergence_impl ON convergence_pairs(impl_task_id);
CREATE INDEX idx_convergence_test ON convergence_pairs(test_task_id);
CREATE INDEX idx_convergence_status ON convergence_pairs(status);
```

### State Layer Methods

| Method | Description |
|--------|-------------|
| `CreateConvergencePair` | Insert a new convergence pair |
| `GetConvergencePair` | Retrieve by ID |
| `GetConvergencePairByImplTask` | Retrieve by implementation task ID |
| `GetConvergencePairByTestTask` | Retrieve by test task ID |
| `UpdateConvergencePairStatus` | Transition status (sets converged_at for converged) |
| `SetConvergenceTestTask` | Set the test task ID |
| `SetConvergenceFixTask` | Set the fix task ID |
| `IncrementConvergenceIteration` | Bump iteration counter for fix loops |
| `ListConvergencePairsByRun` | List all convergence pairs for a run |

## Scheduler Integration

### FamilyExcluder Interface

```go
type FamilyExcluder interface {
    GetExcludeFamily(ctx context.Context, taskID string) (string, error)
}
```

The scheduler's `dispatch()` method:

1. Checks if a `FamilyExcluder` is configured (nil-safe, backward compatible).
2. For test-type tasks, calls `GetExcludeFamily` which looks up the convergence pair and returns the implementation's model family.
3. Passes the exclude family to `ModelSelector.SelectModel()`.
4. The `engineModelSelector` iterates available models and picks one from a different family.

### Engine Adapters

```go
// engineFamilyExcluder adapts testgen.Service to scheduler.FamilyExcluder
type engineFamilyExcluder struct {
    testGen *testgen.Service
}

func (f *engineFamilyExcluder) GetExcludeFamily(ctx context.Context, taskID string) (string, error) {
    return f.testGen.GetExcludeFamily(ctx, taskID)
}
```

## Event Types

Four new authoritative events (persisted to SQLite):

| Event | Emitted When | Details |
|-------|-------------|---------|
| `testgen_created` | Test task created | impl_task, exclude_family |
| `testgen_converged` | Implementation + tests all green | test_task |
| `testgen_fix_created` | Fix task spawned after test failure | impl_task, test_task, iteration |
| `testgen_blocked` | Convergence exhausted retries | iteration |

## Test Coverage

| Test | What it verifies |
|------|-----------------|
| **TestGen Service (24 tests)** | |
| `TestCreateTestTask_CreatesTestTaskForCompletedImpl` | Creates test task with correct type, status, run, dependency |
| `TestCreateTestTask_CreatesConvergencePair` | Convergence pair created with correct impl family and status |
| `TestCreateTestTask_RejectsNonDoneImplTask` | Impl must be done before test generation |
| `TestCreateTestTask_RejectsNonImplTask` | Only implementation tasks can have test tasks |
| `TestCreateTestTask_RejectsImplWithoutSuccessfulAttempt` | Impl must have a passed attempt |
| `TestCreateTestTask_RejectsDuplicateForSameImpl` | Cannot create two test tasks for same impl |
| `TestGetExcludeFamily_ReturnsImplModelFamily` | Returns anthropic/openai/etc. for test tasks |
| `TestGetExcludeFamily_ReturnsEmptyForNonTestTask` | Non-test tasks return empty |
| `TestHandleTestFailure_CreatesFixTask` | Fix task created with correct type and dependency |
| `TestHandleTestFailure_UpdatesConvergencePair` | Convergence pair status -> fixing, iteration incremented |
| `TestHandleTestFailure_RejectsNonFailedTestTask` | Test task must be failed |
| `TestHandleTestFailure_RejectsNonTestTask` | Only test tasks can trigger fix creation |
| `TestHandleTestFailure_FixTaskDescriptionContainsFailureOutput` | Fix task description includes failure context |
| `TestCheckConvergence_PendingWhenJustCreated` | Pending status for new pairs |
| `TestCheckConvergence_TestingWhenTestTaskCreated` | Testing status after CreateTestTask |
| `TestCheckConvergence_FixingAfterTestFailure` | Fixing status after HandleTestFailure |
| `TestCheckConvergence_NoPairReturnsEmptyString` | Empty string when no convergence pair exists |
| `TestMarkConverged_SetsConvergedStatus` | Status transitions to converged, converged_at set |
| `TestMarkConverged_RejectsWhenTestNotDone` | Test task must be done to converge |
| `TestIsFeatureDone_TrueWhenConverged` | True only for converged pairs |
| `TestIsFeatureDone_FalseWhenTesting` | False while testing in progress |
| `TestIsFeatureDone_FalseWhenNoPair` | False when no pair exists |
| `TestCreateTestTask_DifferentModelFamilies` | Parameterized: anthropic, openai, google, meta |
| `TestMarkBlocked_SetsBlockedStatus` | Status transitions to blocked |
| **Scheduler Integration (3 tests)** | |
| `TestTick_TestTaskUsesExcludeFamily` | Test tasks pass impl model family to SelectModel |
| `TestTick_ImplTaskUsesEmptyExcludeFamily` | Impl tasks pass empty excludeFamily |
| `TestTick_NilFamilyExcluder` | Backward compatible - nil excluder works |
