# Development Guide

## Repository Structure

```
axiom/
├── cmd/
│   └── axiom/              # CLI entrypoint
│       └── main.go         # Cobra command definitions
├── internal/               # Private application packages
│   ├── app/                # Composition root (wires config, state, engine)
│   ├── config/             # TOML config loading, validation, layering
│   ├── engine/             # Trusted engine runtime (Phase 3)
│   │   ├── interfaces.go   # Service interfaces (GitService, ContainerService, InferenceService, IndexService)
│   │   ├── engine.go       # Engine struct, constructor, Start/Stop lifecycle, emitEvent helper
│   │   ├── run.go          # Run lifecycle (CreateRun, PauseRun, ResumeRun, CancelRun, CompleteRun, FailRun)
│   │   ├── status.go       # Status projections (RunStatusProjection, TaskSummary, BudgetSummary)
│   │   └── worker.go       # Background worker pool (register, start, stop periodic workers)
│   ├── events/             # Central event bus (Phase 3)
│   │   ├── types.go        # EventType constants (authoritative + view-model), EngineEvent struct
│   │   └── bus.go          # Bus (Publish, Subscribe, Unsubscribe) with write serialization
│   ├── project/            # Project init, discovery, filesystem contracts
│   ├── state/              # SQLite state store — DB, migrations, domain models, repositories
│   │   ├── migrations/     # Embedded SQL migration files
│   │   ├── models.go       # Domain types, status enums, transition validators, WithTx helper
│   │   ├── projects.go     # Project CRUD
│   │   ├── runs.go         # Run CRUD + status transitions
│   │   ├── tasks.go        # Task CRUD, dependencies, locks, SRS refs, target files
│   │   ├── attempts.go     # Attempts, validation runs, review runs, artifacts
│   │   ├── sessions.go     # UI sessions, messages, summaries, input history
│   │   ├── events.go       # Events + cost log
│   │   ├── eco.go          # ECO log + status transitions
│   │   └── containers.go   # Container session tracking
│   ├── version/            # Build-time version injection
│   │
│   ├── gitops/             # Git operations manager (Phase 4)
│   │   └── gitops.go       # Manager, CommitInfo, FormatCommitMessage, branch/diff/snapshot/cleanup
│   │
│   ├── ipc/                # Filesystem IPC for container communication (Phase 5)
│   │   ├── message.go      # Message types (14 types), Envelope, typed payloads
│   │   ├── dirs.go         # Per-task directory management, volume mounts, message read/write
│   │   └── spec.go         # TaskSpec and ReviewSpec writers (Architecture Sections 10.3, 11.7)
│   │
│   ├── container/          # Docker container lifecycle management (Phase 5)
│   │   └── docker.go       # DockerService (ContainerService impl), BuildArgs, hardening flags, orphan cleanup
│   │
│   │   --- Future packages (directories scaffolded, not yet implemented) ---
│   ├── api/                # REST + WebSocket API server
│   ├── audit/              # Audit logging
│   ├── bitnet/             # Local BitNet inference integration
│   ├── budget/             # Budget enforcement and cost tracking
│   ├── cli/                # CLI command helpers
│   ├── doctor/             # System health checks
│   ├── eco/                # Engineering Change Order management
│   ├── index/              # Semantic indexer (tree-sitter)
│   ├── inference/          # Inference broker and provider routing
│   ├── manifest/           # Output manifest parsing and validation
│   ├── mergequeue/         # Serialized merge queue
│   ├── models/             # Model registry
│   ├── orchestrator/       # Orchestrator lifecycle management
│   ├── review/             # Review pipeline
│   ├── scheduler/          # Task scheduler and lock manager
│   ├── security/           # Secret scanning, prompt safety, redaction
│   ├── session/            # Session UX manager
│   ├── srs/                # SRS generation and approval workflow
│   ├── task/               # Task system and state transitions
│   ├── tui/                # Bubble Tea terminal UI
│   └── validation/         # Validation sandbox management
├── migrations/             # (Legacy location — migrations are now embedded)
├── testdata/               # Test fixture data
├── scripts/                # Build and utility scripts
├── docker/                 # Dockerfile definitions
├── docs/                   # Documentation
├── Makefile                # Build targets
├── go.mod                  # Go module definition
├── ARCHITECTURE.md         # System architecture document
└── IMPLEMENTATION_PLAN.md  # Phase-by-phase implementation plan
```

## Technology Choices

| Component | Library | Rationale |
|-----------|---------|-----------|
| CLI framework | [cobra](https://github.com/spf13/cobra) | Standard Go CLI framework, subcommand support |
| Config parsing | [go-toml/v2](https://github.com/pelletier/go-toml) | Architecture specifies TOML format |
| SQLite driver | [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) | Pure Go, no CGo — builds on all platforms without C toolchain |
| UUID generation | [google/uuid](https://github.com/google/uuid) | RFC 4122 UUIDs for run, task, and session IDs |
| Logging | `log/slog` (stdlib) | Structured logging, Go 1.21+ standard library |
| Testing | `testing` (stdlib) | Standard Go test framework |

## Build Commands

```bash
make build        # Build binary to bin/axiom
make install      # Install to $GOPATH/bin
make test         # Run all tests with verbose output
make test-short   # Run tests in short mode
make lint         # Run golangci-lint (falls back to go vet)
make clean        # Remove build artifacts
make tidy         # Run go mod tidy
make check        # tidy + lint + test (full validation)
```

### Version Injection

The Makefile injects version information via ldflags:

```bash
make build VERSION=1.0.0
# Produces: axiom 1.0.0 (abc1234) built 2026-04-05T... linux/amd64
```

Variables injected into `internal/version`:
- `Version` — semantic version or git describe output
- `GitCommit` — short git SHA
- `BuildDate` — UTC build timestamp

## Database

### SQLite Configuration

Per the architecture (Section 15.3):
- **WAL mode** — concurrent reads during writes
- **Busy timeout** — 5000ms (retries on lock contention)
- **Foreign keys** — enforced
- **Max connections** — 10

### Migration System

Migrations are embedded SQL files in `internal/state/migrations/` using Go's `embed` directive. They are applied in lexicographic order and tracked in a `schema_migrations` table.

To add a new migration:
1. Create `internal/state/migrations/NNN_description.sql`
2. Use sequential numbering (e.g., `002_add_indexes.sql`)
3. Migrations run automatically on database open

### Current Schema

The database is built through sequential migrations:
- `001_initial_schema.sql` — creates 20 tables matching Architecture Section 15.2
- `002_relax_container_session_fks.sql` — relaxes FK constraints on `container_sessions` for independent container lifecycle management (Phase 5)

All tables have corresponding repository methods in the `state` package (see [Database Schema Reference](database-schema.md) for the full repository API):

| Table | Purpose |
|-------|---------|
| `projects` | Durable project identity |
| `project_runs` | Execution runs against a project |
| `ui_sessions` | Interactive CLI/TUI sessions |
| `ui_messages` | Transcript and UI cards |
| `ui_session_summaries` | Compacted session summaries |
| `ui_input_history` | CLI input history |
| `tasks` | Task tree nodes |
| `task_srs_refs` | Task-to-SRS requirement mapping |
| `task_dependencies` | Task dependency edges |
| `task_target_files` | Declared file targets per task |
| `task_locks` | Active write-set locks |
| `task_lock_waits` | Tasks waiting for lock acquisition |
| `task_attempts` | Individual execution attempts |
| `validation_runs` | Validation check results |
| `review_runs` | Reviewer verdicts |
| `task_artifacts` | File artifact tracking with hashes |
| `container_sessions` | Active container metadata |
| `events` | Full audit trail |
| `cost_log` | Inference cost tracking |
| `eco_log` | Engineering Change Orders |

## Testing

### Running Tests

```bash
# All tests
go test ./... -v -count=1

# Specific package
go test ./internal/config/... -v

# With race detector
go test ./... -race -count=1
```

### Test Coverage

Current test coverage by package:

| Package | Tests | Coverage |
|---------|-------|----------|
| `internal/version` | 2 | Version string formatting |
| `internal/config` | 10 | Default values, validation, TOML loading, round-trip serialization, layered config |
| `internal/state` | 69 | DB lifecycle (5), projects (6), runs (8), tasks (15), attempts (10), sessions (8), events/costs (7), ECOs (5), containers (5) |
| `internal/project` | 9 | Init, duplicate detection, slugify, discover, paths, SRS write/verify |
| `internal/events` | 11 | Bus creation, SQLite persistence, subscriber fan-out, filtered subscriptions, unsubscribe, view-model event classification, concurrent safety |
| `internal/engine` | 28 | Engine lifecycle (8), run lifecycle (8), status projections (5), worker pool (5), service interface wiring (2) |
| `internal/gitops` | 38 | Branch management (8), snapshots (2), dirty/clean checks (6), commit formatting (3), add/commit (4), diffs (6), setup work branch (3), cancel cleanup (3), exit criteria (2), architecture compliance (1) |
| `internal/ipc` | 24 | Message types (6), envelope serialization (4), directory management (6), spec writers (5), message read/write (3) |
| `internal/container` | 17 | Container naming (2), hardening flags (7), start/stop lifecycle (4), list/cleanup (3), interface compliance (1) |

### Test Patterns

- Tests use `t.TempDir()` for isolated filesystem operations
- Database tests create fresh SQLite databases per test
- Engine tests use noop service implementations for testability without Docker or network
- Gitops tests create real temporary git repositories with initial commits for integration testing
- Container tests use a `mockExecutor` that records Docker commands instead of running them
- IPC tests verify filesystem operations against real temp directories
- No external service dependencies in current tests (Docker, network, inference are all mocked)

## Architecture Constraints

The following rules from ARCHITECTURE.md govern all implementation:

1. **Engine authority** — the Go engine is the sole trusted authority for all privileged operations
2. **SQLite source of truth** — all state lives in SQLite, not in-memory
3. **Untrusted agents** — all LLM agents are stateless and sandboxed
4. **Immutable SRS** — SHA-256 verified on every engine startup
5. **Network isolation** — containers have no network access (`network_mode = "none"`)
6. **No direct project mount** — containers never see the project filesystem
7. **View-model clients** — TUI and API consume engine-authored events, never read SQLite directly
8. **No remote git operations** — Axiom never pushes, pulls, or merges to/from remote repositories automatically

See [ARCHITECTURE.md](../ARCHITECTURE.md) for the complete specification.

## Implementation Status

| Phase | Name | Status |
|-------|------|--------|
| 0 | Foundation and Repo Bootstrap | Complete |
| 1 | Project Bootstrap, Config, and Filesystem Contracts | Complete |
| 2 | SQLite State Store and Core Domain Services | Complete |
| 3 | Engine Kernel and Event Infrastructure | Complete |
| 4 | Git Operations and Workspace Safety | Complete |
| 5 | IPC, Container Lifecycle, and Sandbox Images | Complete |
| 6-20 | Remaining phases | Not started |

### Phase 5 Summary

Phase 5 established the untrusted execution plane with filesystem IPC and Docker container lifecycle management:

- **IPC message protocol** (`internal/ipc/message.go`) — All 14 message types from Architecture Section 20.4 defined as typed constants. JSON envelope format (`Envelope`) wraps every IPC message with type discriminator, task ID, timestamp, and raw JSON payload. Typed payload structs for scope expansion requests/responses (Section 10.7) and inference requests (Section 19.2).

- **IPC directory management** (`internal/ipc/dirs.go`) — `TaskDirs` computes the four per-task directory paths (spec, staging, ipc/input, ipc/output) matching Section 28.1. `CreateTaskDirs` creates them idempotently. `CleanupTaskDirs` removes them. `VolumeMounts()` generates Docker mount strings with correct modes (spec=ro, staging=rw, ipc=rw per Section 12.3). `WriteMessage`/`ReadMessages` implement sequentially-named JSON file exchange for container↔engine communication.

- **Spec writers** (`internal/ipc/spec.go`) — `WriteTaskSpec` produces a Markdown spec file matching the exact format from Architecture Section 10.3, including base snapshot, objective, context, interface contract, constraints, acceptance criteria, and output format instructions directing to `/workspace/staging/` with `manifest.json`. `WriteReviewSpec` produces the reviewer evaluation template from Section 11.7 with verdict, criterion evaluation, and feedback sections.

- **Docker container service** (`internal/container/docker.go`) — `DockerService` implements the `engine.ContainerService` interface. `BuildArgs` constructs Docker run commands with all hardening flags from Section 12.6.1: `--read-only`, `--cap-drop=ALL`, `--security-opt=no-new-privileges`, `--pids-limit=256`, `--tmpfs /tmp:rw,noexec,size=256m`, `--network=none`, `--user 1000:1000`, `--cpus`, `--memory`, `--rm`. Container naming follows `axiom-<task-id>-<timestamp>-<seq>` pattern. `Start` persists a `ContainerSession` to SQLite and emits `ContainerStarted` events. `Stop` issues `docker stop` with fallback to `docker rm -f` and records the stop. `Cleanup` removes orphaned `axiom-*` containers on startup. All execution is abstracted behind a `CommandExecutor` interface for testability.

- **Schema evolution** (`migrations/002_relax_container_session_fks.sql`) — Relaxed foreign key constraints on `container_sessions` table so container lifecycle management (orphan cleanup, tracking) works independently of run/task context.

- **Nil-safety fixes** — Added nil-logger defaulting to `state.Open` and `events.New` (same pattern as `engine.New`), ensuring all components handle nil loggers gracefully.

### Phase 4 Summary

Phase 4 implemented deterministic, architecture-compliant git operations in the `internal/gitops/` package:

- **Git Manager** (`internal/gitops/gitops.go`) — `Manager` struct wrapping all git operations through `exec.Command`. The Manager satisfies the existing `engine.GitService` interface and provides additional methods for the full Phase 4 scope.

- **Branch management** — `CreateBranch`, `CreateAndCheckoutBranch`, `CheckoutBranch`, `BranchExists`, `CurrentBranch`. Work branches follow the `axiom/<project-slug>` naming convention (Architecture Section 23.1). `SetupWorkBranch` handles both new run creation (creates branch from base HEAD) and resume (checks out existing branch), with dirty-tree validation before any operation.

- **Snapshot helpers** — `CurrentHEAD` returns the full 40-character SHA. `Snapshot` is the canonical method for capturing `base_snapshot` values stored in `task_attempts` (Architecture Section 16.2).

- **Working-copy validation** — `IsDirty` detects untracked files, staged changes, and modified tracked files. `ValidateClean` returns an actionable error if the working tree is not clean, enforcing the architecture requirement that the engine refuses to start on a dirty tree (Section 28.2).

- **Commit formatting** — `FormatCommitMessage` builds the exact commit message template from Architecture Section 23.2 with all required metadata fields (task title, task ID, SRS refs, Meeseeks model, reviewer model, attempt number, cost, base snapshot). `CommitTask` stages files and commits in one operation.

- **Diff helpers** — `Diff` (between two refs), `DiffStaged` (staged changes), `DiffWorkBranch` (three-dot diff for work branch review). These support task output review, merge previews, and final branch review (Architecture Section 23.4).

- **Cancel cleanup** — `CancelCleanup` reverts all uncommitted engine-applied changes (`git reset --hard HEAD` + `git clean -fd`) and switches back to the base branch. Committed work on the work branch is preserved for user review.

- **No remote operations** — The Manager has zero push, pull, fetch, or remote-related methods, enforcing Architecture Section 23.4: "Axiom SHALL NOT automatically merge or push to remote repositories."

See [Git Operations Reference](git-operations.md) for the full API.

### Phase 3 Summary

Phase 3 built the trusted control plane that all command surfaces use:

- **Event bus** (`internal/events/`) — Central event emitter with two categories of events:
  - **Authoritative events** (20+ types: `run_created`, `task_started`, etc.) are persisted to the SQLite `events` table as the audit trail (Architecture Section 22.4).
  - **View-model events** (8 types: `startup_summary`, `session_mode_changed`, `task_projection_updated`, etc.) are fanned out to in-memory subscribers but NOT persisted (Architecture Section 26.2.10).
  - Subscriber fan-out supports optional filters, buffered channels, and concurrent-safe operation.
  - SQLite writes are serialized via a dedicated write mutex to avoid SQLITE_BUSY under concurrent publishes.

- **Service interfaces** (`internal/engine/interfaces.go`) — Abstractions for `GitService`, `ContainerService`, `InferenceService`, and `IndexService` so orchestration logic is testable without real Docker or network calls. Tests use noop implementations.

- **Engine runtime** (`internal/engine/engine.go`) — Long-lived `Engine` struct that wires config, database, event bus, and service interfaces. Provides `Start()`/`Stop()` lifecycle, background worker pool, and accessor methods (`Bus()`, `DB()`, `Config()`, `RootDir()`). The `emitEvent()` helper logs errors from event persistence without blocking the calling operation.

- **Run lifecycle** (`internal/engine/run.go`) — Six methods enforcing the run state machine:
  - `CreateRun` — creates a run in `draft_srs` status with config snapshot, work branch derivation, and default budget from config
  - `PauseRun`, `ResumeRun`, `CancelRun`, `CompleteRun`, `FailRun` — each validates the state transition (delegating to `state.UpdateRunStatus`) and emits the corresponding event

- **Status projections** (`internal/engine/status.go`) — `GetRunStatus(projectID)` returns a `RunStatusProjection` containing:
  - Project identity (name, slug, root dir)
  - Active run (if any), including current status and branch
  - `TaskSummary` — counts by status (queued, in_progress, done, failed, blocked, waiting_lock, cancelled_eco)
  - `BudgetSummary` — max/spent/remaining with warning threshold from config

- **Worker pool** (`internal/engine/worker.go`) — `WorkerPool` manages periodic background goroutines. Workers are registered with a name, function, and interval. The pool supports graceful shutdown via context cancellation. Future phases will register scheduler, merge queue, and cleanup workers.

- **App integration** (`internal/app/app.go`) — Updated to create the engine on `Open()` and stop it on `Close()`.

- **CLI update** (`cmd/axiom/main.go`) — `axiom status` now uses `engine.GetRunStatus()` for rich output including run state, task summary, and budget with warnings.

### Phase 2 Summary

Phase 2 added the full domain service layer to the `state` package:

- **Domain models** — 21 typed structs matching every table in the schema, plus typed status enums with `Valid*Transition()` functions
- **Repository methods** — CRUD operations for all entities (projects, runs, tasks, attempts, validation/review runs, artifacts, sessions, events, costs, ECOs, containers)
- **Transactional helpers** — `WithTx` for atomic read-then-write patterns; used by all status transition methods
- **Invariant enforcement** — status transitions are validated before SQL execution; invalid transitions return `ErrInvalidTransition`
- **Lock management** — `AcquireLock` is transactional with `ErrLockConflict` detection; `ReleaseTaskLocks` for batch cleanup
- **69 tests** covering all CRUD operations, valid/invalid transitions, lock conflicts, timestamp handling, and referential integrity

See [Database Schema Reference](database-schema.md) for the complete repository API.
