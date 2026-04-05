# Development Guide

## Repository Structure

```
axiom/
├── cmd/
│   └── axiom/              # CLI entrypoint
│       └── main.go         # Cobra command definitions
├── internal/               # Private application packages
│   ├── app/                # Composition root (wires config, state, services)
│   ├── config/             # TOML config loading, validation, layering
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
│   │   --- Future packages (directories scaffolded, not yet implemented) ---
│   ├── api/                # REST + WebSocket API server
│   ├── audit/              # Audit logging
│   ├── bitnet/             # Local BitNet inference integration
│   ├── budget/             # Budget enforcement and cost tracking
│   ├── cli/                # CLI command helpers
│   ├── container/          # Docker container lifecycle management
│   ├── doctor/             # System health checks
│   ├── eco/                # Engineering Change Order management
│   ├── events/             # Event emitter and subscriptions
│   ├── gitops/             # Git operations (branch, commit, diff, snapshot)
│   ├── index/              # Semantic indexer (tree-sitter)
│   ├── inference/          # Inference broker and provider routing
│   ├── ipc/                # Filesystem IPC for container communication
│   ├── manifest/           # Output manifest parsing and validation
│   ├── mergequeue/         # Serialized merge queue
│   ├── models/             # Model registry
│   ├── orchestrator/       # Orchestrator lifecycle management
│   ├��─ review/             # Review pipeline
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

The initial migration (`001_initial_schema.sql`) creates 20 tables matching the architecture's Section 15.2. All tables have corresponding repository methods in the `state` package (see [Database Schema Reference](database-schema.md) for the full repository API):

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

### Test Patterns

- Tests use `t.TempDir()` for isolated filesystem operations
- Database tests create fresh SQLite databases per test
- No external service dependencies (Docker, network) in current tests

## Architecture Constraints

The following rules from ARCHITECTURE.md govern all implementation:

1. **Engine authority** — the Go engine is the sole trusted authority for all privileged operations
2. **SQLite source of truth** — all state lives in SQLite, not in-memory
3. **Untrusted agents** — all LLM agents are stateless and sandboxed
4. **Immutable SRS** — SHA-256 verified on every engine startup
5. **Network isolation** — containers have no network access (`network_mode = "none"`)
6. **No direct project mount** — containers never see the project filesystem
7. **View-model clients** — TUI and API consume engine-authored events, never read SQLite directly

See [ARCHITECTURE.md](../ARCHITECTURE.md) for the complete specification.

## Implementation Status

| Phase | Name | Status |
|-------|------|--------|
| 0 | Foundation and Repo Bootstrap | Complete |
| 1 | Project Bootstrap, Config, and Filesystem Contracts | Complete |
| 2 | SQLite State Store and Core Domain Services | Complete |
| 3 | Engine Kernel and Event Infrastructure | Not started |
| 4–20 | Remaining phases | Not started |

### Phase 2 Summary

Phase 2 added the full domain service layer to the `state` package:

- **Domain models** — 21 typed structs matching every table in the schema, plus typed status enums with `Valid*Transition()` functions
- **Repository methods** ��� CRUD operations for all entities (projects, runs, tasks, attempts, validation/review runs, artifacts, sessions, events, costs, ECOs, containers)
- **Transactional helpers** — `WithTx` for atomic read-then-write patterns; used by all status transition methods
- **Invariant enforcement** — status transitions are validated before SQL execution; invalid transitions return `ErrInvalidTransition`
- **Lock management** — `AcquireLock` is transactional with `ErrLockConflict` detection; `ReleaseTaskLocks` for batch cleanup
- **69 tests** covering all CRUD operations, valid/invalid transitions, lock conflicts, timestamp handling, and referential integrity

See [Database Schema Reference](database-schema.md) for the complete repository API.
