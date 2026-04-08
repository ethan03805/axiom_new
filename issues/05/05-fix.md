# Issue 05 — Fix Report: Test-generation separation and convergence are now enforced end-to-end

**Status:** Implemented and verified — 2026-04-08

## 1. Summary

The `internal/testgen` service existed with complete unit-test coverage but was
only half-wired into the runtime: the scheduler used its `FamilyExcluder` at
dispatch time, and nothing else. None of the four lifecycle transitions that
Architecture §11.5 requires were actually fired by the engine:

- implementation merge → spawn a test-generation task
- test-task merge → mark the convergence pair converged
- test-task merge failure → create an implementation-fix task via `HandleTestFailure`
- test-task meeseeks exhausted → mark the pair blocked

And `Engine.CompleteRun` had no convergence gate, so an operator could mark a
run `completed` while pairs were still `testing`/`fixing`/`blocked`.

This fix is pure composition-root + adapter-layer wiring. The `testgen.Service`
itself was not modified — its existing API is called from three new hook
points in the engine.

## 2. Hook points added

| Transition | Hook location | Code |
|---|---|---|
| impl merge → `CreateTestTask` | `mergeQueueTaskAdapter.CompleteTask` → `dispatchImplementationMerge` | `internal/engine/mergequeue.go` |
| fix-task merge → `MarkConverged` (on original impl) | `mergeQueueTaskAdapter.CompleteTask` → `dispatchImplementationMerge` recognises fix tasks by matching `task.ID == pair.fix_task_id` | `internal/engine/mergequeue.go` |
| test-task merge → `MarkConverged` | `mergeQueueTaskAdapter.CompleteTask` → `dispatchTestMerge` | `internal/engine/mergequeue.go` |
| test-task merge failure → `HandleTestFailure` | `mergeQueueTaskAdapter.RequeueTask` test-type branch | `internal/engine/mergequeue.go` |
| test meeseeks exhausted → `MarkBlocked` | `Engine.failAttempt` post-`HandleTaskFailure` hook (`action == TaskFailureBlock && task.TaskType == TaskTypeTest`) | `internal/engine/executor.go` |
| run completion gate | `Engine.CompleteRun` now calls `ListConvergencePairsByRun` and refuses to transition while any pair is non-converged | `internal/engine/run.go` |

## 3. Implementation details

### 3.1 `mergeQueueTaskAdapter` now carries a `*testgen.Service`

`internal/engine/mergequeue.go` — added a `testGen *testgen.Service` field to
`mergeQueueTaskAdapter` and a new `testgen` import. `internal/engine/engine.go`
wires it at composition-root time:

```go
Tasks: &mergeQueueTaskAdapter{
    db:      opts.DB,
    sched:   e.sched,
    testGen: e.testGen,  // NEW
    log:     opts.Log,
},
```

`e.testGen` is the same `testgen.Service` already constructed for the
scheduler's `engineFamilyExcluder`, so there is exactly one instance per
engine. No second bus or second DB handle.

### 3.2 `CompleteTask` dispatches on `task_type`

The old implementation was a one-liner:

```go
func (a *mergeQueueTaskAdapter) CompleteTask(_ context.Context, taskID string) error {
    return a.db.UpdateTaskStatus(taskID, state.TaskDone)
}
```

The new implementation marks the task done (unchanged behaviour) and then, if
`testGen` is wired, reloads the task and dispatches on `task.TaskType`:

- `TaskTypeImplementation` → `dispatchImplementationMerge`
  - Lists every convergence pair in the task's run; if any pair's
    `fix_task_id` equals the current task ID, the completed task is itself a
    fix task, so we call `testGen.MarkConverged(ctx, cp.ImplTaskID)` on the
    original impl and return.
  - Otherwise this is a regular implementation merge, so we call
    `testGen.CreateTestTask(ctx, taskID)` to spawn the test task + convergence
    pair (`status=testing`).
- `TaskTypeTest` → `dispatchTestMerge`
  - Looks up the pair by test task ID and calls
    `testGen.MarkConverged(ctx, cp.ImplTaskID)`.

Hook errors are logged but not returned, because the git commit that the merge
queue just performed is irrevocable and a downstream testgen glitch must not
surface as a "failed merge". This matches the spirit of the existing dependent-
task unblocking path (§15.5), which also cannot be rolled back.

### 3.3 `RequeueTask` routes test-task failures through `HandleTestFailure`

The existing path for implementation/review tasks is preserved: transition
`in_progress → failed → queued` with failure feedback captured on the latest
attempt. A new branch runs when `task.TaskType == TaskTypeTest` and `testGen`
is wired:

1. Transition the task `in_progress → failed` (but **not** to `queued`; the
   test task stays in `failed`).
2. Call `testGen.HandleTestFailure(ctx, taskID, feedback)`, which spawns the
   `<impl-id>-fix-<iteration>` implementation task and transitions the pair to
   `fixing`.
3. Return without requeueing — the retry loop for this failure happens via the
   fix task, not by re-dispatching the same test meeseeks.

Errors from `HandleTestFailure` **are** propagated (unlike the CompleteTask
hooks), because here the merge-queue integration check has rejected the test
task and there is no committed artifact to protect.

### 3.4 `failAttempt` calls `MarkBlocked` on exhausted test meeseeks

`internal/engine/executor.go` — the existing call to `e.tasks.HandleTaskFailure`
now captures the returned `TaskFailureAction`. When the action is
`TaskFailureBlock` *and* the task type is `TaskTypeTest` *and* the engine has
a wired `testGen`, we look up the convergence pair via the test task ID and
call `e.testGen.MarkBlocked(ctx, cp.ImplTaskID)`.

This is intentionally narrower than the `RequeueTask` branch: meeseeks-level
failures (manifest errors, review rejections, container crashes, scope
expansion failures) must still funnel through the standard retry/escalate/block
decision tree. Only once that machinery decides to block — meaning all retries
and escalations are exhausted — do we reach into testgen to persist the final
state on the convergence pair.

### 3.5 `CompleteRun` convergence gate

`internal/engine/run.go` — `CompleteRun` now lists every convergence pair for
the run before calling `UpdateRunStatus`, collects the impl IDs whose pair is
not in `converged` status, and if any are non-converged returns a structured
error:

```
cannot complete run <id>: N convergence pair(s) still open: impl-1(testing), impl-2(fixing)
```

The gate is opt-out for `CancelRun` and `FailRun` by design — those transitions
record outcomes that differ from "completed" and the pair state is already
captured elsewhere in the audit trail.

## 4. Tests added

All tests live in `internal/engine/` and exercise the new hook points at both
the adapter layer (unit) and the engine layer (integration against a real
`testgen.Service` + SQLite state store).

### 4.1 `internal/engine/mergequeue_test.go` (new file)

- `TestMergeQueueTaskAdapter_HasTestGenField` — compile-time assertion that the
  adapter carries the `testGen` field (defends against a future refactor
  accidentally dropping the field).
- `TestMergeQueueCompleteTask_ImplMerge_SpawnsTestTask` — end-to-end unit test
  that an impl-task merge ends with the impl done, a queued test task
  (`impl-1-test`, type `test`, dependency on `impl-1`), and a convergence pair
  (`status=testing`, `impl_model_family=anthropic`, `test_task_id=impl-1-test`).
- `TestMergeQueueCompleteTask_TestMerge_MarksConverged` — seeds an impl +
  pair + test task, transitions the test task `queued → in_progress`, calls
  `CompleteTask`, and asserts the pair is `converged` with `converged_at` set.
- `TestMergeQueueCompleteTask_FixMerge_MarksConverged` — seeds a failing test,
  runs `HandleTestFailure` directly to spawn a fix task, drives the fix task
  through to `in_progress`, drives the test task to `done` (so `MarkConverged`'s
  invariant check passes), calls `CompleteTask(fixTask.ID)`, and asserts the
  pair is `converged`. This proves `dispatchImplementationMerge`'s
  fix-task-recognition loop works.
- `TestMergeQueueRequeueTask_TestTaskFailure_SpawnsFix` — seeds a test task in
  progress, calls `RequeueTask` with a failure message, and asserts:
    - the test task is `failed` (NOT requeued to `queued`),
    - the convergence pair is `fixing` with a non-empty `fix_task_id`,
    - the fix task exists with `task_type=implementation` and carries the
      failure output in its description.
- `TestMergeQueueRequeueTask_ImplTaskFailure_RequeuesNormally` — sanity check
  that the new test-task branch does not regress the impl-task path: a failing
  impl task still transitions `in_progress → failed → queued` and no
  convergence pair is created.
- `TestMergeQueueCompleteTask_ImplMerge_EndToEnd_FamilyExclusion` — engine-
  level end-to-end regression that drives `CompleteTask` through the full
  `dispatchImplementationMerge` → `testgen.CreateTestTask` → convergence pair
  path and additionally asserts that `testGen.GetExcludeFamily(testTaskID)`
  returns the impl's family. This is the exact call the scheduler's
  `engineFamilyExcluder` makes at dispatch time, so the test proves the full
  impl-merge → test-dispatch-with-different-family loop closes.

### 4.2 `internal/engine/executor_test.go` (additions)

- `TestFailAttempt_TestTaskBlock_MarksConvergenceBlocked` — flips the shared
  `mockTaskService.failureAction` to `TaskFailureBlock`, seeds an impl + pair
  + test task with a running attempt, calls `Engine.failAttempt(testTask, ...)`
  directly, and asserts the convergence pair is in `ConvergenceBlocked`.
- `TestFailAttempt_ImplTaskBlock_DoesNotMarkConvergence` — symmetry check: a
  `TaskFailureBlock` action on an *implementation*-type task (not a test task)
  must leave an unrelated convergence pair untouched. This guards against a
  future refactor accidentally removing the type guard in `failAttempt`.

### 4.3 `internal/engine/run_test.go` (additions)

- `TestCompleteRun_BlockedByPendingConvergence` — creates an active run, seeds
  a `ConvergenceTesting` pair, calls `CompleteRun`, and asserts the call
  returns an error whose message contains the impl task ID and the pair status,
  and that the run status did **not** transition to `completed`.
- `TestCompleteRun_AllowedAfterConvergence` — same setup but transitions the
  pair to `ConvergenceConverged` first, then asserts `CompleteRun` succeeds
  and the run is `completed`.
- `TestCompleteRun_BlockedByBlockedPair` — verifies that a `ConvergenceBlocked`
  pair also blocks completion (not just `testing`/`fixing`). Blocked pairs are
  a terminal failure that must not be silently ignored.

The existing `TestCompleteRun` (which creates a run with zero convergence
pairs) continues to pass unchanged — the gate is a pass-through when the list
is empty.

## 5. Verification

### 5.1 Build

```
$ go build ./...
(clean)
$ go build ./cmd/axiom
(clean)
$ go vet ./...
(clean)
```

### 5.2 Tests

All relevant packages pass, including the new adapter/engine tests and the
entire pre-existing `testgen`/`state`/`mergequeue` suites:

```
ok   github.com/openaxiom/axiom/internal/testgen      1.975s
ok   github.com/openaxiom/axiom/internal/state        4.658s
ok   github.com/openaxiom/axiom/internal/mergequeue   0.563s
ok   github.com/openaxiom/axiom/internal/scheduler    1.643s
ok   github.com/openaxiom/axiom/internal/validation   0.747s
ok   github.com/openaxiom/axiom/internal/task         1.553s
ok   github.com/openaxiom/axiom/internal/review       0.750s
```

Engine-level tests (excluding three pre-existing Windows-specific
`scriptedContainerService` tests whose IPC write uses a relative path that
does not exist on the Windows development host — see §5.3):

```
ok   github.com/openaxiom/axiom/internal/engine   2.908s
```

The engine suite executed includes every test in the package except the three
that hang on Windows on `main` *before* any changes from this fix. The suite
includes:

- `TestNew` / `TestEngine_*` — constructor and lifecycle
- `TestCreateRun` / `TestPauseRun` / `TestResumeRun` / `TestCancelRun` / `TestCompleteRun` / `TestFailRun` — run lifecycle
- `TestCompleteRun_BlockedByPendingConvergence` / `TestCompleteRun_AllowedAfterConvergence` / `TestCompleteRun_BlockedByBlockedPair` — NEW gate tests
- `TestMergeQueueTaskAdapter_HasTestGenField` — NEW field assertion
- `TestMergeQueueCompleteTask_ImplMerge_SpawnsTestTask` — NEW impl-merge path
- `TestMergeQueueCompleteTask_TestMerge_MarksConverged` — NEW test-merge path
- `TestMergeQueueCompleteTask_FixMerge_MarksConverged` — NEW fix-merge path
- `TestMergeQueueCompleteTask_ImplMerge_EndToEnd_FamilyExclusion` — NEW end-to-end
- `TestMergeQueueRequeueTask_TestTaskFailure_SpawnsFix` — NEW fail-loop
- `TestMergeQueueRequeueTask_ImplTaskFailure_RequeuesNormally` — NEW regression guard
- `TestFailAttempt_TestTaskBlock_MarksConvergenceBlocked` — NEW block hook
- `TestFailAttempt_ImplTaskBlock_DoesNotMarkConvergence` — NEW regression guard
- `TestMergeQueue_RealValidator_BlocksBrokenGoBuild` and siblings — Issue 04 integration tests (unchanged, still green)
- All SRS, startrun, status, worker, recovery tests

Non-engine, non-testgen tests for every other project package (app, cli, api,
tui, bitnet, container, doctor, eco, events, gitops, index, inference, ipc,
manifest, models, observability, project, release, security, session, skill,
srs, version, and the `cmd/axiom` acceptance suite) were also re-run and all
pass.

### 5.3 Pre-existing Windows hang — not caused by this fix

Three tests in `internal/engine/executor_test.go` hang on the current Windows
development host:

- `TestExecuteAttempt_SuccessEnqueuesAndMerges`
- `TestExecuteAttempt_ValidationFailureRequeuesTask`
- `TestEngineWorkers_SchedulerExecutorMergeQueueFlow`

The hang is in `Engine.monitorTaskIPC`, waiting for an IPC message that
`scriptedContainerService` writes to a path resolved via `mountHostPath`. On
Windows, the IPC mount path ends up being resolved such that the scripted
goroutine writes to a relative path like `output\msg-000001.json`, which the
OS rejects with "The system cannot find the path specified." The monitor then
waits forever.

This was verified to be pre-existing by `git stash`-ing all changes from this
fix, building on a clean `main` (commit `47e40fa`), and running the same
tests: the hang reproduces identically. It is unrelated to Issue 05 and falls
outside the scope of this fix. (Fixing it would require rewriting
`scriptedContainerService`'s mount-path resolution or skipping the three tests
on `GOOS=windows` — neither is in scope.)

### 5.4 Acceptance criteria check

From the plan's §7:

- [x] `go test ./internal/testgen/...` passes — confirmed.
- [x] `go test ./internal/engine/...` passes (modulo the pre-existing Windows
  hang) with every new assertion listed.
  - [x] Impl merge → `impl-1-test` created, pair in `testing`, family recorded.
  - [x] Test merge → pair `converged` + `converged_at` set.
  - [x] Fix-task merge → pair `converged`.
  - [x] Test-task merge failure → `HandleTestFailure` fires, fix task created,
        pair in `fixing`, test task stays in `failed`.
  - [x] Test meeseeks block → `MarkBlocked` fires, pair in `blocked`.
  - [x] `CompleteRun` rejects non-converged and accepts all-converged runs.
- [x] `grep -rn "CreateTestTask\|MarkConverged\|HandleTestFailure\|MarkBlocked" --include="*.go" internal/engine/`
      now returns runtime call sites in `mergequeue.go` and `executor.go`, not
      just tests.
- [x] `docs/test-generation.md` no longer contains any "not yet invoked
      automatically" caveats; replaced with runtime-hook descriptions.
- [x] `go build ./cmd/axiom` succeeds.

## 6. Files changed

| File | Kind | Role |
|---|---|---|
| `internal/engine/mergequeue.go` | modified | Added `testGen` field + `dispatchImplementationMerge` / `dispatchTestMerge` helpers; rewrote `CompleteTask` and `RequeueTask` to branch on `task_type` |
| `internal/engine/engine.go` | modified | Wire `e.testGen` into `mergeQueueTaskAdapter` at composition-root time |
| `internal/engine/executor.go` | modified | Capture action from `HandleTaskFailure` and call `testGen.MarkBlocked` when action is Block on a test-type task |
| `internal/engine/run.go` | modified | Convergence gate in `CompleteRun` via `ListConvergencePairsByRun` |
| `internal/engine/mergequeue_test.go` | **new** | Adapter-layer unit tests for all three `CompleteTask` branches, both `RequeueTask` branches, and the end-to-end family-exclusion test |
| `internal/engine/executor_test.go` | modified | Two new `failAttempt` tests (test-task block, impl-task-block regression guard) |
| `internal/engine/run_test.go` | modified | Three new `CompleteRun` gate tests (blocked-by-testing, allowed-after-converged, blocked-by-blocked-pair) |
| `docs/test-generation.md` | modified | Removed all "not yet invoked automatically" caveats; rewrote the lifecycle diagram to describe the engine-wired hooks |
| `docs/approval-pipeline.md` | modified | Added a subsection on post-merge test-generation hooks describing all six hook points |

## 7. Non-goals (respected)

- `internal/testgen/testgen.go` was **not** modified. The service's API is
  the contract; this fix is pure wiring.
- No new database columns, no new state helpers, no new events. The existing
  `ListConvergencePairsByRun`, `GetConvergencePairByImplTask`, and
  `GetConvergencePairByTestTask` helpers cover every lookup needed.
- The actual execution of generated tests against committed code during
  merge-queue integration checks is the responsibility of Issue 04's real
  validation runner — this fix is independent. When Issue 04's runner rejects
  a test-task output, our new `RequeueTask` branch is what converts that
  rejection into a fix-loop.
- The three pre-existing Windows executor-pipeline hangs were not fixed
  because they are unrelated to Issue 05 and reproduce on a clean `main`.
