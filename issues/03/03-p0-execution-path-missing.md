# Issue 03 - P0 - The execution path from scheduled task to container output to approval pipeline is missing

## Status

- Severity: P0
- State: Reproduced and root-caused
- Last reviewed: 2026-04-06
- Source: `issues.md` finding 3

## Expected behavior

When the scheduler dispatches a task (moves it to `in_progress` and creates an attempt record), the engine should execute the full pipeline defined in Architecture Section 5.1 steps 7a-7w:

1. Build a TaskSpec with minimum necessary structured context (objective, context blocks, interface contract, constraints, acceptance criteria)
2. Create per-task IPC directories (spec, staging, ipc/input, ipc/output)
3. Write the TaskSpec to the spec directory
4. Start a Meeseeks Docker container with the spec, staging, and IPC directories mounted
5. Monitor the container for completion (broker inference requests via IPC)
6. Collect the Meeseeks output and `manifest.json` from the staging directory
7. Run manifest validation (Stage 1: path safety, file existence, scope enforcement)
8. Run validation sandbox checks (Stage 2: compile, lint, test in isolated container)
9. If validation fails: destroy Meeseeks, spawn fresh container with failure feedback (max 3 retries per tier, then escalate)
10. If validation passes: spawn reviewer container with ReviewSpec (Stage 3)
11. Parse reviewer verdict (APPROVE/REJECT)
12. If reviewer rejects: destroy both containers, spawn fresh Meeseeks with reviewer feedback
13. If reviewer approves: run orchestrator gate (Stage 4)
14. If gate approves: enqueue approved output to merge queue (Stage 5)
15. Clean up all containers and IPC directories

## Actual behavior

The scheduler's `dispatch` method (`internal/scheduler/scheduler.go:270-329`) only:

1. Selects a model for the task tier
2. Gets the current HEAD for `base_snapshot` pinning
3. Transitions the task to `in_progress`
4. Creates an attempt record
5. Logs "task dispatched"

After dispatch, **nothing happens**. The task sits in `in_progress` indefinitely. No container is started, no output is produced, no validation runs, and no code is ever committed. The five-stage approval pipeline exists as isolated, well-tested packages, but no runtime code composes them into a working execution flow.

## Reproduction

### Method 1: Code-level verification

Searched the entire non-test codebase for runtime callers of the pipeline components:

| Function | Package | Non-test call sites |
|----------|---------|---------------------|
| `ipc.WriteTaskSpec` | `internal/ipc` | **0** — only called in `ipc/spec_test.go` |
| `ipc.WriteReviewSpec` | `internal/ipc` | **0** — only called in `ipc/spec_test.go` |
| `ipc.CreateTaskDirs` | `internal/ipc` | **0** — only called in `ipc/dirs_test.go` and `ipc/spec_test.go` |
| `validation.NewService` | `internal/validation` | **0** — only called in `validation/validation_test.go` |
| `validation.RunChecks` | `internal/validation` | **0** — only via test |
| `review.NewService` | `internal/review` | **0** — only called in `review/review_test.go` |
| `review.RunReview` | `internal/review` | **0** — only via test |
| `manifest.ParseManifest` | `internal/manifest` | **0** — only called in `manifest/manifest_test.go` |
| `manifest.ValidateManifest` | `internal/manifest` | **0** — only via test |
| `engine.EnqueueMerge` | `internal/engine` | **0** — only defined, never invoked |
| `task.HandleTaskFailure` | `internal/task` | **0** — only called in `task/service_test.go` |

Every pipeline package is tested in isolation. None are wired into the live runtime.

### Method 2: Trace the dispatch path

Following the code from `Engine.Start()` through to where work should happen:

1. `engine.Start()` registers the scheduler loop (`engine/engine.go:128`)
2. `schedulerLoop` calls `sched.Tick(ctx)` (`engine/scheduler.go:12-14`)
3. `Tick` finds ready tasks and calls `dispatch()` (`scheduler/scheduler.go:270-329`)
4. `dispatch()` updates status and creates an attempt record
5. **No further action** — dispatch returns, the scheduler moves on to the next tick

The scheduler has no callback, no channel, no event emission, and no container service reference that would trigger actual execution after dispatch.

### Method 3: Runtime confirmation

With Issue 02 fixed (engine workers now start), if tasks were queued in an active run, the scheduler would dispatch them every 500ms. But dispatched tasks would simply transition to `in_progress` and stay there permanently, because no execution worker exists to act on them.

## Root cause

### 1. The scheduler is a dispatch-only component

The scheduler (`internal/scheduler/scheduler.go`) was designed and implemented purely as a task-scheduling and lock-management component. Per its own documentation and test coverage, it:

- Finds dependency-free queued tasks
- Acquires write-set locks atomically
- Moves tasks to `in_progress` and creates attempt records
- Manages `waiting_on_lock` transitions

It does **not** have access to, or knowledge of:
- The container service (`engine.ContainerService`)
- The IPC system (`internal/ipc`)
- The inference broker (`engine.InferenceService`)
- The validation service (`internal/validation`)
- The review service (`internal/review`)
- The manifest validator (`internal/manifest`)
- The merge queue (`internal/mergequeue`)
- The task failure handler (`internal/task`)

This is architecturally correct — the scheduler should only handle scheduling concerns. But the missing piece is an **execution worker** (or "attempt executor") that acts on newly dispatched tasks.

### 2. No execution worker was ever built

The architecture defines the execution flow in Section 5.1 steps 7a-7w and references it across Sections 10 (Meeseeks), 12 (Docker Sandbox), 13 (Validation Sandbox), 14 (File Router & Approval Pipeline), and 20 (Communication Model). The implementation plan covers it across Phases 5, 10, 11, and 12.

Each subsystem was built as an independent, well-tested package:
- `internal/ipc` — IPC directory management, TaskSpec/ReviewSpec writers, message protocol
- `internal/container` — Docker container lifecycle with full hardening
- `internal/manifest` — Manifest parsing, validation, artifact hashing
- `internal/validation` — Validation sandbox service with language profiles
- `internal/review` — Reviewer pipeline with risky-file escalation, model diversification, verdict parsing
- `internal/mergequeue` — Serialized merge queue with conflict detection and revert
- `internal/task` — Retry/escalation/blocking failure handler

But no component was ever built to **compose these packages into the sequential execution pipeline** that the architecture describes. The individual packages are building blocks without an assembler.

### 3. The engine has no hook between dispatch and execution

The engine wires the scheduler and merge queue as background worker loops (`engine/engine.go:128-129`). But there is no third worker — an "execution" or "attempt-processing" loop — that:
- Watches for newly dispatched tasks (status `in_progress`, attempt status `running`)
- Builds and delivers TaskSpecs
- Manages container lifecycle
- Brokers IPC communication
- Feeds results through the approval pipeline
- Handles failures via retry/escalation
- Enqueues successful output to the merge queue

### 4. Phase-by-phase development created the gap

The implementation plan organized work into phases by subsystem:
- Phase 5: IPC, Container Lifecycle
- Phase 10: Task System, Scheduler
- Phase 11: Manifest Validation, Validation Sandbox, Review Pipeline
- Phase 12: Merge Queue

Each phase was completed in isolation with good test coverage. But the cross-cutting integration — the attempt executor that threads through all phases — was never explicitly owned by a single phase or implementation step.

## Fix plan

### Design overview

Introduce an **Attempt Executor** — a new engine component that processes dispatched task attempts through the full approval pipeline. The executor is registered as a third engine background worker alongside the scheduler and merge queue.

The executor is the missing glue layer: it consumes the scheduler's output (tasks in `in_progress` with `running` attempts) and feeds the merge queue's input (approved `MergeItem` structs). All existing packages are used as-is.

### Architecture alignment

The attempt executor implements Architecture Section 5.1 steps 7a-7w and composes:
- Section 10.3: TaskSpec delivery
- Section 12: Docker container lifecycle
- Section 13: Validation sandbox
- Section 14.2: Five-stage approval pipeline
- Section 20: IPC communication model
- Section 28.1: Per-task IPC directories
- Section 30.1: Retry/escalation on failure

### Component: `internal/engine/executor.go`

New file in the engine package. The executor runs as a background worker registered alongside the scheduler and merge queue.

#### Worker loop

```
executorLoop (ticks every 500ms):
  1. Query for attempts in status "running" at phase "executing"
  2. For each, launch executeAttempt() in a goroutine (bounded by maxMeeseeks)
  3. Track active goroutines to avoid re-processing the same attempt
```

#### Attempt lifecycle: `executeAttempt(ctx, attempt)`

```
Phase 1: SETUP
  a. Load task and attempt from state
  b. Create IPC directories: ipc.CreateTaskDirs(rootDir, taskID)
  c. Build TaskSpec from task data, attempt context, and prior feedback
  d. Write TaskSpec: ipc.WriteTaskSpec(dirs.Spec, spec)

Phase 2: MEESEEKS EXECUTION
  a. Build container spec with IPC mounts, hardening flags
  b. Start Meeseeks container: container.Start(ctx, spec)
  c. Update attempt phase → "executing"
  d. Monitor container for completion (poll ipc/output for task_output message)
  e. Broker inference requests (poll ipc/output for inference_request, respond via ipc/input)
  f. On container exit: collect output from staging directory
  g. Stop and destroy container: container.Stop(ctx, containerID)

Phase 3: MANIFEST VALIDATION (Stage 1)
  a. Parse manifest: manifest.ParseManifest(manifestBytes)
  b. Validate manifest: manifest.ValidateManifest(m, stagingDir, allowedScope, cfg)
  c. Compute artifact hashes: manifest.ComputeArtifacts(m, stagingDir, attemptID)
  d. If validation fails → go to FAILURE HANDLING
  e. Update attempt phase → "validating"

Phase 4: VALIDATION SANDBOX (Stage 2)
  a. Detect project languages: validation.DetectLanguages(rootDir)
  b. Run validation checks: validationSvc.RunChecks(ctx, req)
  c. Format results: validation.FormatResults(results)
  d. If any check fails → go to FAILURE HANDLING
  e. Update attempt phase → "reviewing"

Phase 5: REVIEWER (Stage 3)
  a. Build ReviewSpec with original TaskSpec, Meeseeks output, validation results
  b. Write ReviewSpec: ipc.WriteReviewSpec(reviewDirs.Spec, reviewSpec)
  c. Run review: reviewSvc.RunReview(ctx, req)
  d. Parse verdict (already done inside RunReview)
  e. If REJECT → go to FAILURE HANDLING with reviewer feedback
  f. Update attempt phase → "awaiting_orchestrator_gate"

Phase 6: ORCHESTRATOR GATE (Stage 4)
  a. Run gate: review.OrchestratorGate(req)
  b. If rejected → go to FAILURE HANDLING with gate feedback
  c. Update attempt phase → "queued_for_merge"

Phase 7: ENQUEUE FOR MERGE (Stage 5)
  a. Build MergeItem from manifest, commit info, staging dir
  b. Enqueue: engine.EnqueueMerge(item)
  c. Update attempt status → "succeeded"
  d. Clean up IPC directories: ipc.CleanupTaskDirs(rootDir, taskID)

FAILURE HANDLING:
  a. Update attempt status → "failed" with feedback
  b. Clean up containers and IPC directories
  c. Call task.HandleTaskFailure(ctx, taskID, feedback)
     - If retry: task goes back to queued → scheduler picks it up next tick
     - If escalate: task tier upgrades, goes back to queued
     - If block: task is blocked, orchestrator notified
```

### Step-by-step implementation

#### Step 1: Create the TaskSpec builder

**New file:** `internal/engine/taskspec.go`

Build TaskSpecs from task state and engine context. This is the bridge between the state layer and the IPC spec format:

- Read task record and its target files from state
- Query the semantic index for context at the appropriate tier (symbol, file, package, repo-map)
- Include prior attempt feedback if this is a retry
- Include interface contracts from task metadata
- Populate acceptance criteria from SRS traceability refs

This builder uses `engine.IndexService` for context resolution and `state.DB` for task/attempt data.

#### Step 2: Create the IPC monitor

**New file:** `internal/engine/ipcmonitor.go`

A helper that watches a Meeseeks container's IPC output directory for messages and routes them:

- `inference_request` → broker via `engine.InferenceService`, write response to IPC input
- `task_output` → signal completion, stop monitoring
- `action_request` → validate and execute via engine, write response
- `request_scope_expansion` → delegate to `task.RequestScopeExpansion`
- Timeout handling: if no message within container timeout, kill container

This is a polling loop that checks the IPC output directory on a short interval (~100ms) and dispatches messages.

#### Step 3: Create the attempt executor

**New file:** `internal/engine/executor.go`

The core execution worker as described in the design overview above. It:

- Depends on: `state.DB`, `ContainerService`, `InferenceService`, `IndexService`, `GitService`, `task.Service`, `validation.Service`, `review.Service`
- Is constructed in `engine.New()` alongside the scheduler and merge queue
- Is registered as a worker in `engine.Start()`

Key design decisions:
- **One goroutine per active attempt**, bounded by `maxMeeseeks` from config
- **Idempotent**: if an attempt is already being processed (tracked in a sync.Map), skip it
- **Phase-aware recovery**: on engine restart, `Recover()` can inspect attempt phases and resume or retry
- **Container cleanup on context cancellation**: defer-based cleanup ensures containers are always destroyed

#### Step 4: Wire services into the engine

**Modified file:** `internal/engine/engine.go`

Add new service dependencies to `Engine` struct and `Options`:

```go
type Options struct {
    // ... existing fields ...
    Validation  ValidationService  // new
    Review      ReviewService      // new
    Tasks       TaskService        // new
}
```

Where the service interfaces are:

```go
// ValidationService runs automated checks in a validation sandbox.
type ValidationService interface {
    RunChecks(ctx context.Context, req ValidationCheckRequest) ([]ValidationCheckResult, error)
}

// ReviewService orchestrates the reviewer pipeline.
type ReviewService interface {
    RunReview(ctx context.Context, req ReviewRunRequest) (*ReviewRunResult, error)
}

// TaskService handles task lifecycle operations.
type TaskService interface {
    HandleTaskFailure(ctx context.Context, taskID string, feedback string) (FailureAction, error)
}
```

#### Step 5: Wire services in the composition root

**Modified file:** `internal/app/app.go`

Construct the validation, review, and task services in `Open()` and pass them to `engine.New()`:

```go
// Create validation service
validationSvc := validation.NewService(validation.ServiceOptions{
    Containers: containerSvc,
    Log:        log,
    Runner:     validation.NewDockerCheckRunner(containerSvc), // new: real runner
})

// Create review service
reviewSvc := review.NewService(review.ServiceOptions{
    Containers: containerSvc,
    Models:     models.NewRegistryAdapter(registry),
    Runner:     review.NewDockerReviewRunner(containerSvc), // new: real runner
    Log:        log,
})

// Create task service
taskSvc := task.NewService(task.ServiceOptions{
    DB:  db,
    Log: log,
})

eng, err := engine.New(engine.Options{
    // ... existing fields ...
    Validation: validationSvc,
    Review:     reviewSvc,
    Tasks:      taskSvc,
})
```

#### Step 6: Register the executor as an engine worker

**Modified file:** `internal/engine/engine.go`

In `Start()`:

```go
e.workers.Register("scheduler", e.schedulerLoop, 500*time.Millisecond)
e.workers.Register("merge-queue", e.mergeQueueLoop, 500*time.Millisecond)
e.workers.Register("executor", e.executorLoop, 500*time.Millisecond)  // NEW
```

#### Step 7: Implement real CheckRunner and ReviewRunner

The validation and review packages use `CheckRunner` and `ReviewRunner` interfaces. Currently only mock implementations exist for tests. We need real implementations:

**New file:** `internal/validation/docker_runner.go`

A `DockerCheckRunner` that:
- Executes language-specific validation commands inside the running container
- Collects stdout/stderr for each check
- Returns structured `CheckResult` slices

**New file:** `internal/review/docker_runner.go`

A `DockerReviewRunner` that:
- Waits for the reviewer container to complete
- Reads the reviewer's output from the IPC output directory
- Returns the raw output string for verdict parsing

#### Step 8: Replace the merge queue stub validator

**Modified file:** `internal/engine/mergequeue.go`

Replace `mergeQueueValidatorAdapter` with a real implementation that delegates to `validation.Service`:

```go
type mergeQueueValidatorAdapter struct {
    validation ValidationService
    rootDir    string
    log        *slog.Logger
}

func (a *mergeQueueValidatorAdapter) RunIntegrationChecks(ctx context.Context, projectDir string) (bool, string, error) {
    results, err := a.validation.RunChecks(ctx, ValidationCheckRequest{
        ProjectDir: projectDir,
        // ... integration check config
    })
    if err != nil {
        return false, "", err
    }
    allPassed := validation.AllPassed(results)
    output := validation.FormatResults(results)
    return allPassed, output, nil
}
```

Note: this also addresses Issue 04 (merge queue stub validator), but it is a natural part of wiring the execution path.

#### Step 9: Add attempt phase tracking

The attempt phase should advance through the pipeline stages. Update the executor to call `state.DB.UpdateAttemptPhase()` at each transition:

```
executing → validating → reviewing → awaiting_orchestrator_gate → queued_for_merge → succeeded
                                                                                        or
                                                                            → failed (at any stage)
```

This enables crash recovery: on restart, `Recover()` can inspect the phase of interrupted attempts and decide whether to retry or clean up.

#### Step 10: Add tests

##### 10a. Unit test: executor processes a dispatched task end-to-end

Using mock implementations of `ContainerService`, `InferenceService`, `ValidationService`, `ReviewService`, and `TaskService`:

- Create an active run with a task in `in_progress` and a `running` attempt
- Tick the executor
- Assert: IPC dirs created, TaskSpec written, container started, output collected, manifest validated, sandbox checks run, review run, merge enqueued
- Assert: attempt phase advanced through all stages to `succeeded`
- Assert: IPC dirs cleaned up, containers stopped

##### 10b. Unit test: executor handles validation failure with retry

- Mock the validation service to return a failing check
- Assert: attempt marked failed, `HandleTaskFailure` called, task requeued
- Assert: containers and IPC dirs cleaned up

##### 10c. Unit test: executor handles reviewer rejection

- Mock the review service to return REJECT
- Assert: attempt marked failed with reviewer feedback, task requeued

##### 10d. Unit test: executor handles tier escalation

- Set up a task with max retries exhausted at current tier
- Assert: `HandleTaskFailure` returns `ActionEscalate`, task tier upgraded

##### 10e. Unit test: executor handles task blocking

- Set up a task with all retries and escalations exhausted
- Assert: `HandleTaskFailure` returns `ActionBlock`, task marked blocked

##### 10f. Unit test: executor respects maxMeeseeks concurrency

- Queue more tasks than maxMeeseeks
- Assert: only maxMeeseeks goroutines are active simultaneously

##### 10g. Integration test: scheduler -> executor -> merge queue end-to-end

- Create an active run with queued tasks
- Start the engine (scheduler + executor + merge queue all running)
- Assert: within a few ticks, tasks are dispatched, executed, and committed
- Assert: task status reaches `done`, commit exists in git log

#### Step 11: Update documentation

**Files to update:**

1. `docs/approval-pipeline.md` — Remove the "two notable wiring gaps" note at the top. Update the pipeline flow description to reflect the executor as the runtime orchestrator.

2. `docs/ipc-container.md` — Add a section on how the executor uses IPC directories and the message protocol at runtime.

3. `docs/task-scheduler.md` — Clarify the boundary between the scheduler (dispatch) and the executor (execution). Add documentation for the executor worker.

4. `docs/getting-started.md` — Update execution flow descriptions to reflect that tasks are now actually executed.

5. `docs/development.md` — Add the new files (`executor.go`, `taskspec.go`, `ipcmonitor.go`, `docker_runner.go` files) to the package inventory.

### Implementation order

1. **Step 1: TaskSpec builder** — Pure data transformation, no external deps. Testable immediately.
2. **Step 2: IPC monitor** — File-watching helper. Testable with temp dirs.
3. **Step 7: Real CheckRunner and ReviewRunner** — Container interaction. Can be developed with mock executor.
4. **Step 3: Attempt executor** — Core integration. Depends on steps 1-2 and step 7.
5. **Step 4-5: Engine and app wiring** — Inject new services.
6. **Step 6: Register executor worker** — Single line addition.
7. **Step 8: Replace stub validator** — Small adapter change.
8. **Step 9: Phase tracking** — Incremental addition to executor.
9. **Step 10: Tests** — Should be written alongside each step (TDD style).
10. **Step 11: Documentation** — After implementation is verified.

### Risk assessment

| Risk | Mitigation |
|------|-----------|
| Executor goroutines leak on shutdown | Defer-based cleanup + context cancellation + sync.WaitGroup |
| Container left running on crash | `Recover()` already calls `container.Cleanup()` for orphans |
| IPC dirs left on crash | `Recover()` should scan for orphaned IPC dirs and clean up |
| Inference broker not wired (Issue 07) | Executor should fail gracefully if `InferenceService` is nil — log and skip inference brokering. Meeseeks that need inference will time out and be retried when the broker is available. |
| Race between scheduler dispatch and executor pickup | The executor tracks active attempts in a `sync.Map` keyed by attempt ID. The scheduler only creates attempts; the executor only processes them. No shared mutation. |
| Slow container operations block the executor loop | Container operations run in per-attempt goroutines, not in the tick loop itself. The tick loop only launches goroutines and checks for new work. |

## Acceptance criteria

1. A queued task in an active run is dispatched by the scheduler and executed by the attempt executor within a few tick intervals.
2. The full pipeline runs: TaskSpec -> Meeseeks container -> manifest validation -> validation sandbox -> reviewer -> orchestrator gate -> merge queue.
3. Successful task output is committed to git with architecture-compliant commit messages.
4. Validation failures trigger retry/escalation per Architecture Section 30.1.
5. Reviewer rejections trigger fresh Meeseeks with feedback per Architecture Section 14.2.
6. Task exhaustion (max retries + max escalations) results in task blocking.
7. Containers are always cleaned up, even on failure or engine shutdown.
8. IPC directories are always cleaned up after attempt completion or failure.
9. Attempt phases advance through the full lifecycle and are queryable for crash recovery.
10. `go test ./...` passes with the new executor tests.

## Dependencies on other issues

- **Issue 01 (external orchestrator handoff):** The executor processes tasks that are already decomposed and queued. Task creation happens upstream (by the orchestrator via the API). Issue 01 must be resolved for tasks to exist in the first place.
- **Issue 02 (engine workers never started):** Already fixed. The engine now starts background workers in `app.Open()`.
- **Issue 04 (stub merge queue validator):** Addressed as part of Step 8 in this plan. The stub is replaced with a real validation adapter.
- **Issue 07 (inference broker not wired):** The executor can function without the inference broker for simple tasks, but Meeseeks that need model inference will fail until Issue 07 is resolved. The executor should handle `nil` inference gracefully.

## Notes

- The executor is deliberately a **composition layer**, not a monolith. It calls into existing packages (`ipc`, `container`, `manifest`, `validation`, `review`, `mergequeue`, `task`) without duplicating their logic. Each package retains its own tests and interface boundaries.
- The IPC monitor (Step 2) is the most complex new component because it must handle asynchronous container communication. Consider starting with a synchronous "wait for container exit, then read output" approach and adding real-time inference brokering in a follow-up.
- The executor's goroutine-per-attempt model matches the architecture's concurrency model: up to `maxMeeseeks` concurrent containers, bounded by the same config value the scheduler uses.
- This fix, combined with the Issue 02 fix (engine workers started), completes the runtime execution loop. After this, a queued task will flow through the full lifecycle to a git commit.
