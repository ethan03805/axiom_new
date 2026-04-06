# Axiom Architecture Review Findings

## Scope and method

Reviewed:

- `ARCHITECTURE.md`
- `IMPLEMENTATION_PLAN.md`
- `docs/`
- `cmd/`
- `internal/`

Verification performed:

- `go test ./...`
- `go build ./cmd/axiom`
- two throwaway CLI smoke tests in temporary git repositories

High-level result:

- The package-level code is generally well tested.
- The architecture-critical runtime composition is not complete.
- In its current state, Axiom does not work out of the box for a non-technical user. The main failure mode is not compile-time breakage; it is that the end-to-end workflow stops after run creation or relies on packages that are never wired into the live runtime.

Smoke-test notes:

- In a clean temp repo, `axiom init` followed by `axiom run "Build a REST API"` created a run in `draft_srs`, did not create `.axiom/srs.md`, and did not switch the repo to `axiom/<slug>`.
- In a dirty temp repo, `axiom run` still succeeded even with uncommitted changes.

## Findings

### 1. P0: `axiom run` dead-ends at `draft_srs`, and the user prompt is ignored

Evidence:

- `internal/cli/run.go:42-57` accepts `prompt` but never uses it when calling the engine.
- `internal/engine/run.go:22-78` only creates a `project_runs` row and emits `run_created`.
- `internal/engine/srs.go:12-40` implements `SubmitSRS`, but there is no non-test caller that advances a run from `draft_srs` to `awaiting_srs_approval`.
- `internal/api/server.go:172-185` exposes `POST /run`, but there is no matching submit-SRS route; the API only exposes approve/reject later in the lifecycle.
- `internal/cli/run.go:16-20` still describes the command as "generate SRS, await approval, execute."

Why this is a production blocker:

- A first-time user can start a run, but the system does not generate an SRS, does not persist a draft, and does not expose a normal user path to continue.
- The core product promise in the architecture and quick-start docs is "describe what to build, review the SRS, then execute." The current runtime stops at step 1.

Likely root cause:

- The architecture assumes an orchestrator/bootstrap layer, but the composition root never wires an embedded orchestrator or an external-orchestrator handoff that actually consumes the run prompt and calls the SRS lifecycle.

Architecture sections:

- `5.1 Project Lifecycle`
- `6.1-6.3 SRS Format / Immutability / Traceability`
- `8 Orchestrator`
- `8.7 Bootstrap Mode`
- `26.2.4 Startup Experience`

Implementation plan phases:

- `Phase 9: SRS, ECO, and Bootstrap-Mode Workflow`
- `Phase 14: Plain CLI Command Surface`
- `Phase 15: Session UX Manager and Bubble Tea TUI`
- `Phase 16: API Server, WebSockets, and Tunnel Support`

Docs affected:

- `docs/getting-started.md`
- `docs/cli-reference.md:136-160`
- `docs/srs-eco.md`

Potential solutions:

- Introduce a real bootstrap/orchestrator entrypoint that consumes the run prompt and generates an SRS draft.
- Call `Engine.SubmitSRS` as part of that bootstrap path.
- Add a first-class operator surface for the SRS lifecycle in CLI/TUI/API: view draft, approve, reject, resubmit.
- Add an end-to-end acceptance test for `init -> run -> srs draft exists -> approve -> run becomes active`.

### 2. P0: Engine background workers are never started in real app flows

Evidence:

- `internal/app/app.go:83-101` creates the engine and runs `Recover`, but never calls `Engine.Start`.
- `internal/engine/engine.go:116-141` shows that `Start` is what actually registers and starts the scheduler and merge-queue worker loops.
- `internal/cli/stubs.go:27-48` starts the API server directly from `app.Open()` without ever starting the engine runtime.
- A non-test search found no runtime call site for `Engine.Start`.

Why this is a production blocker:

- Even if tasks were created correctly, the scheduler and merge queue would not run in CLI, TUI, or API sessions.
- The system can show state, but it cannot drive the background lifecycle defined by the architecture.

Likely root cause:

- The composition root treats the engine as a collection of synchronous methods instead of a long-lived runtime with required workers.

Architecture sections:

- `15 Task System`
- `16 Concurrency, Snapshots & Merge Queue`
- `22 State Management & Crash Recovery`
- `24.2 API Server`

Implementation plan phases:

- `Phase 3: Engine Kernel and Event Infrastructure`
- `Phase 10: Task System, Scheduler, and Locking`
- `Phase 12: Merge Queue and Integration Checks`
- `Phase 16: API Server, WebSockets, and Tunnel Support`
- `Phase 19: Crash Recovery, Observability, and Operational Hardening`

Docs affected:

- `docs/task-scheduler.md`
- `docs/api-server.md`
- `docs/session-tui.md`

Potential solutions:

- Add an application lifecycle method that starts the engine for long-lived modes.
- Start the engine in `axiom tui` and `axiom api start`.
- Decide whether `axiom run` should also start the runtime or should hand off to a daemon/session process.
- Add a startup assertion in server/TUI modes that fails fast if the engine is not running.

### 3. P0: The execution path from scheduled task to container output to approval pipeline is missing

Evidence:

- `internal/scheduler/scheduler.go:270-329` marks a task `in_progress` and creates an attempt, but does not emit a `TaskSpec`, create IPC dirs, start a Meeseeks container, or collect any output.
- `internal/engine/mergequeue.go:21-24` exposes `EnqueueMerge`, but there is no non-test caller that ever submits approved output.
- A non-test codebase search only found definitions, not call sites, for:
  - `internal/ipc/spec.go:54` `WriteTaskSpec`
  - `internal/ipc/spec.go:128` `WriteReviewSpec`
  - `internal/validation/validation.go:71` `NewService`
  - `internal/review/review.go:298` `NewService`

Why this is a production blocker:

- If engine workers are enabled, tasks can move into `in_progress`, but there is still no runtime that performs the actual work.
- The architecture defines Meeseeks, validation sandboxes, reviewers, IPC, and the file router as the core execution path. Right now those pieces exist as isolated packages, not a working pipeline.

Likely root cause:

- Phase-by-phase package implementation was completed before an end-to-end executor/worker was built to compose them.

Architecture sections:

- `10 Meeseeks (Workers)`
- `12 Docker Sandbox Architecture`
- `13 Validation Sandbox`
- `14 File Router & Approval Pipeline`
- `20 Communication Model`

Implementation plan phases:

- `Phase 5: IPC, Container Lifecycle, and Sandbox Images`
- `Phase 10: Task System, Scheduler, and Locking`
- `Phase 11: Manifest Validation, Validation Sandbox, Review Pipeline`
- `Phase 12: Merge Queue and Integration Checks`

Docs affected:

- `docs/approval-pipeline.md`
- `docs/ipc-container.md`
- `docs/getting-started.md`

Potential solutions:

- Add an attempt-execution worker that:
  - builds TaskSpecs and IPC directories,
  - launches Meeseeks containers,
  - collects manifests/output,
  - runs manifest validation,
  - runs validation sandbox checks,
  - runs reviewer + orchestrator gate,
  - enqueues approved output into the merge queue.
- Add an end-to-end test that starts from a queued task and proves the full pipeline reaches merge or structured rejection.

### 4. P0: The merge queue currently commits code without real integration checks

Evidence:

- `internal/engine/mergequeue.go:80-90` uses `mergeQueueValidatorAdapter.RunIntegrationChecks`, which always returns `(true, "", nil)`.
- The code even logs that it is using a stub validator and that no real checks are running.

Why this is a production blocker:

- Once the merge path is wired, broken code can be committed even though the architecture promises project-wide validation before integration.
- This is a direct violation of the architecture's safety story for non-technical users.

Likely root cause:

- The validation package was built, but the engine adapter was never upgraded from a placeholder to the real sandbox-backed implementation.

Architecture sections:

- `13.8 Warm Sandbox Pools`
- `13.9 Integration with Approval Pipeline`
- `16.4 Serialized Merge Queue`
- `23.3 Integration Checks`

Implementation plan phases:

- `Phase 11: Manifest Validation, Validation Sandbox, Review Pipeline`
- `Phase 12: Merge Queue and Integration Checks`

Docs affected:

- `docs/approval-pipeline.md:5-8`
- `docs/getting-started.md:233-235`

Potential solutions:

- Replace the stub adapter with a real validation service that runs build/test/lint in the validation sandbox.
- Fail closed on validation runner errors instead of auto-passing.
- Add a regression test where intentionally broken staged output must not commit.

### 5. P1: Test-generation separation and convergence are not enforced end to end

Evidence:

- `internal/engine/engine.go:88-99` wires the `testgen.Service` only into scheduler model-family exclusion.
- `internal/testgen/testgen.go:41-110` implements `CreateTestTask`.
- `internal/testgen/testgen.go:256-286` implements `MarkConverged`.
- A non-test search found no runtime callers for `CreateTestTask` or `MarkConverged`.
- `docs/test-generation.md:15-16` explicitly notes that these hooks are not engine-wired.

Why this matters:

- The architecture says a feature is not done until implementation and generated tests converge.
- In the current runtime, implementation completion is not automatically followed by test-generation, convergence tracking, or fix-loop creation.

Likely root cause:

- The service exists, but lifecycle hooks were never connected to merge success and test completion events.

Architecture sections:

- `11.3 Model Family Diversification`
- `11.5 Test Authorship Separation`
- `30.1 Meeseeks Failure Escalation`

Implementation plan phases:

- `Phase 13: Test-Generation Separation and Convergence Logic`

Docs affected:

- `docs/test-generation.md`
- `docs/getting-started.md:237-250`
- `docs/approval-pipeline.md:5-8`

Potential solutions:

- On successful implementation merge, automatically call `CreateTestTask`.
- On successful test merge/validation, automatically call `MarkConverged`.
- On test failure, automatically create fix tasks through `HandleTestFailure`.
- Prevent run completion until all implementation tasks with convergence pairs reach `converged`.

### 6. P1: Git safety and work-branch isolation are defined, but not enforced

Evidence:

- `internal/gitops/gitops.go:116-126` implements dirty-tree rejection via `ValidateClean`.
- `internal/gitops/gitops.go:237-266` implements `SetupWorkBranch`.
- `internal/gitops/gitops.go:270-287` implements `CancelCleanup`.
- A non-test search found no runtime callers for those methods.
- `internal/engine/run.go:44-55` only records `WorkBranch` in state.
- Smoke tests showed:
  - `axiom run` did not switch the repo to `axiom/<slug>`.
  - `axiom run` succeeded on a dirty working tree.

Why this is a production blocker:

- A non-technical user is told Axiom uses isolated work branches and safe git hygiene, but the actual runtime works directly in the current checkout.
- Dirty-tree acceptance makes it easy to mix pre-existing user edits with Axiom-managed lifecycle state.

Likely root cause:

- Git integration was implemented as a library but not incorporated into run start/cancel lifecycle methods.

Architecture sections:

- `16.2 Base Snapshot Pinning`
- `23.1 Branch Strategy`
- `23.4 Project Completion`
- `28.2 Git Hygiene & .axiom/ Lifecycle`

Implementation plan phases:

- `Phase 4: Git Operations and Workspace Safety`
- `Phase 10: Task System, Scheduler, and Locking`
- `Phase 14: Plain CLI Command Surface`

Docs affected:

- `docs/git-operations.md`
- `docs/cli-reference.md:160-176`
- `docs/getting-started.md` (`Git Branch Strategy`)

Potential solutions:

- Call `SetupWorkBranch` during run creation.
- Refuse to create a run when the working tree is dirty unless the user explicitly opts into a recovery mode.
- Call `CancelCleanup` and container shutdown from `CancelRun`.
- Add a smoke test that asserts branch creation, branch checkout, and dirty-tree refusal.

### 7. P1: The inference, budget, prompt-safety, and prompt-logging plane is not wired into the app

Evidence:

- `internal/app/app.go:83-92` creates the engine without setting `Inference`.
- `internal/engine/engine.go:75-85` stores `opts.Inference`, but that dependency is nil in the normal app composition.
- A non-test search found no call site for `internal/inference.NewBroker`.

Why this matters:

- The architecture's core controls around provider routing, budget enforcement, local-only secret handling, and prompt logging are not part of the runtime that the user actually launches.
- Even if execution were wired later, the current composition root would still bypass the inference control plane unless this is fixed.

Likely root cause:

- Model registry and BitNet management were composed first, but the broker/provider layer was never added to the application root.

Architecture sections:

- `4 Trusted Engine vs. Untrusted Planes`
- `19.5 Inference Broker Specification`
- `21 Budget & Cost Management`
- `29.4 Secret-Aware Context Routing`
- `31 Observability & Prompt Logging`

Implementation plan phases:

- `Phase 6: Inference Broker, Provider Routing, and Cost Enforcement`
- `Phase 7: Model Registry and BitNet Operations`
- `Phase 18: Security, Secret Handling, and Prompt Safety`
- `Phase 19: Crash Recovery, Observability, and Operational Hardening`

Docs affected:

- `docs/inference-broker.md`
- `docs/security-prompt-safety.md`
- `docs/operations-diagnostics.md`

Potential solutions:

- Construct the broker in `app.Open()` using:
  - registry-derived pricing/tier maps,
  - OpenRouter provider,
  - BitNet provider,
  - prompt logger,
  - security policy.
- Inject that broker into `engine.New(...)`.
- Add a runtime health check that fails if no provider path is available for the configured orchestration mode.

### 8. P2: The TUI/session layer is mostly presentational and can mislead operators

Evidence:

- `internal/tui/model.go:357-386` shows that regular input only appends to transcript/history; it does not create runs, submit prompts, or call any orchestrator entrypoint.
- `internal/tui/model.go:397-424` has several slash commands that return canned strings rather than performing real actions (`/new`, `/resume`, `/eco`, `/diff`, `/theme`).
- `docs/getting-started.md:321-360` presents the TUI as an operator-facing execution surface with slash commands and session continuity.

Why this matters:

- For a non-technical user, the TUI looks like the obvious primary interface.
- Today it behaves more like a mock shell over state projections than a real control surface.

Likely root cause:

- The UX layer was built before the underlying orchestration actions existed, and the placeholder commands were never retired or hidden.

Architecture sections:

- `26.2.3 Session UX Manager`
- `26.2.6 Input Model & Quick Commands`
- `26.2.10 Event Model`
- `27.1 Interactive Session Commands`

Implementation plan phases:

- `Phase 15: Session UX Manager and Bubble Tea TUI`

Docs affected:

- `docs/getting-started.md`
- `docs/session-tui.md`

Potential solutions:

- Connect normal prompt submission to the same run/bootstrap entrypoint used by CLI/API.
- Replace placeholder slash commands with real actions or hide them until implemented.
- Add TUI smoke tests that prove `new run`, `SRS review`, and `pause/cancel` work end to end.

## Overall recommendation

The next milestone should not be "more packages." It should be one vertical slice that a non-technical user can actually complete:

1. `axiom init`
2. `axiom run "<prompt>"`
3. SRS draft generation
4. user approve/reject path
5. at least one task executing through container -> validation -> review -> merge
6. safe git branch handling

Until that slice works, the codebase is better described as "architecture-aligned subsystems with strong package tests" than as a production-ready local-first orchestration product.
