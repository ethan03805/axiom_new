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
│   ├── engine/             # Trusted engine runtime (Phase 3+)
│   │   ├── interfaces.go   # Service interfaces (GitService, ContainerService, InferenceService, IndexService, ModelService)
│   │   ├── engine.go       # Engine struct, constructor, Start/Stop lifecycle, emitEvent helper
│   │   ├── run.go          # Run lifecycle (CreateRun, PauseRun, ResumeRun, CancelRun, CompleteRun, FailRun)
│   │   ├── srs.go          # SRS lifecycle (SubmitSRS, ApproveSRS, RejectSRS) (Phase 9)
│   │   ├── eco.go          # ECO lifecycle (ProposeECO, ApproveECO, RejectECO) (Phase 9)
│   │   ├── scheduler.go    # Scheduler worker loop + engine adapters (engineModelSelector, engineSnapshotProvider) (Phase 10)
│   │   ├── mergequeue.go   # Merge queue worker loop + engine adapters (git, validator, indexer, lock, task, event) (Phase 12)
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
│   │   ├── containers.go   # Container session tracking
│   │   ├── model_registry.go # Model registry CRUD (Phase 7)
│   │   ├── index.go        # Semantic index CRUD (Phase 8)
│   │   └── convergence.go  # Convergence pair CRUD (Phase 13)
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
│   ├── inference/          # Inference broker, provider routing, cost enforcement (Phase 6)
│   │   ├── provider.go     # Provider interface, types, sentinel errors
│   │   ├── openrouter.go   # OpenRouter API client (OpenAI-compatible chat completions)
│   │   ├── bitnet_provider.go # BitNet local inference client (GBNF grammar support)
│   │   ├── broker.go       # Central broker: validate, route, execute, log, emit events
│   │   ├── budget.go       # Budget pre-authorization (goroutine-safe)
│   │   └── ratelimit.go    # Per-task rate limiting
│   │
│   ├── models/             # Model registry service (Phase 7)
│   │   ├── models.json     # Shipped capability index (31 models, embedded)
│   │   ├── shipped.go      # Embedded models.json loader
│   │   ├── openrouter.go   # OpenRouter /api/v1/models fetcher + pricing classification
│   │   ├── bitnet_models.go # BitNet /v1/models fetcher + Falcon model normalization
│   │   ├── registry.go     # Registry service: refresh, list, get, broker map extraction
│   │   └── engine_adapter.go # RegistryAdapter → engine.ModelService bridge
│   │
│   ├── bitnet/             # Local BitNet server lifecycle (Phase 7)
│   │   └── service.go      # Service: Status, ListModels, Start, Stop, Enabled, WeightDir
│   │
│   ├── index/              # Semantic indexer and typed query API (Phase 8)
│   │   ├── types.go        # Domain types (SymbolResult, ModuleGraphResult), exclusion lists, language mapping
│   │   ├── parser.go       # Parser interface and language registry
│   │   ├── parser_go.go    # Go parser using go/parser + go/ast (stdlib)
│   │   ├── parser_ts.go    # TypeScript regex-based parser
│   │   ├── parser_js.go    # JavaScript parser (reuses TypeScript)
│   │   ├── parser_python.go # Python regex-based parser
│   │   ├── parser_rust.go  # Rust regex-based parser
│   │   ├── indexer.go      # Indexer service: full/incremental indexing, impl detection, package graph
│   │   ├── query.go        # Typed query API: lookup_symbol, reverse_dependencies, list_exports, find_implementations, module_graph
│   │   └── engine_adapter.go # IndexerAdapter → engine.IndexService bridge
│   │
│   ├── srs/                # SRS validation, bootstrap context, draft persistence (Phase 9)
│   │   └── srs.go          # ValidateStructure, BuildBootstrapContext, WriteDraft/ReadDraft/DeleteDraft, ComputeHash
│   │
│   ├── eco/                # ECO validation, category enforcement, file persistence (Phase 9)
│   │   └── eco.go          # ValidCategory, ValidateProposal, WriteECOFile, ListECOFiles, formatECOMarkdown
│   │
│   ├── task/               # Task service: creation, batch, cycle detection, retry/escalation/blocking (Phase 10)
│   │   └── service.go      # Service, CreateTask, CreateBatch, HandleTaskFailure, RetryTask, EscalateTask, BlockTask, RequestScopeExpansion
│   │
│   ├── scheduler/          # Execution scheduler: dispatch loop, lock acquisition, waiter processing (Phase 10)
│   │   └── scheduler.go    # Scheduler, Tick, ReleaseLocks, ModelSelector/SnapshotProvider interfaces, sortLockRequests
│   │
│   ├── manifest/           # Output manifest parsing and validation (Phase 11)
│   │   └── manifest.go     # ParseManifest, ValidateManifest, ComputeArtifacts, path/scope/size checks
│   │
│   ├── validation/         # Validation sandbox orchestration (Phase 11)
│   │   └── validation.go   # Service, BuildSandboxSpec, DetectLanguages, GetProfile, CheckResult aggregation
│   │
│   ├── review/             # Review pipeline (Phase 11)
│   │   └── review.go       # Service, IsRiskyFile, ReviewerTier, SelectReviewerModel, ParseVerdict, OrchestratorGate
│   │
│   ├── mergequeue/         # Serialized merge queue (Phase 12)
│   │   └── mergequeue.go   # Queue, MergeItem, Tick, Enqueue, conflict detection, file apply/revert, commit
│   │
│   ├── testgen/            # Test-generation separation and convergence logic (Phase 13)
│   │   └── testgen.go      # Service, CreateTestTask, GetExcludeFamily, HandleTestFailure, CheckConvergence, MarkConverged, MarkBlocked, IsFeatureDone
│   │
│   ├── cli/                # Plain CLI command surface (Phase 14) + session/TUI commands (Phase 15)
│   │   ├── commands.go     # Commands() registry — returns all CLI commands
│   │   ├── helpers.go      # Shared helpers (findProjectID, findActiveRun, openApp)
│   │   ├── run.go          # axiom run, pause, resume, cancel
│   │   ├── export.go       # axiom export (JSON project state)
│   │   ├── models.go       # axiom models refresh/list/info
│   │   ├── bitnet.go       # axiom bitnet start/stop/status/models
│   │   ├── index.go        # axiom index refresh/query
│   │   ├── session.go      # axiom session list/resume/export, axiom tui (Phase 15)
│   │   └── stubs.go        # Stub commands for api, tunnel, skill, doctor
│   │
│   ├── session/            # Session UX Manager (Phase 15)
│   │   └── manager.go      # Manager: create/resume sessions, mode transitions, startup summary, transcript, compaction, export, suggestions
│   │
│   ├── tui/                # Bubble Tea terminal UI (Phase 15)
│   │   ├── model.go        # Main Bubble Tea model: Update/View, event handling, slash commands
│   │   ├── styles.go       # Lip Gloss theme definitions
│   │   ├── plain.go        # Plain-text fallback renderer for non-TTY environments
│   │   └── program.go      # Bubble Tea program wrapper with alt-screen and mouse support
│   │
│   │   --- Future packages (directories scaffolded, not yet implemented) ---
│   ├── api/                # REST + WebSocket API server
│   ├── audit/              # Audit logging
│   ├── budget/             # (Budget logic is in inference/budget.go)
│   ├── doctor/             # System health checks
│   ├── orchestrator/       # Orchestrator lifecycle management
│   ├── security/           # Secret scanning, prompt safety, redaction
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
| TUI framework | [Bubble Tea](https://github.com/charmbracelet/bubbletea) | Event-driven terminal UI framework |
| TUI components | [Bubbles](https://github.com/charmbracelet/bubbles) | Reusable terminal widgets (textarea, viewport) |
| TUI styling | [Lip Gloss](https://github.com/charmbracelet/lipgloss) | Declarative terminal styling and layout |
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
- `003_model_registry.sql` — adds `model_registry` table for model catalog with tier, family, and source indexes (Phase 7)
- `004_semantic_index.sql` — adds 6 semantic index tables (`index_files`, `index_symbols`, `index_imports`, `index_references`, `index_packages`, `index_package_deps`) with 11 performance indexes (Phase 8)
- `005_attempt_tier.sql` — adds `tier` column to `task_attempts` for per-tier retry counting (Phase 10)
- `006_convergence_pairs.sql` — adds `convergence_pairs` table for test-generation separation and convergence tracking (Phase 13)

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
| `model_registry` | Aggregated model catalog (Phase 7) |
| `index_files` | Indexed source files with content hashes (Phase 8) |
| `index_symbols` | Functions, types, interfaces, constants, variables, fields, methods (Phase 8) |
| `index_imports` | Import declarations per file (Phase 8) |
| `index_references` | Symbol references for reverse-dependency queries (Phase 8) |
| `index_packages` | Package/module identity (Phase 8) |
| `index_package_deps` | Package dependency edges (Phase 8) |

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
| `internal/state` | 113 | DB lifecycle (5), projects (6), runs (8), tasks (15), attempts (10), sessions (17: create/get/list/activity/messages/uniqueness/summaries/input-history + Phase 15: update-mode/update-run-id/get-summaries/input-history-by-project/message-count/delete-before/latest-session), events/costs (7), ECOs (5), containers (5), model registry (13), semantic index (22) |
| `internal/project` | 9 | Init, duplicate detection, slugify, discover, paths, SRS write/verify |
| `internal/events` | 11 | Bus creation, SQLite persistence, subscriber fan-out, filtered subscriptions, unsubscribe, view-model event classification, concurrent safety |
| `internal/srs` | 17 | Structure validation (8), bootstrap context (3), draft persistence (5), hash computation (1) |
| `internal/eco` | 13 | Category validation (3), proposal validation (5), file persistence (5) |
| `internal/engine` | 44 | Engine lifecycle (8), run lifecycle (8), SRS lifecycle (9), ECO lifecycle (7), status projections (5), worker pool (5), service interface wiring (2) |
| `internal/gitops` | 38 | Branch management (8), snapshots (2), dirty/clean checks (6), commit formatting (3), add/commit (4), diffs (6), setup work branch (3), cancel cleanup (3), exit criteria (2), architecture compliance (1) |
| `internal/ipc` | 24 | Message types (6), envelope serialization (4), directory management (6), spec writers (5), message read/write (3) |
| `internal/container` | 17 | Container naming (2), hardening flags (7), start/stop lifecycle (4), list/cleanup (3), interface compliance (1) |
| `internal/inference` | 51 | Budget enforcer (11), rate limiter (6), OpenRouter provider (11), BitNet provider (7), broker integration (16) |
| `internal/models` | 19 | Shipped loader (3), OpenRouter fetcher (2), BitNet scanner (2), registry service (7), merge enrichment (1), combined filtering (1), broker maps (1), performance preservation (1), adapter (1) |
| `internal/bitnet` | 11 | Service creation (1), status up/down (2), model listing (2), enabled/disabled (1), base URL (1), start/stop guards (2), weight dir (1), status fields (1) |
| `internal/index` | 24 | Full indexing (3), incremental indexing (2), exclusion rules (2), lookup_symbol (6), reverse_dependencies (1), list_exports (2), find_implementations (1), module_graph (2), multi-language (4), edge cases (3) |
| `internal/task` | 24 | Single creation (5), batch creation (7), retry (2), escalation (3), blocking (1), HandleTaskFailure routing (3), scope expansion (2), per-tier counting (1) |
| `internal/scheduler` | 15 | Dispatch ready tasks (3), lock acquisition (2), lock conflicts (2), concurrency limits (2), lock waiter processing (2), lock ordering (1), edge cases (3) |
| `internal/manifest` | 23 | Parsing (5), validation checks (12), artifact hash tracking (4), path helpers (2) |
| `internal/validation` | 22 | Language detection (6), profiles (5), sandbox spec (2), result aggregation (3), service orchestration (6) |
| `internal/review` | 29 | Risky file detection (4), tier escalation (5), diversification (4), model selection (4), verdict parsing (4), container spec (1), service orchestration (4), orchestrator gate (2), FindRiskyFiles (1) |
| `internal/mergequeue` | 20 | Empty queue (1), queue length (1), clean merge (2), stale snapshot (2), integration failure (2), file operations (2), serialization (1), events (3), commit failure (1), indexer failure (1), affected files (1), git staging (1), file revert (2), context cancellation (1) |
| `internal/session` | 19 | Session create/resume (4), mode determination (5), startup summary (2), transcript (1), compaction (1), export (2), suggestions (2), events (1), input history (1) |
| `internal/tui` | 29 | Model creation (2), view rendering (3), input handling (2), slash commands (6), overlay (1), status bar (1), task rail (1), window resize (1), transcript (1), submit input (2), plain renderer (7) |
| `internal/cli` | 58 | Command registration (7), run actions (15), export (5), models (7), bitnet (6), index (11), session commands (7: list with/without sessions, export, resume found/not-found, TUI plain flag/mode), stubs (10: api/tunnel/skill/doctor existence + phase messages) |

### Test Patterns

- Tests use `t.TempDir()` for isolated filesystem operations
- Database tests create fresh SQLite databases per test
- Engine tests use noop service implementations for testability without Docker or network
- Gitops tests create real temporary git repositories with initial commits for integration testing
- Container tests use a `mockExecutor` that records Docker commands instead of running them
- IPC tests verify filesystem operations against real temp directories
- Inference tests use `httptest.NewServer` for mock provider endpoints and `mockProvider` for broker integration
- Model registry tests use `httptest.NewServer` for mock OpenRouter and BitNet API endpoints
- BitNet service tests use `httptest.NewServer` with a test URL override for mock health and model endpoints
- Indexer tests use embedded fixture files in `internal/index/testdata/` with Go, TypeScript, Python, and Rust source files
- CLI tests create a full `app.App` with real DB, engine, registry, and BitNet service in temp directories, then call action functions directly (not through cobra) for deterministic output verification
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
| 6 | Inference Broker, Provider Routing, and Cost Enforcement | Complete |
| 7 | Model Registry and BitNet Operations | Complete |
| 8 | Semantic Indexer and Typed Query API | Complete |
| 9 | SRS, ECO, and Bootstrap-Mode Workflow | Complete |
| 10 | Task System, Scheduler, and Locking | Complete |
| 11 | Manifest Validation, Validation Sandbox, Review Pipeline | Complete |
| 12 | Merge Queue and Integration Checks | Complete |
| 13 | Test-Generation Separation and Convergence Logic | Complete |
| 14 | Plain CLI Command Surface | Complete |
| 15 | Session UX Manager and Bubble Tea TUI | Complete |
| 16-20 | Remaining phases | Not started |

### Phase 14 Summary

Phase 14 implemented the plain CLI command surface per Architecture Section 27, making the engine fully operable without the full-screen TUI:

- **CLI package** (`cli/`) — New `internal/cli/` package with `Commands()` entry point returning all CLI commands for registration. Commands are split into separate files by domain: `run.go` (project lifecycle), `export.go` (JSON export), `models.go` (model registry), `bitnet.go` (BitNet server), `index.go` (semantic indexer), `stubs.go` (future-phase placeholders). Shared helpers in `helpers.go` provide `findProjectID` (lookup project by root path), `findActiveRun` (lookup active run for a project), and `openApp` (create App from cwd).

- **Project lifecycle commands** (`cli/run.go`) — Four commands wired to engine methods:
  - `axiom run "<prompt>" [--budget <usd>]` — creates a project run in `draft_srs` status using `engine.CreateRun`, defaults budget to config value, prints run ID/status/branch/budget
  - `axiom pause` — finds active run, calls `engine.PauseRun`, prints confirmation
  - `axiom resume` — finds active run, calls `engine.ResumeRun`, prints confirmation
  - `axiom cancel` — finds active run, calls `engine.CancelRun`, prints confirmation

- **Export command** (`cli/export.go`) — `axiom export` outputs human-readable indented JSON containing project identity, active run state, and all tasks for the run. Uses `json.MarshalIndent` for readability per Architecture Section 27.2.

- **Model commands** (`cli/models.go`) — Three subcommands backed by `models.Registry`:
  - `axiom models refresh` — refreshes from shipped data, OpenRouter (if API key configured), and BitNet (if enabled), with per-source warning on failure
  - `axiom models list [--tier <tier>] [--family <family>]` — tabular output via `tabwriter` with ID, family, tier, context window, and source columns
  - `axiom models info <model-id>` — detailed output including pricing, capability flags, strengths, weaknesses, and recommendations

- **BitNet commands** (`cli/bitnet.go`) — Four subcommands backed by `bitnet.Service`:
  - `axiom bitnet start` / `axiom bitnet stop` — delegate to `Service.Start`/`Stop` (manual-mode stubs in current release)
  - `axiom bitnet status` — shows enabled/disabled state, endpoint, running status, and loaded model count
  - `axiom bitnet models` — lists models loaded in the server with ID and owner

- **Index commands** (`cli/index.go`) — Two subcommands backed by `index.Indexer`:
  - `axiom index refresh` — performs full project re-index via `Indexer.Index`
  - `axiom index query --type <type> [--name <name>] [--package <pkg>]` — supports all five Architecture Section 17.5 query types with parameter validation (lookup_symbol/reverse_dependencies/find_implementations require `--name`, list_exports requires `--package`)

- **Stub commands** (`cli/stubs.go`) — Section 27 commands that depend on subsystems from later phases exist as stubs returning informational messages:
  - `axiom api start/stop`, `axiom api token generate [--scope]/list/revoke`, `axiom tunnel start/stop` — Phase 16
  - `axiom skill generate --runtime <rt>` — Phase 17
  - `axiom doctor` — Phase 19
  - Note: `axiom tui`, `axiom session` were stubs in Phase 14 but are now fully implemented (Phase 15).

- **Design decisions** — All commands use engine projections and service methods rather than direct SQLite access, per Architecture constraint 7. Commands are testable via separated action functions that accept an `*app.App` and `io.Writer`, allowing tests to call the logic directly without cobra or filesystem dependencies.

- **Main.go integration** — `cmd/axiom/main.go` registers all commands via `cli.Commands(&verbose)` loop. Pre-existing `version`, `init`, and `status` commands remain in main.go.

- **Test coverage** — 67 tests across 7 test files:
  - `cli_test.go` (7) — command registration tests verifying all Section 27 command groups and subcommand trees exist
  - `run_test.go` (15) — run creation, budget defaults, pause/resume/cancel state transitions, error cases, event emission, helper functions
  - `export_test.go` (5) — JSON output with no run, active run, tasks, indentation, project root inclusion
  - `models_test.go` (7) — refresh, list all, filter by tier/family, no results, info, not found
  - `bitnet_test.go` (6) — status, endpoint, start/stop guards, models, disabled config
  - `index_test.go` (11) — refresh, all 5 query types, invalid type, 4 required parameter validations
  - `stubs_test.go` (16) — all stub commands exist and return phase-appropriate messages

- **Resolved deferred items from prior phases:**
  - `axiom run "<prompt>"` CLI wiring (was deferred from Phase 9)
  - `axiom models` and `axiom bitnet` CLI wiring (was deferred from Phase 7)
  - `axiom index refresh` CLI wiring (was deferred from Phase 8)

See [CLI Reference](cli-reference.md) for the full command documentation.

### Phase 15 Summary

Phase 15 implemented the Session UX Manager and Bubble Tea TUI per Architecture Section 26.2:

- **Session UX Manager** (`session/manager.go`) — Engine-side component responsible for interactive terminal-session orchestration. `CreateSession` creates a new session with auto-determined mode from run state. `ResumeSession` resumes by ID, refreshing mode from current state. `ResumeOrCreateSession` resumes the most recent session or creates a new one. `DetermineMode` maps run status to four session modes: `bootstrap` (no run/draft), `approval` (awaiting SRS), `execution` (active/paused), `postrun` (completed/cancelled/error). `StartupSummary` generates the deterministic startup frame (engine-authored, no LLM) with workspace identity, action card, budget, tasks, and command suggestions. `AddTranscriptMessage` persists messages with auto-incrementing sequence numbers. `CompactSession` summarizes old messages into `ui_session_summaries` and deletes them, keeping only the most recent N messages. `ExportSession` returns the full transcript and summaries. `PromptSuggestions` returns mode-specific deterministic suggestions. `RecordInput`/`InputHistory` manage per-project input history for up-arrow recall. `UpdateMode` transitions session mode and emits `session_mode_changed` events.

- **Bubble Tea TUI** (`tui/model.go`, `tui/styles.go`, `tui/program.go`) — Full-screen interactive terminal UI using Bubble Tea, Bubbles, and Lip Gloss. Layout: top status bar (project, mode, branch, budget), main transcript viewport, right-side task rail (task counts, budget), and footer composer (textarea with mode indicator and shortcut hints). Subscribes to engine event bus for real-time updates. Handles `task_projection_updated`, `session_mode_changed`, `approval_requested`, and `diff_preview_ready` events. Implements 11 slash commands: `/status`, `/tasks`, `/budget`, `/srs`, `/eco`, `/diff`, `/new`, `/resume`, `/clear`, `/theme`, `/help`. Supports `!` shell-mode prefix and up/down arrow input history. Overlay system for help view (Esc to dismiss). Theme with 14 Lip Gloss style definitions using ANSI 256 colors.

- **Plain-text fallback** (`tui/plain.go`) — Line-oriented renderer for non-TTY environments per Section 26.2.11. `RenderStartup` writes the deterministic startup frame. `RenderMessage` writes role-prefixed message lines. `RenderStatus` writes project status. `RenderSessionList` writes session table. `RenderExport` writes full transcript export. `RenderApproval` writes approval prompts. `RenderTaskList` writes task summary. `RenderEvent` writes single event lines. `RunStatus` writes one-line status for polling scripts.

- **CLI commands** (`cli/session.go`) — Replaced Phase 14 stubs with real implementations:
  - `axiom session list` — lists sessions for the project via `PlainRenderer.RenderSessionList`
  - `axiom session resume <id>` — resumes a session via `Manager.ResumeSession`, prints confirmation
  - `axiom session export <id>` — exports transcript via `Manager.ExportSession` + `PlainRenderer.RenderExport`
  - `axiom tui` — launches full-screen Bubble Tea TUI if stdout is TTY, or plain-text startup frame if not
  - `axiom tui --plain` — forces plain-text renderer

- **New state methods** — Added to `state/sessions.go`: `UpdateSessionMode` (change mode), `UpdateSessionRunID` (link to run), `GetLatestSessionByProject` (most recently active), `GetSessionSummaries` (list summaries), `GetMessageCount` (for compaction threshold), `DeleteMessagesBySessionBefore` (compaction deletion), `GetInputHistoryByProject` (recent history). Added to `state/runs.go`: `GetLatestRunByProject` (most recent run regardless of status).

- **Init fix** — `axiom init` now creates the project record in the database so subsequent commands (`session list`, `status`, `tui`) can find the project without requiring `axiom run` first.

- **Test coverage** — 55 new tests:
  - `session/manager_test.go` (19) — session create/resume, mode determination for all run states, startup summary, transcript, compaction, export, suggestions, event emission, input history
  - `tui/tui_test.go` (20) — model creation, view rendering with startup frame, input handling, quit on ctrl+c, all slash commands, overlay toggle, status bar, task rail, window resize, transcript append, submit routing
  - `tui/plain_test.go` (9) — plain renderer startup, active run, messages, status, session list/empty, export
  - `cli/session_test.go` (7) — session list with/without sessions, export with messages, resume found/not-found, TUI plain flag, TUI plain mode output

- **Known deferred items:**
  - File mention autocomplete (`@` trigger)
  - LLM-generated prompt suggestions (deterministic heuristics only)
  - Theme switching (placeholder `/theme` command)
  - Diff preview overlay with syntax highlighting
  - Individual task inspection views
  - Approval card overlay dialogs
  - Shell mode execution (`!` captures intent only)

See [Session & TUI Reference](session-tui.md) for the full documentation.

### Phase 13 Summary

Phase 13 enforced architecture-mandated independence between implementation and test authorship per Architecture Section 11.5:

- **Test-generation service** (`testgen/testgen.go`) — `Service` struct with `CreateTestTask` (creates a test-generation task after implementation merge succeeds, validates impl is done, records implementation model family for exclusion, creates task + dependency + convergence pair in a single atomic transaction), `GetExcludeFamily` (returns the implementation's model family for a given test task, used by the scheduler to enforce family separation), `HandleTestFailure` (creates an implementation-fix task when generated tests fail — includes committed code reference, failing test task reference, and failure output in the fix task description; atomically creates the fix task, dependency, and updates the convergence pair status/iteration), `CheckConvergence` (returns the current convergence status for an implementation task), `MarkConverged` (marks a convergence pair as converged after test task completes successfully; validates test task is done), `MarkBlocked` (marks a convergence pair as blocked after exhausting retries in the fix loop), `IsFeatureDone` (returns true only when convergence pair status is `converged`).

- **Convergence tracking** (`state/convergence.go`, `migrations/006_convergence_pairs.sql`) — New `convergence_pairs` table linking implementation tasks to their test-generation and fix tasks with status lifecycle: `pending → testing → fixing → converged` (or `blocked`). Fields: `impl_task_id`, `test_task_id`, `fix_task_id`, `status`, `impl_model_family`, `iteration` (incremented on each fix cycle), `converged_at`. Indexes on `impl_task_id`, `test_task_id`, and `status`. State layer CRUD: `CreateConvergencePair`, `GetConvergencePair`, `GetConvergencePairByImplTask`, `GetConvergencePairByTestTask`, `UpdateConvergencePairStatus`, `SetConvergenceTestTask`, `SetConvergenceFixTask`, `IncrementConvergenceIteration`, `ListConvergencePairsByRun`.

- **Model family exclusion in scheduler** (`scheduler/scheduler.go`) — New `FamilyExcluder` interface with `GetExcludeFamily(ctx, taskID)` method. The scheduler's `dispatch()` method calls the excluder before model selection. For test-type tasks, the implementation's model family is passed as `excludeFamily` to `ModelSelector.SelectModel()`, ensuring a different model family is selected. When no `FamilyExcluder` is configured (backward compatible), the scheduler passes empty `excludeFamily` as before.

- **Engine model selector update** (`engine/scheduler.go`) — `engineModelSelector.SelectModel` now uses the `excludeFamily` parameter (previously ignored as `_ string`). When `excludeFamily` is set, it iterates available models at the requested tier and returns the first model from a different family. If all models at the tier are from the excluded family, a warning is logged (Section 11.5 violation) and falls back to the first available model.

- **Engine integration** (`engine/engine.go`, `engine/scheduler.go`) — `testgen.Service` created in `Engine.New()` and wired to the scheduler via `engineFamilyExcluder` adapter. `Engine.TestGen()` accessor exposes the service. The testgen service receives the event bus for lifecycle event emission.

- **Event types** (`events/types.go`) — Four new authoritative events: `testgen_created` (test task created for implementation), `testgen_converged` (implementation + tests all green), `testgen_fix_created` (fix task spawned after test failure), `testgen_blocked` (convergence exhausted retries). Events emitted in `CreateTestTask`, `HandleTestFailure`, `MarkConverged`, and `MarkBlocked`.

- **Atomicity** — Both `CreateTestTask` and `HandleTestFailure` execute all database mutations (task creation, dependency insertion, convergence pair creation/update) within a single `WithTx` transaction. If any step fails, the entire operation rolls back — no orphaned tasks or inconsistent convergence state.

- **Test coverage** — 27 new tests across two files:
  - `testgen/testgen_test.go` (24 tests) — CreateTestTask (5 tests: success, convergence pair creation, rejects non-done impl, rejects non-impl type, rejects impl without successful attempt, rejects duplicate), GetExcludeFamily (2 tests: returns impl family, returns empty for non-test), HandleTestFailure (4 tests: creates fix task, updates convergence pair, rejects non-failed test, rejects non-test type), CheckConvergence (4 tests: pending/testing/fixing/no-pair), MarkConverged (2 tests: success, rejects when test not done), IsFeatureDone (3 tests: true when converged, false when testing, false when no pair), MarkBlocked (1 test), model family parameterized (4 sub-tests: anthropic/openai/google/meta), fix task description content (1 test).
  - `scheduler/testgen_test.go` (3 tests) — test task uses exclude family, impl task uses empty exclude family, nil FamilyExcluder backward compatibility.

- **Known deferred items:**
  - Automatic test task creation triggered by merge queue success events (currently `CreateTestTask` must be called explicitly by the orchestrator — Phase 16+)
  - Automatic convergence detection when test tasks complete (currently `MarkConverged` must be called explicitly)
  - Context packaging of committed implementation + semantic index for test-generation TaskSpecs (the convergence pair tracks the data; TaskSpec construction is orchestrator responsibility)

See [Test-Generation Separation Reference](test-generation.md) for the full API.

### Phase 12 Summary

Phase 12 implemented the serialized merge queue and integration checks per Architecture Sections 16.4, 23.2, 23.3, and 30.2:

Phase 12 implemented the serialized merge queue and integration checks per Architecture Sections 16.4, 23.2, 23.3, and 30.2:

- **Merge queue service** (`mergequeue/mergequeue.go`) — `Queue` struct with `Enqueue` (adds approved output to queue, emits `merge_queued` event), `Tick` (processes one item per invocation for serialization), and `Len`. The `Tick` method executes all 10 steps from Architecture Section 16.4: validate base_snapshot against HEAD, detect conflicts via git diff, apply Meeseeks output to project (add/modify/delete/rename), run integration checks, commit on success or revert and requeue on failure, re-index changed files, release write-set locks, and mark the task done.

- **Conflict detection** — Uses `git diff --name-only` via the `ChangedFilesSince` git operation to identify files that actually changed between the base_snapshot and current HEAD. Only output files that overlap with genuinely changed files are treated as conflicts, preventing excessive requeuing when unrelated tasks commit to non-overlapping files. Falls back to conservative file-existence checking when git diff is unavailable.

- **File apply/revert** — `applyOutput` copies files from the Meeseeks staging directory to the project, handles deletions and renames, and creates a `fileBackup` with original file contents. `revertOutput` restores all files to their pre-apply state when integration checks or commits fail. Newly added files are removed on revert; modified files are restored to original content.

- **Commit protocol** — `formatCommitMessage` produces architecture-compliant commit messages per Section 23.2: `[axiom] <task-title>` header followed by Task, SRS Refs, Meeseeks Model, Reviewer Model, Attempt, Cost, and Base Snapshot metadata lines.

- **Integration checks** — The `Validator` interface abstracts project-wide build/test/lint checks per Section 23.3. The engine adapter currently uses a stub validator (logs a warning when used) that will be connected to real Docker validation containers when the full execution pipeline is complete.

- **Failure handling** — Per Sections 23.3 and 30.2: on integration check failure, the merge queue reverts applied files, stores structured feedback on the latest attempt record (for inclusion in the next TaskSpec), releases write-set locks, requeues the task, and emits a `merge_failed` event.

- **Engine integration** (`engine/mergequeue.go`) — Six adapter types bridge engine services to merge queue interfaces: `mergeQueueGitAdapter` (wraps `GitService` with `ChangedFilesSince`), `mergeQueueValidatorAdapter` (stub for integration checks), `mergeQueueIndexAdapter` (wraps `IndexService`), `mergeQueueLockAdapter` (wraps `scheduler.ReleaseLocks`), `mergeQueueTaskAdapter` (handles `CompleteTask` and `RequeueTask` with feedback persistence), `mergeQueueEventAdapter` (wraps `events.Bus`). The merge queue runs as a 500ms background worker alongside the scheduler.

- **GitService expansion** — Extended `engine.GitService` interface with `AddFiles(dir, files)`, `Commit(dir, message)`, and `ChangedFilesSince(dir, sinceRef)`. Added `ChangedFilesSince` to `gitops.Manager` using `git diff --name-only`.

- **Events** — Three merge queue events: `merge_queued` (on enqueue), `merge_succeeded` (after commit), `merge_failed` (on any failure). All events were already defined in `events/types.go` from Phase 3.

- **Test coverage** — 20 tests covering: empty queue no-op, queue length tracking, clean merge success, commit message format verification, stale snapshot with no conflicts (proceed), stale snapshot with conflicts (requeue), integration check failure (revert + requeue), file deletions, file renames, serialization (one-at-a-time), event emission (queued/succeeded/failed), commit failure handling, indexer failure non-fatality, all affected files indexed, correct git staging, integration check file revert, new file cleanup on revert, and context cancellation.

- **Known deferred items:**
  - Real integration check execution via Docker validation containers (validator interface is abstracted; stub adapter passes all checks)
  - Three-way merge for stale snapshots (currently uses whole-file replacement; textual merge would reduce requeuing further)

See [Approval Pipeline Reference](approval-pipeline.md) for the full Stage 5 documentation.

### Phase 11 Summary

Phase 11 implemented the approval pipeline that protects the repo from bad output, covering Architecture Sections 10.4, 11, 13, and 14:

- **Manifest parser and validator** (`manifest/manifest.go`) — `ParseManifest` parses the `manifest.json` emitted by Meeseeks with required field validation (task_id, base_snapshot). `ValidateManifest` performs all Stage 1 checks from Section 14.2: file existence in staging, no unlisted files, path canonicalization (no traversal, no absolute paths), symlink rejection via `os.Lstat`, non-regular file rejection, configurable file-size limits (default 50 MB), scope enforcement via prefix matching (nil means unrestricted), and duplicate path detection. Validation covers all path categories: added, modified, deleted, and renamed files.

- **Artifact hash tracking** (`manifest/manifest.go`) — `ComputeArtifacts` computes SHA-256 hashes and file sizes for all file operations using streaming I/O (`io.Copy` into `sha256.New()`). Renames are first-class operations per Section 10.4 — they preserve both "from" and "to" paths through validation and audit logging, never degraded into delete-plus-add pairs. Each `ArtifactRecord` includes the `AttemptID` for persistence via `state.TaskArtifact`.

- **Validation sandbox** (`validation/validation.go`) — `Service.RunChecks` orchestrates hermetic validation per Section 13.3: builds a container spec with hardcoded `network=none` (security invariant — ignores config), read-only project mount, read-write staging overlay, no secrets. Containers are always destroyed after checks complete (deferred cleanup). `DetectLanguages` inspects project directories for language markers with deterministic sorted output. `GetProfile` returns language-specific validation commands and dependency strategies per Section 13.5: Go (vendored modules), Node (`npm ci --ignore-scripts --offline`), Python (`pip install --no-index --find-links`), Rust (pre-populated registry). `FormatResults` produces human-readable summaries for inclusion in ReviewSpecs per Section 13.9.

- **Reviewer pipeline** (`review/review.go`) — `Service.RunReview` orchestrates the full Stage 3 pipeline: detects risky files per Section 11.6 (CI/CD configs, package manifests, infrastructure, auth/security code, migrations, build scripts), escalates local/cheap tiers to standard when risky files are present, selects a reviewer model with family diversification for standard/premium tiers per Section 11.3 (falls back to same family when no alternative exists), starts a reviewer container with no network and no project filesystem access per Section 11.8, and parses the structured verdict output. `ParseVerdict` extracts APPROVE/REJECT with robust feedback capture — malformed output defaults to REJECT (fail-safe). For REJECT verdicts with no explicit feedback section, all post-verdict content is captured as feedback.

- **Orchestrator gate** (`review/review.go`) — `OrchestratorGate` implements Section 14.2 Stage 4 as a pass-through for reviewer decisions. Future versions will add SRS cross-validation via IPC.

- **Test patterns** — All 74 tests follow existing codebase conventions: pure `testing.T` assertions, `t.TempDir()` for filesystem isolation, simple mock structs for `engine.ContainerService`, `CheckRunner`, `ReviewRunner`, and `ModelSelector` interfaces. No external test dependencies.

- **Known deferred items:**
  - Actual container execution of validation checks (runner interface is abstracted; real Docker execution wired when task execution pipeline is complete)
  - Warm sandbox pools (Section 13.8 — behind `warm_pool_enabled = false` feature flag)
  - Integration sandbox with scoped secrets/network (Section 13.6 — config struct exists, not implemented)
  - Batched review for local-tier tasks (Section 14.3)
  - SRS cross-validation in orchestrator gate (requires orchestrator IPC — Phase 16)

See [Approval Pipeline Reference](approval-pipeline.md) for the full API.

### Phase 10 Summary

Phase 10 implemented the task system, execution scheduler, and write-set locking per Architecture Sections 15, 16, 22, and 30:

- **Task service** (`task/service.go`) — `Service` struct with `CreateTask` (single, transactional), `CreateBatch` (batch with DFS cycle detection), `HandleTaskFailure` (routes to retry/escalate/block based on attempt history), `RetryTask` (requeue at same tier), `EscalateTask` (bump to next tier: local→cheap→standard→premium), `BlockTask` (mark as requiring orchestrator intervention), and `RequestScopeExpansion` (atomic lock acquisition for additional files or move to `waiting_on_lock`).

- **Cycle detection** — DFS with three-color marking (white/gray/black) over the batch dependency graph. Detects direct cycles, transitive cycles, and self-dependencies. Dependencies referencing already-persisted tasks are checked for existence but not traversed (cycle-free by induction).

- **Retry/escalation/blocking** — Per Architecture Section 30.1: `MaxRetriesPerTier = 3` (attempts counted per tier using the `task_attempts.tier` column), `MaxEscalations = 2` (counted as distinct tiers in attempt history minus one). Tier chain: `local → cheap → standard → premium`. After exhaustion: `failed → blocked` (direct state transition).

- **Scheduler** (`scheduler/scheduler.go`) — `Scheduler` struct with `Tick` (periodic dispatch across all active runs), `ReleaseLocks` (release + process waiters). Tick loop: count in-progress tasks, find queued tasks with all deps done, acquire lock sets atomically in deterministic order, dispatch up to `MaxMeeseeks` concurrency limit.

- **Lock acquisition** — Per Architecture Section 16.3: locks sorted by `(resource_type, resource_key)` for deadlock prevention, acquired in a single database transaction (all-or-nothing). On conflict the transaction rolls back — no partial locks. Conflicting tasks move to `waiting_on_lock` with a `task_lock_waits` record.

- **Lock waiter processing** — On lock release, all `waiting_on_lock` tasks are scanned. If a waiter's requested resources are all free, it transitions `waiting_on_lock → queued` and the lock wait record is removed.

- **Dispatch** — Selects a model via `ModelSelector` interface, captures current HEAD via `SnapshotProvider` for base_snapshot pinning (Section 16.2), computes attempt number, transitions `queued → in_progress`, creates `task_attempts` record with `status = running`, `phase = executing`, and the task's current tier.

- **Engine integration** (`engine/scheduler.go`) — Scheduler registered as a 500ms background worker. `engineModelSelector` adapts `ModelService.List()` to `scheduler.ModelSelector` (with `excludeFamily` support for test-generation separation per Section 11.5). `engineSnapshotProvider` adapts `GitService.CurrentHEAD()` to `scheduler.SnapshotProvider`. `engineFamilyExcluder` adapts `testgen.Service.GetExcludeFamily()` to `scheduler.FamilyExcluder`.

- **Schema evolution** (`migrations/005_attempt_tier.sql`) — Adds `tier TEXT NOT NULL DEFAULT 'standard'` column to `task_attempts` for per-tier retry counting.

- **State transition additions** — `in_progress → waiting_on_lock` (scope expansion conflict) and `failed → blocked` (retry/escalation exhaustion) added to `validTaskTransitions`.

- **Known deferred items:**
  - Actual container spawning on dispatch (Phase 11+ — the scheduler creates the attempt record but does not start containers)
  - Cross-batch cycle detection (only within-batch cycles are detected; cross-batch cycles prevented by topological ordering in practice)
  - Context invalidation warnings for active Meeseeks (Architecture Section 16.5 — optional optimization)

See [Task System, Scheduler, and Locking Reference](task-scheduler.md) for the full API.

### Phase 9 Summary

Phase 9 implemented the SRS approval state machine and ECO lifecycle per Architecture Sections 6, 7, and 8.7:

- **SRS validation** (`srs/srs.go`) — `ValidateStructure` checks that submitted SRS content contains all four required top-level sections from Architecture Section 6.1: Architecture, Requirements & Constraints, Test Strategy, and Acceptance Criteria. Also validates the `# SRS: <Project Name>` title format. Structural validation only — content quality is the orchestrator's responsibility.

- **Bootstrap context** (`srs/srs.go`) — `BuildBootstrapContext` assembles scoped context for SRS generation per Architecture Section 8.7. For greenfield projects: only the project root (no repo-map, no semantic index). For existing projects: project root plus a read-only file listing excluding `.axiom/`, `.git/`, and `node_modules/`. The `BootstrapContext` struct carries `ProjectRoot`, `IsGreenfield`, and `RepoMap`.

- **SRS draft persistence** (`srs/srs.go`) — `WriteDraft`, `ReadDraft`, `DeleteDraft` persist pending SRS drafts as `.axiom/srs-draft-<run-id>.md` files. Supports multiple revision cycles (submit → reject → revise → resubmit) and survives engine restarts.

- **SRS hash computation** (`srs/srs.go`) — `ComputeHash` returns the hex-encoded SHA-256 hash of content, used for both file and database hash storage.

- **Engine SRS methods** (`engine/srs.go`) — Three methods implementing the SRS approval state machine:
  - `SubmitSRS(runID, content)` — validates structure, persists draft, transitions `draft_srs → awaiting_srs_approval`, emits `srs_submitted`
  - `ApproveSRS(runID)` — reads draft, writes read-only `.axiom/srs.md` (0o444 permissions) via `project.WriteSRS`, writes `.axiom/srs.md.sha256`, stores hash in `project_runs.srs_hash` via `UpdateRunSRSHash`, transitions `awaiting_srs_approval → active`, deletes draft, emits `srs_approved`
  - `RejectSRS(runID, feedback)` — transitions `awaiting_srs_approval → draft_srs`, emits `srs_rejected` with feedback. Draft is preserved for revision.

- **ECO validation** (`eco/eco.go`) — `ValidCategory` checks against the 6 allowed codes from Architecture Section 7.2 (ECO-DEP, ECO-API, ECO-SEC, ECO-PLT, ECO-LIC, ECO-PRV). `ValidateProposal` checks category validity plus required fields (description, affected refs, proposed change). `CategoryDescription` returns human-readable names.

- **ECO file persistence** (`eco/eco.go`) — `WriteECOFile` writes append-only markdown records to `.axiom/eco/<ECO-code>.md` matching the format from Architecture Section 7.4 (title with category code, filed timestamp, status, affected sections, environmental issue, proposed substitute, impact assessment). `ListECOFiles` returns sorted filenames.

- **Engine ECO methods** (`engine/eco.go`) — Three methods implementing the ECO lifecycle:
  - `ProposeECO(proposal)` — validates proposal, verifies run is active or paused, auto-generates sequential ECO codes (ECO-001, ECO-002, ...), creates `eco_log` entry with `proposed` status, emits `eco_proposed`
  - `ApproveECO(ecoID, approvedBy)` — transitions to `approved`, writes ECO markdown file to `.axiom/eco/`, emits `eco_resolved` with `resolution: approved`
  - `RejectECO(ecoID)` — transitions to `rejected`, emits `eco_resolved` with `resolution: rejected`

- **State layer additions** — `UpdateRunSRSHash(id, hash)` method added to `state.DB` for storing the SRS SHA-256 hash on a run record.

- **Event additions** — Three new authoritative event types: `srs_submitted`, `srs_approved`, `srs_rejected`. ECO events (`eco_proposed`, `eco_resolved`) were already defined in Phase 3.

- **ECO-to-task integration hooks** — The existing `Task.ECORef` foreign key and `TaskCancelledECO` status provide the hook points for ECO-driven task cancellation and replanning. The actual task replanning logic is the orchestrator's responsibility (Phase 10+).

- **Known deferred items:**
  - ~~`axiom run "<prompt>"` CLI command wiring~~ — resolved in Phase 14
  - SRS approval delegation to Claw (engine infrastructure is ready; Claw integration is Phase 16)
  - SRS hash verification on engine startup (Phase 19)
  - Full semantic index query access during bootstrap for existing projects (currently provides file listing; full index queries available via `IndexService`)

See [SRS and ECO Reference](srs-eco.md) for the full API.

### Phase 8 Summary

Phase 8 implemented the semantic indexer and typed query API per Architecture Section 17:

- **Semantic index tables** (`migrations/004_semantic_index.sql`) — 6 SQLite tables for structured code indexing: `index_files` (tracked files with SHA-256 content hashes for incremental reindexing), `index_symbols` (functions, types, interfaces, constants, variables, fields, methods with kind checks, parent references, and exported status), `index_imports` (per-file import declarations), `index_references` (symbol references with usage type: call, reference, implementation), `index_packages` (package identity), and `index_package_deps` (dependency edges). 11 performance indexes on name, kind, file, parent, import path, symbol name, and package path.

- **State layer CRUD** (`state/index.go`) — 20 repository methods: `CreateIndexFile`, `GetIndexFile`, `DeleteIndexFile` (cascades to symbols/imports/references), `UpdateIndexFileHash`, `ListIndexFiles`, `ClearIndex`, `CreateIndexSymbol`, `ListSymbolsByFile`, `LookupSymbol` (with optional kind filter, joins file paths), `ListExportedSymbolsByPackageDir`, `FindImplementations`, `CreateIndexImport`, `ListImportsByFile`, `ListImporterFiles`, `CreateIndexReference`, `ListReferencesBySymbol`, `CreateIndexPackage`, `GetIndexPackage`, `AddPackageDep` (idempotent), `ListPackageDeps`.

- **Parser abstraction** (`index/parser.go`) — `Parser` interface with `Parse(source, relPath)` returning `ParseResult` (symbols, imports, references) and `Language()`. Language-specific parsers registered at init time. Parser implementations:
  - **Go** (`parser_go.go`) — Uses Go stdlib `go/parser` + `go/ast` for full AST analysis: function signatures with receiver types, type/interface/struct declarations with field and method extraction, const/var declarations, import paths with aliases, function call references. Formats complete function signatures. Superior to tree-sitter for Go-specific analysis.
  - **TypeScript** (`parser_ts.go`) — Regex-based extraction of exported/private functions, classes, interfaces, type aliases, const/let/var declarations, and import statements. Covers `export` keyword detection.
  - **JavaScript** (`parser_js.go`) — Reuses TypeScript parser patterns since declaration syntax is compatible.
  - **Python** (`parser_python.go`) — Regex-based extraction of classes, functions/methods (distinguished by indentation), UPPER_CASE constants, module-level variables, and import/from-import statements. Respects `_` prefix convention for private symbols.
  - **Rust** (`parser_rust.go`) — Regex-based extraction of `pub`/private fn, struct, trait, enum, type, const, static declarations, `use` statements as imports, and `impl Trait for Type` as implementation references.

- **Indexer service** (`index/indexer.go`) — `Indexer` struct backed by `state.DB`. `Index(ctx, dir)` performs full project indexing: walks the directory tree, excludes `.axiom/`, `.git/`, `node_modules/`, `vendor/`, `__pycache__/`, `target/`, `dist/`, `build/`, and non-source files. Parses each file, stores symbols with parent linking (methods → types), imports, and references. Post-index pass detects Go interface implementations by matching struct method sets against interface method sets. Builds package dependency graph from import declarations with Go module path resolution. `IndexFiles(ctx, dir, paths)` performs incremental reindexing: computes SHA-256 content hashes, skips unchanged files, deletes and re-indexes changed files.

- **Typed query API** (`index/query.go`) — 5 query methods per Architecture Section 17.5:
  - `LookupSymbol(name, kind)` — finds symbols by name with optional kind filter, returns file paths, line numbers, signatures, export status
  - `ReverseDependencies(symbolName)` — returns all files/symbols that reference a symbol, with usage type (call, reference, implementation)
  - `ListExports(packagePath)` — returns all exported symbols in a package directory
  - `FindImplementations(interfaceName)` — returns types implementing an interface (via implementation references)
  - `ModuleGraph(rootPackage)` — returns package dependency graph; full graph when rootPackage is empty, BFS subgraph when rooted

- **Engine integration** — `IndexService` interface in `engine/interfaces.go` expanded from 1 method to 7 methods with `SymbolResult`, `ReferenceResult`, `ModuleGraphResult`, `PackageNode`, and `PackageEdge` types. `IndexerAdapter` in `index/engine_adapter.go` bridges `Indexer` to `engine.IndexService` with compile-time assertion.

- **Known deferred items:**
  - tree-sitter CGO bindings for non-Go languages (currently regex-based; designed for drop-in upgrade when a C compiler is available)
  - Implementation detection line numbers (currently 0 for Go interface implementations detected post-parse)
  - ~~CLI command wiring for `axiom index refresh`~~ — resolved in Phase 14

See [Semantic Indexer Reference](semantic-indexer.md) for the full API.

### Phase 7 Summary

Phase 7 implemented the model registry and BitNet server lifecycle per Architecture Sections 18 and 19:

- **Model registry table** (`migrations/003_model_registry.sql`) — SQLite table with all 18 fields from Section 18.3: id, family, source, tier, context/output windows, pricing, capability tags (strengths/weaknesses/recommended_for/not_recommended_for), feature flags (tools/vision/grammar), historical performance metrics, and last_updated timestamp. Indexed on tier, family, and source.

- **State layer CRUD** (`state/model_registry.go`) — 10 repository methods: `UpsertModel` (INSERT OR REPLACE preserving performance history via COALESCE), `GetModel`, `ListModels` (ordered by tier then ID), `ListModelsByTier`, `ListModelsByFamily`, `ListModelsByTierAndFamily` (combined filter), `DeleteModel`, `DeleteModelsBySource`, `ModelCountByTier`, and `UpdateModelPerformance`. JSON array encoding/decoding helpers for string slice columns.

- **Shipped capability index** (`models/models.json`) — 31 curated models embedded via `embed.FS`:
  - **Premium (8):** Claude Opus 4.6, GPT-5.4, GPT-5.4 Pro, Gemini 3.1 Pro Preview, Grok 4.20, Grok 4, o3-pro, MiMo-V2-Pro
  - **Standard (12):** Claude Sonnet 4.6, GPT-5.3 Codex, o3, Kimi K2.5, Gemini 2.5 Pro, Devstral 2, Mistral Large, DeepSeek V3.2, Qwen3-Coder-Plus, Qwen3-Coder-Next, Llama 4 Maverick, Trinity Large Thinking
  - **Cheap (10):** GPT-5.4 Mini, Claude Haiku 4.5, GPT-5.4 Nano, Gemini 2.5 Flash, Gemini 2.5 Flash Lite, o4-mini, Devstral Small, MiMo-V2 Flash, DeepSeek R1-0528
  - **Local (4):** Falcon3-1B/3B/7B/10B Instruct (1.58-bit, zero cost, GBNF grammar support)

- **OpenRouter fetcher** (`models/openrouter.go`) — Fetches model list from OpenRouter `/api/v1/models`, parses per-token pricing, auto-classifies tiers by price thresholds, extracts family from model ID, and merges capability data from shipped models when IDs match.

- **BitNet scanner** (`models/bitnet_models.go`) — Fetches loaded models from BitNet server `/v1/models`, normalizes Falcon model names to `bitnet/<name>` format, estimates context windows by model size, and marks all as local tier with grammar support.

- **Registry service** (`models/registry.go`) — `RefreshShipped`, `RefreshOpenRouter`, and `RefreshBitNet` methods that independently load their sources into the SQLite registry. `RefreshOpenRouter` enriches fetched models with shipped capability data (strengths, weaknesses, tools, vision, grammar, tier override). `List` supports filtering by tier, family, or both. `BrokerMaps()` extracts `ModelPricing` and tier maps for the inference broker.

- **Engine adapter** (`models/engine_adapter.go`) — `RegistryAdapter` bridges `Registry` to `engine.ModelService` interface with compile-time assertion. Converts `state.ModelRegistryEntry` to `engine.ModelInfo` including performance history fields.

- **BitNet service** (`bitnet/service.go`) — Server lifecycle management: `Status` (health check via `/health` + model count), `ListModels` (query `/v1/models`), `Start`/`Stop` (manual-mode stubs for initial release), `Enabled` (config-driven), `BaseURL` (constructed from config), `WeightDir` (resolves `~/.axiom/bitnet/models/`). Sentinel errors: `ErrDisabled`, `ErrNotRunning`, `ErrNoWeights`.

- **Engine integration** — `ModelService` interface added to `engine/interfaces.go` with `RefreshShipped`, `RefreshOpenRouter`, `RefreshBitNet`, `List`, and `Get` methods. `ModelInfo` struct includes all registry fields plus performance history. `Models` field added to `Engine.Options` and wired in `Engine` constructor.

- **App wiring** (`app/app.go`) — `Open()` now creates a `models.Registry`, loads shipped models at startup, creates a `bitnet.Service`, and passes a `RegistryAdapter` as the engine's `ModelService`. Both `Registry` and `BitNet` service are exposed on the `App` struct for CLI access.

- **Known deferred items:**
  - Full BitNet process management (spawning `bitnet.cpp`) — currently requires manual server start
  - First-run weight download with confirmation prompt (Architecture Section 19.9)
  - ~~CLI command wiring for `axiom models` and `axiom bitnet` commands~~ — resolved in Phase 14
  - Dynamic model pricing refresh from OpenRouter on broker construction (currently static at startup)

See [Model Registry Reference](model-registry.md) for the full API.

### Phase 6 Summary

Phase 6 centralized all model access behind engine policy with the `internal/inference/` package:

- **Provider abstraction** (`provider.go`) — `Provider` interface with `Name()`, `Available()`, and `Complete()` methods. Shared types: `ProviderRequest`, `ProviderResponse`, `Message`, `ModelPricing`. Sentinel errors for budget exceeded, rate limit, model not allowed, token cap, and provider down.

- **OpenRouter provider** (`openrouter.go`) — HTTP client for the OpenRouter chat completions API (`POST /chat/completions`). Implements the OpenAI-compatible request/response format with Bearer token authentication. Configurable timeout via functional options. Response parsing extracts content, finish reason, and token usage. Handles all error status codes (402 payment required, 429 rate limited, 500+ server errors).

- **BitNet provider** (`bitnet_provider.go`) — HTTP client for a local BitNet inference server using the same OpenAI-compatible API format. Supports GBNF grammar constraints for structured output (Architecture Section 19.3). Grammar field is only included in the request body when non-nil.

- **Budget enforcer** (`budget.go`) — Goroutine-safe budget tracker implementing pre-authorization per Architecture Section 21.3. `Authorize(maxTokens, pricing)` calculates worst-case cost (`max_tokens * completion_cost_per_token`) and rejects if it exceeds remaining budget. Zero-cost models (BitNet) are always authorized regardless of budget. `Record()` tracks actual spend. `WarnReached()` and `Exceeded()` check thresholds.

- **Rate limiter** (`ratelimit.go`) — Goroutine-safe per-task request counter. Default limit is 50 requests per task (configurable via `inference.max_requests_per_task`). `Reset()` clears the count for a task (used on retry with fresh container).

- **Inference broker** (`broker.go`) — Central broker service implementing `engine.InferenceService`. The `Infer()` method enforces four validation checks before any provider call: (1) token cap, (2) model allowlist + tier hierarchy, (3) budget pre-authorization, (4) per-task rate limit. Routes requests to the appropriate provider (cloud for standard/premium/cheap tiers, local for local tier). Logs every completed request to the `cost_log` table via `state.DB.CreateCostLog()`. Emits `inference_requested`, `inference_completed`, `inference_failed`, `provider_unavailable`, `budget_warning`, and `budget_exceeded` events via the event bus. Tracks latency in event details.

- **Tier hierarchy** — The model allowlist enforces that a task at tier N may use models at tier N or below: `local(0) < cheap(1) < standard(2) < premium(3)`. A local-tier task cannot request a standard-tier model.

- **Config additions** — `[inference]` section added to `config.Config` with `openrouter_api_key`, `openrouter_base_url`, `max_requests_per_task`, `token_cap_per_request`, and `timeout_seconds`. API keys are stored in trusted config only (never in containers).

- **Event additions** — Five new authoritative event types: `inference_requested`, `inference_completed`, `inference_failed`, `provider_available`, `provider_unavailable`.

- **Interface evolution** — `engine.InferenceRequest` expanded with `RunID`, `AttemptID`, `AgentType`, `Tier`, `Messages`, `GrammarConstraints`. `engine.InferenceResponse` expanded with `FinishReason` and `ProviderName`. Compile-time interface assertion (`var _ engine.InferenceService = (*Broker)(nil)`) ensures the broker satisfies the engine interface.

- **Known deferred items** — Streaming via chunked IPC output files (requires Phase 10 task execution). Queue-until-connectivity for non-local tasks when cloud is down (returns `ErrProviderDown` immediately; queuing belongs in Phase 10 scheduler).

See [Inference Broker Reference](inference-broker.md) for the full API.

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
  - **Authoritative events** (28+ types: `run_created`, `task_started`, `srs_submitted`, `srs_approved`, `srs_rejected`, `inference_completed`, `provider_unavailable`, etc.) are persisted to the SQLite `events` table as the audit trail (Architecture Section 22.4).
  - **View-model events** (8 types: `startup_summary`, `session_mode_changed`, `task_projection_updated`, etc.) are fanned out to in-memory subscribers but NOT persisted (Architecture Section 26.2.10).
  - Subscriber fan-out supports optional filters, buffered channels, and concurrent-safe operation.
  - SQLite writes are serialized via a dedicated write mutex to avoid SQLITE_BUSY under concurrent publishes.

- **Service interfaces** (`internal/engine/interfaces.go`) — Abstractions for `GitService`, `ContainerService`, `InferenceService`, `IndexService`, and `ModelService` so orchestration logic is testable without real Docker or network calls. Tests use noop implementations. `InferenceRequest` includes fields for run/task/attempt tracking, model tier, messages, grammar constraints; `InferenceResponse` includes cost, token counts, provider name, and finish reason. Phase 6 provides a real implementation of `InferenceService` via the `inference.Broker`. Phase 8 expanded `IndexService` from a single `Index()` method to a full typed query API with 7 methods (see [Semantic Indexer Reference](semantic-indexer.md)).

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
