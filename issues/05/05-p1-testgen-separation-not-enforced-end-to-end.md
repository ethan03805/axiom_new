# Issue 05 — P1: Test-generation separation and convergence are not enforced end-to-end

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the already-implemented `internal/testgen` service into the live runtime so that (a) every implementation merge automatically spawns a test-generation task from a different model family, (b) every test-task merge automatically marks the feature converged, (c) failing tests against committed code automatically create an implementation-fix task, (d) meeseeks-exhausted test tasks automatically mark the convergence pair blocked, and (e) a run cannot be marked `completed` until every convergence pair for that run is in `converged` status. Satisfies Architecture §11.5 (Test Authorship Separation) and §30.1 (Meeseeks Failure Escalation).

**Architecture:** The `testgen.Service` already exists with full unit-test coverage; the fix is integration wiring, not new domain logic. The single hook point for successful task completion is `mergeQueueTaskAdapter.CompleteTask` in `internal/engine/mergequeue.go` (the only place in production code where a task becomes `TaskDone`). The single hook point for merge-time test failure is `mergeQueueTaskAdapter.RequeueTask`. The single hook point for exhausted-retry test failures is `Engine.failAttempt` after `e.tasks.HandleTaskFailure` returns `ActionBlock`. The single hook point for run-completion gating is `Engine.CompleteRun`. Inject `*testgen.Service` into the merge-queue task adapter, dispatch on `task.TaskType` and convergence-pair lookups to pick the correct testgen method, and add a convergence-pair gate to `CompleteRun`.

**Tech Stack:** Go 1.22, `internal/testgen`, `internal/state/convergence.go`, `internal/engine/{mergequeue,engine,run,executor}.go`, `internal/mergequeue`, `testing`.

---

## 1. Issue Statement (from `issues.md` §5)

> **P1: Test-generation separation and convergence are not enforced end to end**
>
> `internal/engine/engine.go:88-99` wires the `testgen.Service` only into scheduler model-family exclusion.
> `internal/testgen/testgen.go:41-110` implements `CreateTestTask`.
> `internal/testgen/testgen.go:256-286` implements `MarkConverged`.
> A non-test search found no runtime callers for `CreateTestTask` or `MarkConverged`.
> `docs/test-generation.md:15-16` explicitly notes that these hooks are not engine-wired.
>
> **Why this matters:** The architecture says a feature is not done until implementation and generated tests converge. In the current runtime, implementation completion is not automatically followed by test-generation, convergence tracking, or fix-loop creation.

## 2. Reproduction and Verification (2026-04-08)

The claim was verified by code inspection rather than a binary smoke test because (a) the upstream P0 issues (1–4) still block any real end-to-end run from reaching merge, and (b) the bug is an *absence* of runtime wiring, which is most decisively proved by a symbol search.

### 2.1 The service is constructed but only half-wired

`internal/engine/engine.go:98-109`:

```go
// Create testgen service for test-generation separation (Section 11.5)
e.testGen = testgen.New(opts.DB, bus, opts.Log)

// Create scheduler with engine-provided adapters
e.sched = scheduler.New(scheduler.Options{
    DB:               opts.DB,
    Log:              opts.Log,
    MaxMeeseeks:      opts.Config.Concurrency.MaxMeeseeks,
    ModelSelector:    &engineModelSelector{models: opts.Models, log: opts.Log},
    SnapshotProvider: &engineSnapshotProvider{git: opts.Git, rootDir: opts.RootDir},
    FamilyExcluder:   &engineFamilyExcluder{testGen: e.testGen},
})
```

The only consumer is `engineFamilyExcluder`, which is used by the scheduler to pick a different model family *at dispatch time*. Nothing on the merge path, on the task-done path, on the task-failed path, or on the run-completion path reaches into `e.testGen`.

### 2.2 No runtime caller of `CreateTestTask`, `MarkConverged`, `HandleTestFailure`, or `MarkBlocked`

Grep across `*.go` (excluding tests):

```
$ grep -rn "CreateTestTask\|MarkConverged\|HandleTestFailure\|MarkBlocked" --include="*.go"
internal/testgen/testgen.go:41:  func (s *Service) CreateTestTask(...)
internal/testgen/testgen.go:157: func (s *Service) HandleTestFailure(...)
internal/testgen/testgen.go:259: func (s *Service) MarkConverged(...)
internal/testgen/testgen.go:291: func (s *Service) MarkBlocked(...)
internal/testgen/testgen_test.go: ...  (all remaining hits are inside the testgen package tests)
```

Every non-test hit is a definition; every call site is in `internal/testgen/testgen_test.go`.

### 2.3 The single production path that turns a task into `TaskDone` does nothing else

`internal/engine/mergequeue.go:146-151`:

```go
// CompleteTask marks a task as done. Dependent tasks are automatically unblocked
// by the scheduler's findReadyTasks check, which verifies all dependencies have
// status "done" before dispatching a queued task (Architecture Section 15.5).
func (a *mergeQueueTaskAdapter) CompleteTask(_ context.Context, taskID string) error {
    return a.db.UpdateTaskStatus(taskID, state.TaskDone)
}
```

There is no other production call site that writes `TaskDone`. Confirmed with:

```
$ grep -rn "TaskDone" --include="*.go" internal/
# Only the mergequeue adapter above plus tests
```

This means: the moment a task (implementation, test, or fix) merges, nothing in the runtime checks convergence, spawns a downstream test task, or marks the feature done.

### 2.4 The failure paths treat test tasks like every other task

`internal/engine/executor.go:242-286` (`Engine.failAttempt`) routes every failed attempt through `e.tasks.HandleTaskFailure`, which runs the standard retry/escalate/block decision tree defined in `internal/task/service.go:355-396`. There is no branch on `task.TaskType == state.TaskTypeTest`, so a failing test meeseeks never funnels into `testgen.HandleTestFailure`, and a blocked test meeseeks never funnels into `testgen.MarkBlocked`.

`internal/engine/mergequeue.go:153-187` (`mergeQueueTaskAdapter.RequeueTask`) handles merge-time integration-check failures by transitioning `in_progress → failed → queued`. Again, there is no `TaskType` branch: a test task whose generated tests fail against HEAD is simply requeued instead of being held in `failed` so that `HandleTestFailure` can spawn a fix task.

### 2.5 Run completion is not gated on convergence

`internal/engine/run.go:198-211`:

```go
// CompleteRun transitions a run to completed.
func (e *Engine) CompleteRun(runID string) error {
    if err := e.db.UpdateRunStatus(runID, state.RunCompleted); err != nil {
        return fmt.Errorf("completing run: %w", err)
    }
    e.emitEvent(events.EngineEvent{Type: events.RunCompleted, RunID: runID})
    e.log.Info("run completed", "run_id", runID)
    return nil
}
```

`CompleteRun` never calls `ListConvergencePairsByRun` or `IsFeatureDone`. An operator or orchestrator can transition a run to `completed` while convergence pairs are still in `testing`, `fixing`, or `pending` — a direct violation of Architecture §11.5: *"a feature is not considered done until this convergence is achieved."*

### 2.6 Independent corroboration from the docs

`docs/test-generation.md:15-16` explicitly owns this gap:

> Current runtime note: the service and scheduler-family-exclusion pieces are implemented. Automatic merge-success hooks that call `CreateTestTask`, plus automatic completion hooks that call `MarkConverged`, are still explicit/orchestrator-driven rather than engine-wired.

And again at line 110, 133, 149:

> Current runtime note: this method is not yet invoked automatically when a test task finishes; orchestration code must call it explicitly.

The package-level test suite is green (`go test ./internal/testgen/...` → `ok`), which confirms the service is healthy and the only missing piece is integration wiring.

## 3. Root Cause

Phase 13 built the `testgen.Service` and the `convergence_pairs` schema/state layer as a standalone domain module, and Phase 10 wired a single slice of it — the `FamilyExcluder` — into the scheduler so the architectural "different-family" requirement at dispatch is honored. But the four lifecycle transitions — *impl merged → spawn tests*, *tests merged → converged*, *tests failed → create fix*, *meeseeks exhausted → blocked* — were left as "orchestrator-driven" TODOs inside `testgen`, and no engine worker, merge-queue adapter, or event subscriber was ever built to fire them.

On top of that, `Engine.CompleteRun` was built before `ListConvergencePairsByRun` existed, and nobody went back to add the convergence gate when the state helper landed.

Concretely:

1. `mergeQueueTaskAdapter` does not carry a reference to `*testgen.Service`, so its `CompleteTask` and `RequeueTask` methods cannot reach into testgen even if they wanted to.
2. `Engine.failAttempt` delegates every failure to `e.tasks.HandleTaskFailure` without inspecting `task.TaskType`, so the test-task-specific branches of Architecture §11.5 and §30.1 never execute.
3. `Engine.CompleteRun` has no convergence gate.
4. `docs/test-generation.md` documents the gap accurately but has not been paired with a fix.

## 4. Fix Strategy

Keep the surface area small: the existing `testgen` package is the right domain module, its unit tests already cover every method, and the only change it needs is *being called*. The fix is purely composition-root + adapter-layer work plus one run-completion guard.

1. Inject `*testgen.Service` into `mergeQueueTaskAdapter` so it can reach both the DB and the testgen API.
2. Rewrite `mergeQueueTaskAdapter.CompleteTask` to dispatch on `task.TaskType` + convergence-pair lookups:
   - *Regular impl merge* → `CreateTestTask`.
   - *Fix-task merge* (the completed task is already the `fix_task_id` of an existing convergence pair) → `MarkConverged`.
   - *Test-task merge* → `MarkConverged` on the impl pointed to by the convergence pair.
3. Rewrite `mergeQueueTaskAdapter.RequeueTask` to branch on `task.TaskType`:
   - *Test tasks* → call `testGen.HandleTestFailure(taskID, feedback)`, which marks the pair `fixing` and spawns the impl-fix task; skip the normal `failed → queued` requeue.
   - *Impl/review tasks* → existing requeue behavior unchanged.
4. Add a post-`HandleTaskFailure` hook inside `Engine.failAttempt`: if the task-service's decision was `ActionBlock` and the task is of type `test`, call `testGen.MarkBlocked(ctx, cp.ImplTaskID)` so the convergence pair reflects terminal failure instead of being silently abandoned.
5. Add a convergence gate to `Engine.CompleteRun` that calls `db.ListConvergencePairsByRun(runID)` and refuses to transition the run to `completed` while any pair is not in `converged` status, returning a structured error that lists the blocking impl tasks. The gate must be opt-outable for the narrow cancel/error paths that already have their own state transitions (only `CompleteRun` is affected — `CancelRun` and `FailRun` stay as-is).
6. Add regression tests at three layers:
   - **Adapter layer** (`internal/engine/mergequeue_test.go`): injecting a stub `*testgen.Service` (or a thin interface) and asserting that `CompleteTask` fires the right method for each of the three task-type branches, and that `RequeueTask` fires `HandleTestFailure` for test tasks.
   - **Engine layer** (`internal/engine/executor_test.go`): a scripted pipeline where an impl task merges and the next scheduler tick finds a new test task with the correct `task_type`, `impl_task_id` dependency, and a convergence pair row linking them.
   - **Run-completion layer** (`internal/engine/run_test.go`): asserting that `CompleteRun` rejects a run with a pending convergence pair, and that `CompleteRun` succeeds once every pair is marked `converged`.
7. Update `docs/test-generation.md` to remove the three "not yet invoked automatically" caveats and replace them with the new runtime hook points, and update `docs/approval-pipeline.md:5-8` to describe the fix-loop flow.
8. Commit each step separately per the TDD / frequent-commits discipline established by Issue 01 and Issue 04 fixes.

## 5. File Structure

| File | Role | Change type |
|---|---|---|
| `internal/engine/mergequeue.go` | Merge-queue adapters in the engine composition root | **Modify** — add `testGen *testgen.Service` field to `mergeQueueTaskAdapter`; rewrite `CompleteTask` and `RequeueTask` to branch on `task.TaskType` and convergence-pair lookups |
| `internal/engine/engine.go` | Engine composition root | **Modify** — pass `e.testGen` into the new `mergeQueueTaskAdapter` field |
| `internal/engine/executor.go` | Attempt execution / failure path | **Modify** — after `e.tasks.HandleTaskFailure` returns `ActionBlock`, if task is type `test` and a convergence pair exists, call `e.testGen.MarkBlocked` |
| `internal/engine/run.go` | Run lifecycle | **Modify** — add a convergence-pair gate to `CompleteRun` |
| `internal/engine/mergequeue_test.go` | New adapter-layer regression tests | **Create** — cover all three `CompleteTask` branches and the `RequeueTask` test-task branch |
| `internal/engine/executor_test.go` | Existing executor tests | **Modify** — add a new end-to-end test for "impl merges → test task spawned with correct metadata" |
| `internal/engine/run_test.go` | Existing run lifecycle tests | **Modify** — add `TestCompleteRun_BlockedByPendingConvergence` and `TestCompleteRun_AllowedAfterConvergence` |
| `docs/test-generation.md` | Reference docs | **Modify** — remove "not yet invoked automatically" caveats; document engine hook points |
| `docs/approval-pipeline.md` | Reference docs | **Modify** — describe the convergence and fix-loop steps of the pipeline |

No new files in `internal/testgen/` — the service is correct as-is.

---

## 6. Task Breakdown

### Task 1: Add `testGen` field to `mergeQueueTaskAdapter`

**Files:**
- Modify: `internal/engine/mergequeue.go:139-151`
- Modify: `internal/engine/engine.go:111-122`

- [ ] **Step 1: Write the failing compile-time check**

  Add a throwaway assertion to `internal/engine/mergequeue_test.go`:

  ```go
  package engine

  import (
      "testing"

      "github.com/openaxiom/axiom/internal/testgen"
  )

  func TestMergeQueueTaskAdapter_HasTestGenField(t *testing.T) {
      a := &mergeQueueTaskAdapter{testGen: (*testgen.Service)(nil)}
      _ = a
  }
  ```

- [ ] **Step 2: Run the test to verify it fails**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestMergeQueueTaskAdapter_HasTestGenField -count=1`
  Expected: compile failure — `unknown field 'testGen' in struct literal`.

- [ ] **Step 3: Add the field and import**

  Edit `internal/engine/mergequeue.go`:

  ```go
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
  ```

  And:

  ```go
  // mergeQueueTaskAdapter handles task lifecycle operations for the merge queue.
  type mergeQueueTaskAdapter struct {
      db      *state.DB
      sched   *scheduler.Scheduler
      testGen *testgen.Service
      log     *slog.Logger
  }
  ```

- [ ] **Step 4: Wire the field in the composition root**

  Edit `internal/engine/engine.go` in the `mergequeue.New(mergequeue.Options{...})` call so `Tasks:` uses the new field:

  ```go
  Tasks:      &mergeQueueTaskAdapter{db: opts.DB, sched: e.sched, testGen: e.testGen, log: opts.Log},
  ```

- [ ] **Step 5: Run the test to verify it passes**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestMergeQueueTaskAdapter_HasTestGenField -count=1`
  Expected: `ok`.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/engine/mergequeue.go internal/engine/engine.go internal/engine/mergequeue_test.go
  git commit -m "engine: inject testgen.Service into mergeQueueTaskAdapter"
  ```

### Task 2: Dispatch `CompleteTask` on task type (impl → CreateTestTask)

**Files:**
- Modify: `internal/engine/mergequeue.go:146-151`
- Modify: `internal/engine/mergequeue_test.go` (add test stubs)

- [ ] **Step 1: Write the failing test for the impl→test path**

  Append to `internal/engine/mergequeue_test.go`:

  ```go
  func TestMergeQueueCompleteTask_ImplMerge_SpawnsTestTask(t *testing.T) {
      db := newTestDB(t) // reuse existing test helper
      defer db.Close()

      // Seed project + run
      seedProjectAndRun(t, db, "proj-1", "run-1")

      // Seed an implementation task with a passed attempt
      implTask := &state.Task{
          ID: "impl-1", RunID: "run-1", Title: "Impl feature X",
          Status: state.TaskInProgress, Tier: state.TierStandard,
          TaskType: state.TaskTypeImplementation,
      }
      mustCreateTask(t, db, implTask)
      mustCreatePassedAttempt(t, db, "impl-1", "anthropic")

      testGen := testgen.New(db, events.New(db, slog.Default()), slog.Default())
      adapter := &mergeQueueTaskAdapter{db: db, testGen: testGen, log: slog.Default()}

      if err := adapter.CompleteTask(context.Background(), "impl-1"); err != nil {
          t.Fatalf("CompleteTask: %v", err)
      }

      // The impl task must now be done
      after, _ := db.GetTask("impl-1")
      if after.Status != state.TaskDone {
          t.Fatalf("impl status = %q, want done", after.Status)
      }

      // A test task should have been spawned
      testTask, err := db.GetTask("impl-1-test")
      if err != nil {
          t.Fatalf("expected test task impl-1-test to exist: %v", err)
      }
      if testTask.TaskType != state.TaskTypeTest {
          t.Fatalf("spawned task type = %q, want test", testTask.TaskType)
      }

      // A convergence pair should exist for impl-1
      cp, err := db.GetConvergencePairByImplTask("impl-1")
      if err != nil {
          t.Fatalf("expected convergence pair: %v", err)
      }
      if cp.ImplModelFamily != "anthropic" {
          t.Fatalf("impl family = %q, want anthropic", cp.ImplModelFamily)
      }
      if cp.Status != state.ConvergenceTesting {
          t.Fatalf("pair status = %q, want testing", cp.Status)
      }
  }
  ```

  (Use the existing test helpers in `internal/testgen/testgen_test.go:40-110` as a reference for `newTestDB`, `seedProjectAndRun`, `mustCreateTask`, `mustCreatePassedAttempt`. If they live in `testgen_test.go` only, copy the minimum subset into an `internal/engine/testgen_helpers_test.go` file. Do not export them.)

- [ ] **Step 2: Run the test to verify it fails**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestMergeQueueCompleteTask_ImplMerge_SpawnsTestTask -count=1`
  Expected: FAIL — no test task or convergence pair exists because `CompleteTask` only writes `TaskDone`.

- [ ] **Step 3: Rewrite `CompleteTask` to dispatch**

  Replace the existing `CompleteTask` body in `internal/engine/mergequeue.go`:

  ```go
  // CompleteTask marks a task as done and then dispatches test-generation lifecycle
  // hooks per Architecture Section 11.5:
  //   - implementation merge → spawn a test-generation task via testgen.CreateTestTask
  //   - test-task merge      → mark the convergence pair converged
  //   - fix-task merge       → mark the original impl's convergence pair converged
  //
  // Dependent tasks are automatically unblocked by the scheduler's findReadyTasks
  // check (Architecture Section 15.5). Any hook error is logged, not returned: the
  // merge itself already committed to git and must not be rolled back because of
  // a downstream test-generation glitch.
  func (a *mergeQueueTaskAdapter) CompleteTask(ctx context.Context, taskID string) error {
      if err := a.db.UpdateTaskStatus(taskID, state.TaskDone); err != nil {
          return err
      }
      if a.testGen == nil {
          return nil
      }

      task, err := a.db.GetTask(taskID)
      if err != nil {
          a.log.Warn("testgen hook: get task failed", "task_id", taskID, "error", err)
          return nil
      }

      switch task.TaskType {
      case state.TaskTypeImplementation:
          a.dispatchImplementationMerge(ctx, taskID)
      case state.TaskTypeTest:
          a.dispatchTestMerge(ctx, taskID)
      }
      return nil
  }

  // dispatchImplementationMerge is called when an implementation-type task merges.
  // It distinguishes regular impl tasks (which spawn a new test task) from fix
  // tasks (which must mark the pre-existing convergence pair converged).
  func (a *mergeQueueTaskAdapter) dispatchImplementationMerge(ctx context.Context, taskID string) {
      // Fix tasks are recognised by being the fix_task_id of an existing pair.
      // Inspect every pair whose impl is the same run's impl task. The cheap
      // lookup is: iterate through all pairs looking for fix_task_id == taskID.
      // We have no index on fix_task_id, so use the run-scoped list.
      task, err := a.db.GetTask(taskID)
      if err != nil {
          a.log.Warn("testgen hook: reload task failed", "task_id", taskID, "error", err)
          return
      }
      pairs, err := a.db.ListConvergencePairsByRun(task.RunID)
      if err != nil {
          a.log.Warn("testgen hook: list convergence pairs failed", "run_id", task.RunID, "error", err)
          return
      }
      for _, cp := range pairs {
          if cp.FixTaskID != nil && *cp.FixTaskID == taskID {
              if err := a.testGen.MarkConverged(ctx, cp.ImplTaskID); err != nil {
                  a.log.Warn("testgen MarkConverged (fix path) failed",
                      "impl_task_id", cp.ImplTaskID, "fix_task_id", taskID, "error", err)
              }
              return
          }
      }

      // Not a fix task — spawn test generation for this impl.
      if _, err := a.testGen.CreateTestTask(ctx, taskID); err != nil {
          a.log.Info("testgen CreateTestTask skipped or failed",
              "task_id", taskID, "error", err)
      }
  }

  // dispatchTestMerge is called when a test-type task merges successfully —
  // i.e. its generated tests passed the merge-queue integration checks against
  // committed implementation code. The feature is now converged.
  func (a *mergeQueueTaskAdapter) dispatchTestMerge(ctx context.Context, taskID string) {
      cp, err := a.db.GetConvergencePairByTestTask(taskID)
      if err != nil {
          a.log.Warn("testgen hook: no convergence pair for test task",
              "task_id", taskID, "error", err)
          return
      }
      if err := a.testGen.MarkConverged(ctx, cp.ImplTaskID); err != nil {
          a.log.Warn("testgen MarkConverged (test path) failed",
              "impl_task_id", cp.ImplTaskID, "test_task_id", taskID, "error", err)
      }
  }
  ```

- [ ] **Step 4: Run the test to verify it passes**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestMergeQueueCompleteTask_ImplMerge_SpawnsTestTask -count=1`
  Expected: `ok`.

- [ ] **Step 5: Commit**

  ```bash
  git add internal/engine/mergequeue.go internal/engine/mergequeue_test.go
  git commit -m "engine: spawn test task on implementation merge"
  ```

### Task 3: Dispatch `CompleteTask` for test-task merges (MarkConverged)

**Files:**
- Modify: `internal/engine/mergequeue_test.go` (add test)

- [ ] **Step 1: Write the failing test**

  ```go
  func TestMergeQueueCompleteTask_TestMerge_MarksConverged(t *testing.T) {
      db := newTestDB(t)
      defer db.Close()
      seedProjectAndRun(t, db, "proj-1", "run-1")

      // Seed an impl task done with a passed anthropic attempt
      implTask := &state.Task{
          ID: "impl-1", RunID: "run-1", Title: "Impl X",
          Status: state.TaskDone, Tier: state.TierStandard,
          TaskType: state.TaskTypeImplementation,
      }
      mustCreateTask(t, db, implTask)
      mustCreatePassedAttempt(t, db, "impl-1", "anthropic")

      testGen := testgen.New(db, events.New(db, slog.Default()), slog.Default())
      if _, err := testGen.CreateTestTask(context.Background(), "impl-1"); err != nil {
          t.Fatalf("seed CreateTestTask: %v", err)
      }

      // Transition the test task to in_progress so CompleteTask's
      // UpdateTaskStatus call is a valid queued→in_progress→done path.
      if err := db.UpdateTaskStatus("impl-1-test", state.TaskInProgress); err != nil {
          t.Fatalf("mark test task in_progress: %v", err)
      }

      adapter := &mergeQueueTaskAdapter{db: db, testGen: testGen, log: slog.Default()}
      if err := adapter.CompleteTask(context.Background(), "impl-1-test"); err != nil {
          t.Fatalf("CompleteTask: %v", err)
      }

      cp, err := db.GetConvergencePairByImplTask("impl-1")
      if err != nil {
          t.Fatalf("GetConvergencePairByImplTask: %v", err)
      }
      if cp.Status != state.ConvergenceConverged {
          t.Fatalf("pair status = %q, want converged", cp.Status)
      }
      if cp.ConvergedAt == nil {
          t.Fatalf("converged_at not set")
      }
  }
  ```

- [ ] **Step 2: Run the test to verify it passes**

  (The dispatch code from Task 2 already handles this branch, so the test should pass immediately. If it does not, debug the dispatch rather than loosening the test.)

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestMergeQueueCompleteTask_TestMerge_MarksConverged -count=1`
  Expected: `ok`.

- [ ] **Step 3: Commit**

  ```bash
  git add internal/engine/mergequeue_test.go
  git commit -m "engine: cover MarkConverged dispatch for test-task merges"
  ```

### Task 4: Dispatch `CompleteTask` for fix-task merges (MarkConverged on original impl)

**Files:**
- Modify: `internal/engine/mergequeue_test.go` (add test)

- [ ] **Step 1: Write the failing test**

  ```go
  func TestMergeQueueCompleteTask_FixMerge_MarksConverged(t *testing.T) {
      db := newTestDB(t)
      defer db.Close()
      seedProjectAndRun(t, db, "proj-1", "run-1")

      // Seed an impl task done with a passed anthropic attempt
      implTask := &state.Task{
          ID: "impl-1", RunID: "run-1", Title: "Impl X",
          Status: state.TaskDone, Tier: state.TierStandard,
          TaskType: state.TaskTypeImplementation,
      }
      mustCreateTask(t, db, implTask)
      mustCreatePassedAttempt(t, db, "impl-1", "anthropic")

      testGen := testgen.New(db, events.New(db, slog.Default()), slog.Default())
      if _, err := testGen.CreateTestTask(context.Background(), "impl-1"); err != nil {
          t.Fatalf("seed CreateTestTask: %v", err)
      }

      // Simulate test failure → fix task
      if err := db.UpdateTaskStatus("impl-1-test", state.TaskInProgress); err != nil {
          t.Fatalf("test in_progress: %v", err)
      }
      if err := db.UpdateTaskStatus("impl-1-test", state.TaskFailed); err != nil {
          t.Fatalf("test failed: %v", err)
      }
      if _, err := testGen.HandleTestFailure(context.Background(), "impl-1-test",
          "TestFoo FAIL"); err != nil {
          t.Fatalf("HandleTestFailure: %v", err)
      }

      // Move the fix task through the normal pipeline to in_progress
      if err := db.UpdateTaskStatus("impl-1-fix-2", state.TaskInProgress); err != nil {
          t.Fatalf("fix task in_progress: %v", err)
      }

      adapter := &mergeQueueTaskAdapter{db: db, testGen: testGen, log: slog.Default()}
      if err := adapter.CompleteTask(context.Background(), "impl-1-fix-2"); err != nil {
          t.Fatalf("CompleteTask: %v", err)
      }

      cp, err := db.GetConvergencePairByImplTask("impl-1")
      if err != nil {
          t.Fatalf("GetConvergencePairByImplTask: %v", err)
      }
      if cp.Status != state.ConvergenceConverged {
          t.Fatalf("pair status = %q, want converged", cp.Status)
      }
  }
  ```

- [ ] **Step 2: Run the test to verify it passes**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestMergeQueueCompleteTask_FixMerge_MarksConverged -count=1`
  Expected: `ok` (already handled by `dispatchImplementationMerge`).

- [ ] **Step 3: Commit**

  ```bash
  git add internal/engine/mergequeue_test.go
  git commit -m "engine: cover MarkConverged dispatch for fix-task merges"
  ```

### Task 5: Route test-task merge failures through `HandleTestFailure`

**Files:**
- Modify: `internal/engine/mergequeue.go:153-187` (`RequeueTask`)
- Modify: `internal/engine/mergequeue_test.go` (new test)

- [ ] **Step 1: Write the failing test**

  ```go
  func TestMergeQueueRequeueTask_TestTaskFailure_SpawnsFix(t *testing.T) {
      db := newTestDB(t)
      defer db.Close()
      seedProjectAndRun(t, db, "proj-1", "run-1")

      implTask := &state.Task{
          ID: "impl-1", RunID: "run-1", Title: "Impl X",
          Status: state.TaskDone, Tier: state.TierStandard,
          TaskType: state.TaskTypeImplementation,
      }
      mustCreateTask(t, db, implTask)
      mustCreatePassedAttempt(t, db, "impl-1", "anthropic")

      testGen := testgen.New(db, events.New(db, slog.Default()), slog.Default())
      if _, err := testGen.CreateTestTask(context.Background(), "impl-1"); err != nil {
          t.Fatalf("seed CreateTestTask: %v", err)
      }
      if err := db.UpdateTaskStatus("impl-1-test", state.TaskInProgress); err != nil {
          t.Fatalf("test in_progress: %v", err)
      }

      adapter := &mergeQueueTaskAdapter{db: db, testGen: testGen, log: slog.Default()}
      if err := adapter.RequeueTask(context.Background(), "impl-1-test",
          "TestFoo FAIL at line 42"); err != nil {
          t.Fatalf("RequeueTask: %v", err)
      }

      // The test task should NOT have been requeued (it should remain failed).
      after, err := db.GetTask("impl-1-test")
      if err != nil {
          t.Fatalf("GetTask: %v", err)
      }
      if after.Status != state.TaskFailed {
          t.Fatalf("test task status = %q, want failed (not requeued)", after.Status)
      }

      // A fix task should have been created via HandleTestFailure.
      fix, err := db.GetTask("impl-1-fix-2")
      if err != nil {
          t.Fatalf("expected fix task impl-1-fix-2: %v", err)
      }
      if fix.TaskType != state.TaskTypeImplementation {
          t.Fatalf("fix task type = %q, want implementation", fix.TaskType)
      }

      // Convergence pair should now be in fixing state
      cp, err := db.GetConvergencePairByImplTask("impl-1")
      if err != nil {
          t.Fatalf("GetConvergencePairByImplTask: %v", err)
      }
      if cp.Status != state.ConvergenceFixing {
          t.Fatalf("pair status = %q, want fixing", cp.Status)
      }
  }
  ```

- [ ] **Step 2: Run the test to verify it fails**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestMergeQueueRequeueTask_TestTaskFailure_SpawnsFix -count=1`
  Expected: FAIL — either the test task is requeued (wrong) or no fix task exists.

- [ ] **Step 3: Add the test-task branch to `RequeueTask`**

  Replace the existing `RequeueTask` in `internal/engine/mergequeue.go`:

  ```go
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
              a.log.Error("testgen HandleTestFailure failed", "task_id", taskID, "error", err)
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
  ```

- [ ] **Step 4: Run the test to verify it passes**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestMergeQueueRequeueTask_TestTaskFailure_SpawnsFix -count=1`
  Expected: `ok`.

- [ ] **Step 5: Run the full engine test suite**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/... -count=1`
  Expected: `ok` across every package — fail hard if any existing merge-queue test regressed.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/engine/mergequeue.go internal/engine/mergequeue_test.go
  git commit -m "engine: route failing test-task merges through HandleTestFailure"
  ```

### Task 6: Mark convergence blocked when test-task meeseeks exhausts retries

**Files:**
- Modify: `internal/engine/executor.go:242-286` (`failAttempt`)
- Modify: `internal/engine/executor_test.go` (new test)

- [ ] **Step 1: Write the failing test**

  Append to `internal/engine/executor_test.go`:

  ```go
  func TestFailAttempt_TestTaskBlock_MarksConvergenceBlocked(t *testing.T) {
      // Scripted pipeline: impl-1 done; testgen.CreateTestTask; then the test
      // meeseeks is marked block-eligible by a stub task service returning
      // ActionBlock from HandleTaskFailure. After failAttempt returns we expect
      // the convergence pair for impl-1 to be in state.ConvergenceBlocked.
      //
      // See existing pipeline tests for the scripted container / task service
      // wiring pattern.
      // ... (fill in with the same scripted test harness already used in
      // executor_test.go TestPipeline_* tests)
  }
  ```

  Use the existing scripted stubs from `internal/engine/executor_test.go` — keep the test self-contained by constructing a `mergeQueueTaskAdapter` and a stub `TaskService` whose `HandleTaskFailure` returns `ActionBlock`.

- [ ] **Step 2: Run the test to verify it fails**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestFailAttempt_TestTaskBlock_MarksConvergenceBlocked -count=1`
  Expected: FAIL — the convergence pair status is `testing`, not `blocked`.

- [ ] **Step 3: Hook `MarkBlocked` into `failAttempt`**

  Edit the tail of `Engine.failAttempt` in `internal/engine/executor.go` to inspect the action returned by the task service:

  ```go
      if e.tasks == nil {
          return fmt.Errorf("task service unavailable")
      }
      action, err := e.tasks.HandleTaskFailure(ctx, task.ID, feedback)
      if err != nil {
          return err
      }

      // Architecture §11.5 + §30.1: when a test-type task exhausts all retries
      // and escalations, the convergence pair must be marked blocked so the run
      // cannot silently pass the completion gate.
      if action == TaskFailureActionBlock && task.TaskType == state.TaskTypeTest && e.testGen != nil {
          cp, lookupErr := e.db.GetConvergencePairByTestTask(task.ID)
          if lookupErr == nil && cp != nil {
              if markErr := e.testGen.MarkBlocked(ctx, cp.ImplTaskID); markErr != nil {
                  e.log.Warn("testgen MarkBlocked failed",
                      "impl_task_id", cp.ImplTaskID, "test_task_id", task.ID, "error", markErr)
              }
          }
      }
      return nil
  }
  ```

  Check the exact constant name for the "block" action — it may be `engine.ActionBlock`, `engine.TaskFailureActionBlock`, or a typed enum; reuse whatever `TaskService.HandleTaskFailure` already returns. If the current signature returns a named integer, import it locally.

- [ ] **Step 4: Run the test to verify it passes**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestFailAttempt_TestTaskBlock_MarksConvergenceBlocked -count=1`
  Expected: `ok`.

- [ ] **Step 5: Commit**

  ```bash
  git add internal/engine/executor.go internal/engine/executor_test.go
  git commit -m "engine: mark convergence blocked when test meeseeks exhausts retries"
  ```

### Task 7: Gate `CompleteRun` on convergence

**Files:**
- Modify: `internal/engine/run.go:198-211`
- Modify: `internal/engine/run_test.go`

- [ ] **Step 1: Write the failing test**

  Append to `internal/engine/run_test.go`:

  ```go
  func TestCompleteRun_BlockedByPendingConvergence(t *testing.T) {
      e, cleanup := newTestEngine(t) // reuse existing helper
      defer cleanup()

      run := mustCreateActiveRun(t, e)

      // Seed a pending convergence pair via direct DB insertion.
      impl := &state.Task{
          ID: "impl-1", RunID: run.ID, Title: "Impl X",
          Status: state.TaskDone, Tier: state.TierStandard,
          TaskType: state.TaskTypeImplementation,
      }
      mustCreateTask(t, e.DB(), impl)
      _, err := e.DB().CreateConvergencePair(&state.ConvergencePair{
          ImplTaskID:      "impl-1",
          Status:          state.ConvergenceTesting,
          ImplModelFamily: "anthropic",
      })
      if err != nil {
          t.Fatalf("seed pair: %v", err)
      }

      err = e.CompleteRun(run.ID)
      if err == nil {
          t.Fatal("CompleteRun should fail while convergence pair is pending")
      }
      if !strings.Contains(err.Error(), "impl-1") {
          t.Fatalf("error should name the blocking impl task, got %q", err)
      }

      after, _ := e.DB().GetRun(run.ID)
      if after.Status == state.RunCompleted {
          t.Fatalf("run status = %q, want still active", after.Status)
      }
  }

  func TestCompleteRun_AllowedAfterConvergence(t *testing.T) {
      e, cleanup := newTestEngine(t)
      defer cleanup()

      run := mustCreateActiveRun(t, e)

      impl := &state.Task{
          ID: "impl-1", RunID: run.ID, Title: "Impl X",
          Status: state.TaskDone, Tier: state.TierStandard,
          TaskType: state.TaskTypeImplementation,
      }
      mustCreateTask(t, e.DB(), impl)
      id, err := e.DB().CreateConvergencePair(&state.ConvergencePair{
          ImplTaskID:      "impl-1",
          Status:          state.ConvergenceConverged,
          ImplModelFamily: "anthropic",
      })
      if err != nil {
          t.Fatalf("seed pair: %v", err)
      }
      if err := e.DB().UpdateConvergencePairStatus(id, state.ConvergenceConverged); err != nil {
          t.Fatalf("UpdateConvergencePairStatus: %v", err)
      }

      if err := e.CompleteRun(run.ID); err != nil {
          t.Fatalf("CompleteRun: %v", err)
      }
      after, _ := e.DB().GetRun(run.ID)
      if after.Status != state.RunCompleted {
          t.Fatalf("run status = %q, want completed", after.Status)
      }
  }
  ```

- [ ] **Step 2: Run the tests to verify they fail**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestCompleteRun_ -count=1`
  Expected: `TestCompleteRun_BlockedByPendingConvergence` fails because `CompleteRun` currently ignores convergence state.

- [ ] **Step 3: Add the convergence gate to `CompleteRun`**

  Edit `internal/engine/run.go`:

  ```go
  // CompleteRun transitions a run to completed. Per Architecture §11.5, a run
  // cannot complete while any implementation task has an open convergence pair.
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
  ```

  Add `"strings"` to the import block if it is not already imported.

- [ ] **Step 4: Run the tests to verify they pass**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestCompleteRun_ -count=1`
  Expected: both tests `ok`.

- [ ] **Step 5: Run the full engine suite**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/... -count=1`
  Expected: `ok`. If any existing test relied on the old "no-gate" behavior, update it to seed a converged pair (or no pair at all) instead of loosening the gate.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/engine/run.go internal/engine/run_test.go
  git commit -m "engine: gate CompleteRun on convergence pair status"
  ```

### Task 8: Engine-level end-to-end regression — impl merge spawns test task

**Files:**
- Modify: `internal/engine/executor_test.go`

- [ ] **Step 1: Write the failing test**

  Add a new test alongside the existing `TestPipeline_*` tests that drives a full mock executor pipeline end-to-end and asserts that after the first impl task merges, a new test task exists with the correct shape:

  ```go
  func TestPipeline_ImplMerge_SpawnsTestTaskEndToEnd(t *testing.T) {
      // Reuse the scripted container/validation/review stubs from existing tests
      // to simulate a successful impl task run. At the end, assert:
      //   - impl-1 is Done
      //   - impl-1-test exists, is queued, type test
      //   - convergence pair for impl-1 is in "testing" state
      //   - the scheduler's next tick would dispatch impl-1-test with
      //     ExcludeFamily = "anthropic"
  }
  ```

- [ ] **Step 2: Run the test to verify it fails or passes as expected**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestPipeline_ImplMerge_SpawnsTestTaskEndToEnd -count=1`
  Expected: should pass once Task 2 is in place. If it fails, debug and fix the scripted harness before loosening assertions.

- [ ] **Step 3: Commit**

  ```bash
  git add internal/engine/executor_test.go
  git commit -m "engine: end-to-end test for impl merge → test task spawn"
  ```

### Task 9: Update docs

**Files:**
- Modify: `docs/test-generation.md:15-16`, `:110`, `:133-149`
- Modify: `docs/approval-pipeline.md:5-8` (and any surrounding lines that describe post-merge behavior)

- [ ] **Step 1: Rewrite the "Current runtime note" caveats**

  Replace lines 15–16 of `docs/test-generation.md` with:

  > Runtime wiring: the engine's merge-queue adapter automatically calls `CreateTestTask` after an implementation task merges, `MarkConverged` after a test task merges or a fix task merges, `HandleTestFailure` when a test task's merge-queue integration checks fail against committed code, and `MarkBlocked` when a test meeseeks exhausts retries. See `internal/engine/mergequeue.go` (`CompleteTask` / `RequeueTask`) and `internal/engine/executor.go` (`failAttempt`) for the hook points.

  Remove the orchestration-only caveats at lines 110, 133, and 149 and replace them with the new runtime hook descriptions.

- [ ] **Step 2: Update `docs/approval-pipeline.md`**

  Add a step describing:

  1. After the merge queue commits an implementation task, the engine enqueues a test-generation task from a different model family.
  2. The test task runs through the same approval pipeline; merge-time integration checks actually run the generated tests against the committed code.
  3. A failing integration check on a test task triggers `HandleTestFailure`, which creates an implementation-fix task.
  4. A successful fix-task merge marks the convergence pair converged.
  5. `CompleteRun` refuses to complete a run while any convergence pair is open.

- [ ] **Step 3: Commit**

  ```bash
  git add docs/test-generation.md docs/approval-pipeline.md
  git commit -m "docs: describe engine-wired test-generation and convergence hooks"
  ```

### Task 10: Full verification

- [ ] **Step 1: Run the full repository test suite**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./... -count=1`
  Expected: `ok` across every package.

- [ ] **Step 2: Build the binary**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go build ./cmd/axiom`
  Expected: clean build.

- [ ] **Step 3: Commit any doc follow-ups discovered during verification**

  If the full test run surfaces a stale doc string or a now-invalid test assertion elsewhere, fix it in a single follow-up commit rather than squashing into an earlier task.

  ```bash
  git add <files>
  git commit -m "tests: update assertions that depended on the unwired testgen hooks"
  ```

---

## 7. Acceptance Criteria

- `go test ./internal/testgen/...` still passes (the service contract is unchanged).
- `go test ./internal/engine/...` passes with at least the following new assertions:
  - Impl merge → `impl-1-test` task created, convergence pair in `testing`, impl family recorded.
  - Test merge → convergence pair transitioned to `converged`, `converged_at` set.
  - Fix-task merge → convergence pair transitioned to `converged`.
  - Test-task merge-queue failure → `HandleTestFailure` fires, fix task created, pair in `fixing`, test task stays in `failed` (not requeued).
  - Test-task meeseeks block → `MarkBlocked` fires, pair in `blocked`.
  - `CompleteRun` refuses to complete while any pair is non-converged, and succeeds once every pair is `converged`.
- `grep -rn "CreateTestTask\|MarkConverged\|HandleTestFailure\|MarkBlocked" --include="*.go" internal/engine/` now returns runtime call sites, not just definitions.
- `docs/test-generation.md` no longer contains any "not yet invoked automatically" caveats.
- `go build ./cmd/axiom` succeeds.

## 8. Notes and Non-Goals

- **Non-goal: rewriting `testgen.Service`.** The service is correct, well-tested, and the only thing it is missing is a caller. Do not touch `internal/testgen/testgen.go`.
- **Non-goal: implementing the real validation runner for test tasks.** This plan assumes Issue 04's real validation runner will actually execute generated tests during merge-queue integration checks. If Issue 04 is not yet fully shipped, the test-task merge path will still land in `RequeueTask` with a real failure, and the Task 5 branch still fires `HandleTestFailure` correctly — the wiring is independent.
- **Idempotency.** `testgen.CreateTestTask` rejects duplicates; `dispatchImplementationMerge` relies on this to avoid double-spawning if a task is somehow merged twice. `MarkConverged` is also idempotent against a pair already in `converged`, so double-fires are safe.
- **Ordering.** The dispatch logic in `CompleteTask` distinguishes fix tasks from regular impl tasks by checking `fix_task_id` on existing pairs. This is O(N) per run in `ListConvergencePairsByRun`, which is acceptable because convergence pairs are bounded by the number of implementation tasks in a run. If profiling later shows this is hot, add a `GetConvergencePairByFixTask` helper; until then, do not prematurely optimize.
- **Scope of the failure hook.** Only `mergeQueueTaskAdapter.RequeueTask` routes test-task failures through `HandleTestFailure`. The executor's `failAttempt` path uses the standard retry/escalate/block decision tree because meeseeks-level failures (bad manifest, review rejection, container crash) should still be retried — only once the retry/escalation machinery gives up with `ActionBlock` does `failAttempt` reach into testgen to mark the pair blocked.
- **Interaction with Issue 01.** Issue 01's fix is a prerequisite for observing this hook firing end-to-end from `axiom run`, but it is not a prerequisite for the unit / adapter-level tests in this plan. They can merge in any order.
- **Interaction with Issue 03.** If Issue 03 adds an executor-level hook for "task done", prefer to wire the testgen dispatch there rather than in the merge-queue adapter. The current plan puts it in the merge-queue adapter because that is the only place a task currently becomes `TaskDone` in production code. If the merge-queue remains the single choke point after Issue 03, nothing in this plan needs to move.
