# Axiom Implementation Plan

**Source of truth:** [ARCHITECTURE.md](C:/Users/ethan/Projects/axiom_new/ARCHITECTURE.md)
**Plan intent:** Take Axiom from an empty repo to a production-ready initial release.
**Current scope:** Includes the full engine, CLI, TUI, API, Docker isolation, inference routing, indexing, review pipeline, and security model.
**Explicitly excluded from this plan:** GUI dashboard implementation from Section 26.1 of the architecture. The engine event and view-model layers required by a future GUI are included.

---

## 1. Delivery Goal

Build Axiom as a local-first AI software orchestration system whose trusted Go engine manages:

- project initialization and configuration
- immutable SRS approval and ECO handling
- task decomposition persistence and execution scheduling
- disposable worker, reviewer, and validator containers
- inference brokering across OpenRouter and local BitNet
- semantic indexing and minimum-context TaskSpec construction
- manifest validation, sandbox validation, review, and merge queue approval
- resumable CLI/TUI operator workflows
- external orchestrator control through REST and WebSocket APIs
- audit logging, cost tracking, crash recovery, and security enforcement

The first release must support a full project lifecycle through the CLI/TUI and plain CLI commands without relying on any GUI components.

---

## 2. Non-Negotiable Architecture Constraints

These rules govern implementation order and code structure.

1. The Go engine is the only trusted authority for filesystem writes, git operations, Docker lifecycle, SQLite writes, budget enforcement, and model access.
2. SQLite is the source of truth for run state, task state, attempts, locks, events, sessions, and costs.
3. All agents are stateless and untrusted. No direct project mount, no direct network access, no direct provider credentials, no direct git or Docker access.
4. The SRS becomes immutable after approval. Post-approval changes are limited to ECOs and must remain environmental rather than scope-changing.
5. No Meeseeks output reaches the repo without manifest validation, hermetic validation, reviewer approval, orchestrator approval, and merge queue checks.
6. Tests are authored by separate downstream tasks from a different model family than the implementation task.
7. The TUI and API are clients of engine-authored view models and events. They do not read SQLite directly.
8. `.axiom/` runtime state is excluded from semantic indexing and prompt packaging.
9. Secret-bearing context stays local unless the user explicitly overrides that policy.
10. The initial release should favor correctness, traceability, and recovery over throughput or optimization.

---

## 3. Release Strategy

The implementation should be delivered in five major gates:

1. **Gate A: Single-task vertical slice**
   Build `axiom init`, config/state/bootstrap, one task execution, manifest validation, validator, reviewer, merge, and status reporting.
2. **Gate B: Full orchestration loop**
   Add task trees, dependencies, locks, retries, escalations, test-separation, semantic indexing, and merge queue requeue behavior.
3. **Gate C: Operator surface**
   Add the full CLI command set, Session UX Manager, Bubble Tea TUI, transcript persistence, and plain-text fallback.
4. **Gate D: External orchestration**
   Add API server, auth tokens, lifecycle endpoints, event/control WebSockets, tunnel integration, and skill generation.
5. **Gate E: Hardening and release**
   Add crash recovery, doctor, prompt safety, local-secret routing, dependency-cache workflows, robust test suites, and packaging.

Warm validation pools, orchestrator-granted lateral channels, and context invalidation warnings should be implemented behind feature flags after the core path is stable.

---

## 4. Proposed Initial Repository Layout

This structure should be created early and preserved unless implementation pressure reveals a cleaner boundary.

```text
cmd/
  axiom/

internal/
  api/
  app/
  audit/
  bitnet/
  budget/
  cli/
  config/
  container/
  doctor/
  eco/
  events/
  gitops/
  index/
  inference/
  ipc/
  manifest/
  mergequeue/
  models/
  orchestrator/
  project/
  review/
  scheduler/
  security/
  session/
  srs/
  state/
  task/
  tui/
  validation/
  version/

migrations/
testdata/
scripts/
docker/
docs/
```

Package boundaries should follow engine responsibilities rather than UI or runtime preferences.

---

## 5. Phase-by-Phase Execution Plan

## Phase 0: Foundation and Repo Bootstrap

**Objective:** Create a clean Go application skeleton with testable boundaries and repeatable local development.

**Implementation work**

- Initialize the Go module and dependency baseline.
- Choose core libraries for CLI parsing, config decoding, SQLite access, logging, and testing.
- Create `cmd/axiom` entrypoint plus `internal/app` composition root.
- Add structured logging with human-readable local output and machine-readable internal fields.
- Add migration framework and `migrations/` layout.
- Create build scripts or `Makefile` targets for build, test, lint, and Docker image tasks.
- Add baseline CI-oriented commands even if CI config itself is deferred.
- Add version injection and `axiom version`.

**Deliverables**

- Buildable `axiom` binary.
- Empty but wired application container.
- Repeatable local dev/test commands.

**Exit criteria**

- `go test ./...` passes on the scaffold.
- `axiom version` works.
- Migrations can be applied to a fresh SQLite database.

## Phase 1: Project Bootstrap, Config, and Filesystem Contracts

**Objective:** Implement the local project contract that every later subsystem depends on.

**Implementation work**

- Implement `axiom init`.
- Generate `.axiom/config.toml` using the architecture defaults.
- Generate `.gitignore` entries for ephemeral `.axiom/` paths.
- Ensure committed `.axiom/` files and gitignored runtime files match the architecture.
- Implement project discovery from cwd upward.
- Add config layering for project config and global config under `~/.axiom/`.
- Implement dirty-worktree checks before `axiom run`.
- Implement branch naming helpers using `axiom/<slug>`.
- Implement immutable SRS file helpers and hash file utilities.

**Deliverables**

- Project initialization flow.
- Config loader and validator.
- Filesystem helpers for `.axiom/` state.

**Exit criteria**

- A new repo can be initialized and re-opened cleanly.
- Invalid configs fail with actionable errors.
- Dirty repo detection blocks execution.

## Phase 2: SQLite State Store and Core Domain Services

**Objective:** Stand up the authoritative state model from Section 15.

**Implementation work**

- Implement all required tables from the architecture schema.
- Add migration versioning and integrity checks.
- Create repositories or service-layer adapters for:
  - projects
  - project runs
  - tasks and dependencies
  - task target files and locks
  - task attempts
  - validation and review runs
  - artifacts
  - sessions and transcript state
  - events and costs
  - ECO log
- Configure SQLite in WAL mode with sane busy timeout and pooled connections.
- Add typed domain models and transactional helpers for state transitions.
- Define invariant checks for task status changes and run lifecycle changes.

**Deliverables**

- Durable state layer.
- Migration-backed SQLite schema.
- Domain services for state mutation.

**Exit criteria**

- Fresh DB creation works.
- Reopening existing DB works.
- Transactional tests cover key state transitions and invariant enforcement.

## Phase 3: Engine Kernel and Event Infrastructure

**Objective:** Build the trusted control plane that all command surfaces use.

**Implementation work**

- Create the engine runtime that wires config, state, git, Docker, inference, indexing, sessions, and API services.
- Implement a central event emitter that writes authoritative events to SQLite and fans out view-model updates to subscribers.
- Add background worker loop infrastructure for scheduler, merge queue, cleanup, and API/TUI event streaming.
- Define internal service interfaces so orchestration logic remains testable without real Docker or network calls.
- Implement run-level lifecycle orchestration: create run, pause, resume, cancel, complete, error.
- Add top-level status projections used by `axiom status`, the TUI, and the future GUI.

**Deliverables**

- A long-lived engine runtime.
- Event bus and projections.
- Reusable service composition.

**Exit criteria**

- Commands can create and inspect run state through one engine path.
- Event emission is observable in tests and persisted to the database.

## Phase 4: Git Operations and Workspace Safety

**Objective:** Make git behavior deterministic and architecture-compliant.

**Implementation work**

- Capture the base branch and create the work branch.
- Implement snapshot helpers for current HEAD and task `base_snapshot`.
- Implement working-copy validation for safe execution.
- Add commit writer using the architecture’s commit message template.
- Add diff helpers for task output, merge previews, and final branch review.
- Implement cancellation cleanup semantics, including reverting uncommitted engine-applied changes.
- Ensure Axiom never pushes or merges to remote automatically.

**Deliverables**

- Git manager with branch, snapshot, diff, and commit operations.

**Exit criteria**

- Work branch creation is deterministic.
- Commits follow the architecture format.
- Cancel behavior is tested on a temp repo.

## Phase 5: IPC, Container Lifecycle, and Sandbox Images

**Objective:** Establish the untrusted execution plane safely.

**Implementation work**

- Implement filesystem IPC directories and JSON message envelopes.
- Implement spec writers for TaskSpecs and ReviewSpecs.
- Implement staging and artifact directory management.
- Implement Docker run wrappers with required hardening flags.
- Add orphan cleanup on startup.
- Track active container sessions in SQLite.
- Build or document language-specific worker images and default multi-language image.
- Add timeouts, CPU, and memory limits from config.
- Ensure project source is never mounted into Meeseeks or reviewer containers.

**Deliverables**

- Reusable container supervisor.
- IPC transport and spec/staging directories.

**Exit criteria**

- Test containers can be spawned, communicate over IPC, and be destroyed.
- Container metadata is persisted.
- Startup cleans up orphaned `axiom-*` containers.

## Phase 6: Inference Broker, Provider Routing, and Cost Enforcement

**Objective:** Centralize all model access behind engine policy.

**Implementation work**

- Define provider abstraction for OpenRouter and BitNet.
- Implement broker request validation:
  - model allowlist
  - budget pre-authorization
  - per-task rate limits
  - token caps
- Implement request/response logging into `cost_log` and related attempt state.
- Add streaming support through chunked IPC output files.
- Store provider credentials only in trusted config.
- Emit provider availability events.
- Implement fallback behavior for BitNet-eligible work when external provider is unavailable.

**Deliverables**

- Inference broker service.
- Provider adapters.
- Cost accounting path.

**Exit criteria**

- Mocked inference can be brokered end to end.
- Budget rejection occurs before a provider call is made.
- Cost and token logs are persisted.

## Phase 7: Model Registry and BitNet Operations

**Objective:** Support model-aware scheduling and zero-cost local trivial work.

**Implementation work**

- Implement model registry tables or persistence strategy backed by SQLite.
- Load and merge:
  - OpenRouter model metadata
  - shipped `models.json` capability index
  - local BitNet model inventory
- Implement `axiom models refresh`, `list`, and `info`.
- Implement BitNet service control commands:
  - start
  - stop
  - status
  - models
- Add first-run weight download flow with confirmation prompt.
- Support grammar-constrained decoding in broker requests for local-tier tasks.

**Deliverables**

- Usable model catalog.
- Operational local inference management.

**Exit criteria**

- Registry refresh works online and falls back offline.
- BitNet commands work against a real or stubbed local server.

## Phase 8: Semantic Indexer and Typed Query API

**Objective:** Enable minimum-necessary structured context and lock-scope reasoning.

**Implementation work**

- Integrate tree-sitter parsers for Go, TypeScript/JavaScript, Python, and Rust.
- Design SQLite-backed symbol/index tables.
- Implement full indexing on init and incremental indexing after commits.
- Exclude `.axiom/` and other internal paths from indexing.
- Implement typed query API:
  - `lookup_symbol`
  - `reverse_dependencies`
  - `list_exports`
  - `find_implementations`
  - `module_graph`
- Build helpers that support context packaging and lock-scope escalation decisions.

**Deliverables**

- Semantic index service.
- Typed query interface for orchestrators and CLI commands.

**Exit criteria**

- Queries return structured results on fixture repos.
- Incremental reindex works after file changes.

## Phase 9: SRS, ECO, and Bootstrap-Mode Workflow

**Objective:** Implement the scope-locking contract before autonomous execution begins.

**Implementation work**

- Implement `axiom run "<prompt>"` up through SRS generation and approval gating.
- Add bootstrap-mode context rules for greenfield versus existing projects.
- Persist pending SRS drafts and approval state.
- On approval:
  - write `.axiom/srs.md`
  - set read-only permissions
  - write `.axiom/srs.md.sha256`
  - store hash in SQLite
- On rejection, persist feedback and reopen the revision loop.
- Implement ECO validation with allowed categories only.
- Record ECOs as append-only markdown addenda under `.axiom/eco/`.
- Add ECO approval/rejection flow and replacement-task hooks.

**Deliverables**

- Full SRS approval state machine.
- ECO lifecycle.

**Exit criteria**

- Approved SRS cannot be modified by normal engine flow.
- ECOs preserve the original SRS and remain traceable.

## Phase 10: Task System, Scheduler, and Locking

**Objective:** Move from approved spec to safe concurrent execution.

**Implementation work**

- Implement task creation APIs and batch creation.
- Add dependency validation and cycle detection.
- Persist `task_srs_refs` and target-file lock metadata.
- Implement scheduler loop:
  - find dependency-free queued tasks
  - acquire lock set atomically
  - move task to `in_progress`
  - create attempt record
- Implement `waiting_on_lock` behavior and requeue on release.
- Implement retries and escalations using fresh containers every time.
- Implement task blocking after exhaustion.
- Implement scope-expansion request flow and lock-wait handling for expansion conflicts.
- Track attempt phases precisely for recovery.

**Deliverables**

- Execution scheduler.
- Lock manager.
- Retry and escalation engine.

**Exit criteria**

- Independent tasks can run concurrently.
- Conflicting tasks are held back safely.
- Retry/escalation behavior matches the architecture.

## Phase 11: Manifest Validation, Validation Sandbox, Review Pipeline

**Objective:** Enforce the approval pipeline that protects the repo from bad output.

**Implementation work**

- Implement manifest parser and validator.
- Enforce canonical path checks, symlink rejection, file-size limits, manifest completeness, and scope enforcement.
- Implement artifact hash tracking for add, modify, delete, and rename operations.
- Implement hermetic validation sandboxes:
  - read-only base snapshot
  - writable overlay with staged diff
  - no network
  - no secrets
  - dependency install from prepared caches only
- Implement language-specific validation profiles for Go, Node, Python, and Rust.
- Implement reviewer spawning with model-family diversification for standard and premium tiers.
- Implement risky-file escalation rules.
- Implement orchestrator final gate after reviewer approval.

**Deliverables**

- Full approval pipeline from staged output to approved artifact.

**Exit criteria**

- Invalid manifests are rejected before validation.
- Broken code fails in the validation sandbox, not on the host.
- Reviewer rejection loops back into a fresh attempt.

## Phase 12: Merge Queue and Integration Checks

**Objective:** Serialize commits and validate against the real current HEAD.

**Implementation work**

- Implement merge queue worker.
- Compare task `base_snapshot` to current HEAD.
- Attempt clean application of staged output to latest HEAD.
- Run project-wide build, test, and lint in a validation sandbox against the merged state.
- On failure:
  - reject commit
  - requeue task
  - create updated feedback payload
- On success:
  - write files to repo
  - commit
  - reindex changed files
  - release locks
  - mark task done
  - unblock dependents

**Deliverables**

- Serialized merge path with integration safety net.

**Exit criteria**

- Only one merge occurs at a time.
- Stale or conflicting work is requeued rather than forced through.

## Phase 13: Test-Generation Separation and Convergence Logic

**Objective:** Enforce architecture-mandated independence between implementation and test authorship.

**Implementation work**

- Ensure implementation tasks and test tasks are separate task types.
- Enforce different model-family selection for generated tests.
- Only create test-generation tasks after implementation merge succeeds.
- Feed committed implementation plus semantic index context into test-generation tasks.
- When generated tests fail, spawn implementation-fix tasks with:
  - committed code
  - failing tests
  - failure output
- Require convergence before a feature is treated as done.

**Deliverables**

- Feature completion model that includes generated tests.

**Exit criteria**

- The same model family cannot author both implementation and generated tests for the same feature.
- Post-test fix loops are traceable and recoverable.

## Phase 14: Plain CLI Command Surface

**Objective:** Make the engine operable without the full-screen TUI.

**Implementation work**

- Implement:
  - `axiom`
  - `axiom tui`
  - `axiom tui --plain`
  - `axiom init`
  - `axiom run`
  - `axiom status`
  - `axiom pause`
  - `axiom resume`
  - `axiom cancel`
  - `axiom export`
  - session commands
  - model commands
  - bitnet commands
  - api commands
  - tunnel commands
  - skill commands
  - index commands
  - `axiom doctor`
- Make plain CLI output rely on the same engine projections as TUI/API.
- Ensure approval prompts and run-state changes work without full-screen mode.

**Deliverables**

- Complete command-line operability in non-TTY and TTY contexts.

**Exit criteria**

- Every command listed in Section 27 exists and has integration coverage.

## Phase 15: Session UX Manager and Bubble Tea TUI

**Objective:** Build the primary operator experience without violating engine authority.

**Implementation work**

- Implement Session UX Manager in the engine:
  - session create/resume
  - mode transitions
  - startup summary generation
  - transcript storage
  - compaction and export
  - prompt suggestions
- Build Bubble Tea TUI with:
  - top status bar
  - transcript viewport
  - task rail
  - footer composer
  - overlay surfaces
- Implement slash commands, shell mode, input history, and file mentions.
- Add approval cards for SRS and ECO decisions.
- Add diff preview overlays and task inspection views.
- Add plain-text fallback parity for non-interactive mode.

**Deliverables**

- Resumable full-screen TUI.
- Persisted terminal sessions.

**Exit criteria**

- `axiom` launches into a deterministic startup frame immediately.
- Sessions survive engine restart and can be resumed.
- Approval and status actions are operable from the TUI.

## Phase 16: API Server, WebSockets, and Tunnel Support

**Objective:** Support Claw and other external orchestrators without compromising local authority.

**Implementation work**

- Implement API server startup and shutdown.
- Add token generation, listing, revocation, scopes, and expiration.
- Implement lifecycle and read endpoints from Section 24.2.
- Implement event WebSocket for project updates.
- Implement authenticated control WebSocket with typed action envelopes and idempotency keys.
- Add rate limiting and optional IP allowlists.
- Add audit logging for all API requests and failed auth attempts.
- Implement tunnel start/stop integration.

**Deliverables**

- External orchestration surface with read and control channels.

**Exit criteria**

- External client can create a project, submit a run, receive events, and send control actions safely.

## Phase 17: Runtime Skill Generation

**Objective:** Generate runtime-specific instruction artifacts that teach supported orchestrators how to use Axiom.

**Implementation work**

- Implement `axiom skill generate --runtime <...>`.
- Generate runtime-specific artifacts for:
  - claw
  - claude-code
  - codex
  - opencode
- Ensure generated content includes workflow, trust boundaries, request types, TaskSpec rules, review rules, budget rules, ECO flow, and test-separation rules.
- Regenerate on relevant config changes.

**Deliverables**

- Skill generation system aligned with supported runtimes.

**Exit criteria**

- Each runtime produces the expected artifact and content.

## Phase 18: Security, Secret Handling, and Prompt Safety

**Objective:** Make the engine safe by default against the main threat classes in Section 29.

**Implementation work**

- Implement file sensitivity classification by pattern.
- Implement regex-based secret scanning before prompt packaging.
- Implement redaction and exclusion policy.
- Route secret-bearing tasks to local-only inference unless explicitly overridden.
- Separate security-critical routing from secret-bearing routing.
- Wrap repo content in prompt-safe delimiters and provenance labels.
- Implement exclusion lists for `.axiom/`, `.env*`, logs, and generated internal state.
- Add comment sanitization or flagging for instruction-like content.
- Ensure prompt logging reuses redacted content only.

**Deliverables**

- Secret-aware context packaging.
- Prompt-injection defense layer.

**Exit criteria**

- Tests prove secrets do not leak into external prompt payloads by default.
- Prompt packaging clearly separates instructions from untrusted repo data.

## Phase 19: Crash Recovery, Observability, and Operational Hardening

**Objective:** Make the system restartable and diagnosable under failure.

**Implementation work**

- Implement startup recovery:
  - orphan container cleanup
  - stale in-progress attempt reset
  - stale lock release
  - lock-wait rebuild
  - staging cleanup
  - SRS hash verification
- Implement prompt logging controls and storage.
- Add engine logs and useful diagnostic event types.
- Implement `axiom doctor`:
  - Docker availability
  - BitNet availability
  - network/provider reachability
  - resource checks
  - cache readiness
  - security regex validity
- Add operator-facing warnings for budget pressure and system resource saturation.

**Deliverables**

- Reliable restart behavior.
- Diagnostics and observability baseline.

**Exit criteria**

- Simulated crash/restart resumes cleanly from persisted state.
- `axiom doctor` catches common local setup failures.

## Phase 20: Stabilization, Test Matrix, and Release Packaging

**Objective:** Turn the implementation into a releasable product.

**Implementation work**

- Build fixture repos for greenfield and existing-project scenarios.
- Add unit tests for all domain and policy-heavy packages.
- Add integration tests for:
  - init/run/status flows
  - approval pipeline
  - merge queue
  - API auth
  - recovery
  - security routing
- Add end-to-end tests against real Docker and fixture projects.
- Add failure-injection tests for:
  - provider outage
  - Docker outage
  - lock contention
  - budget exhaustion
  - stale snapshot merge failure
  - reviewer rejection
  - dependency cache miss
- Package the binary, default config docs, images, and operator documentation.

**Deliverables**

- Release candidate build and test matrix.

**Exit criteria**

- The first release is operable on a clean machine with documented setup.
- Critical flows are covered by automated tests and manual release checks.

---

## 6. Recommended Build Order Inside the Codebase

This is the implementation order I should follow while coding.

1. `config`, `version`, `state`, `project`
2. `events`, `app`, `gitops`
3. `ipc`, `container`, `manifest`
4. `budget`, `inference`, `models`, `bitnet`
5. `index`
6. `srs`, `eco`
7. `task`, `scheduler`
8. `validation`, `review`, `mergequeue`
9. `cli` plain commands
10. `session`, `tui`
11. `api`
12. `security`, `doctor`, `observability`
13. `skills`
14. stabilization and release tooling

This order keeps the trusted engine and approval path ahead of operator surfaces.

---

## 7. Features to Defer Behind Flags

These should not block the first reliable release.

- validation warm pool
- integration sandbox
- context invalidation warnings
- orchestrator-granted lateral Meeseeks channels
- advanced LLM-generated prompt suggestions in the TUI

Each deferred feature should still get clean extension points in the engine design so we do not need architectural rewrites later.

---

## 8. Parallelization Opportunities

Parallel work is safe only after the underlying dependencies exist.

**After Phase 3**

- plain CLI formatting
- config validation polish
- migration and repository tests

**After Phase 6**

- model registry work
- BitNet command surface
- provider integration tests

**After Phase 8**

- SRS/ECO command flow
- task decomposition persistence
- index query commands

**After Phase 12**

- TUI implementation
- API implementation
- skill generation

The core approval pipeline, lock manager, merge queue, and recovery logic should stay on the critical path and be implemented serially to reduce architectural drift.

---

## 9. Definition of Done for the First Release

The first release is done when all of the following are true:

1. A user can run `axiom init`, configure the project, and start a run from the CLI.
2. Axiom can generate or accept an SRS, gate approval, lock scope, and persist the immutable SRS.
3. The engine can execute a task through Meeseeks, validation sandbox, reviewer, orchestrator gate, and merge queue.
4. The engine can schedule multiple independent tasks safely with lock enforcement, retries, escalations, and requeues.
5. Test-generation separation is enforced and post-test fix loops function correctly.
6. The full command surface in Section 27 exists in working form.
7. The TUI supports session resume, approvals, task monitoring, diffs, and plain-text fallback.
8. The API server supports authenticated external orchestration via REST and WebSocket.
9. Secret-bearing context is protected by default and prompt packaging follows the security model.
10. Crash recovery restores a run safely after engine interruption.
11. The system is covered by automated tests and validated against fixture repos.

---

## 10. Immediate Next Step

Begin with **Phase 0 and Phase 1 together**:

- scaffold the Go application
- create the migration/config foundation
- implement `axiom init`
- establish `.axiom/` project contracts
- lock down git hygiene and config validation early

That gives the implementation a stable base before any orchestration logic, container work, or TUI development begins.
