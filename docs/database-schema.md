# Database Schema Reference

Axiom uses SQLite in WAL mode as its authoritative state store. The database lives at `.axiom/axiom.db` and is gitignored (machine-specific runtime state).

## Database Configuration

| Setting | Value | Rationale |
|---------|-------|-----------|
| Journal mode | WAL | Concurrent reads during writes |
| Busy timeout | 5000ms | Retry on lock contention |
| Foreign keys | ON | Referential integrity |
| Max connections | 10 | Connection pooling |

## Migration System

Migrations are embedded SQL files applied in lexicographic order. The `schema_migrations` table tracks which migrations have been applied.

```sql
CREATE TABLE schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

Current migrations:
- `001_initial_schema.sql` — full schema from Architecture Section 15.2
- `002_relax_container_session_fks.sql` — relaxes FK constraints on `container_sessions` (Phase 5)
- `003_model_registry.sql` — adds `model_registry` table for model catalog (Phase 7)
- `004_semantic_index.sql` — adds semantic index tables for symbol/export/dependency tracking (Phase 8)
- `005_attempt_tier.sql` — adds `tier` column to `task_attempts` for per-tier retry counting (Phase 10)
- `006_convergence_pairs.sql` — adds `convergence_pairs` table for test-generation separation and convergence tracking (Phase 13)

## Table Reference

### Core Tables

#### `projects`
Durable identity for a repository managed by Axiom.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | Unique project ID |
| `root_path` | TEXT | NOT NULL UNIQUE | Absolute path to project root |
| `name` | TEXT | NOT NULL | Display name |
| `slug` | TEXT | NOT NULL | URL-safe identifier |
| `created_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | Creation time |

#### `project_runs`
A single execution of Axiom against a project.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | Unique run ID |
| `project_id` | TEXT | FK → projects | Parent project |
| `status` | TEXT | NOT NULL | draft_srs, awaiting_srs_approval, active, paused, cancelled, completed, error |
| `base_branch` | TEXT | NOT NULL | Git branch execution started from |
| `work_branch` | TEXT | NOT NULL | Axiom work branch (axiom/<slug>) |
| `orchestrator_mode` | TEXT | NOT NULL | embedded or external |
| `orchestrator_runtime` | TEXT | NOT NULL | claw, claude-code, codex, opencode |
| `orchestrator_identity` | TEXT | | Orchestrator identifier |
| `srs_approval_delegate` | TEXT | NOT NULL | user or claw |
| `budget_max_usd` | REAL | NOT NULL | Budget ceiling for this run |
| `config_snapshot` | TEXT | NOT NULL | Serialized config at run start |
| `srs_hash` | TEXT | | SHA-256 of approved SRS |
| `started_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |
| `paused_at` | DATETIME | | |
| `cancelled_at` | DATETIME | | |
| `completed_at` | DATETIME | | |

### Task System

#### `tasks`
Task tree nodes with status tracking.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | Unique task ID |
| `run_id` | TEXT | FK → project_runs | Parent run |
| `parent_id` | TEXT | FK → tasks | Parent task (for tree structure) |
| `title` | TEXT | NOT NULL | Task title |
| `description` | TEXT | | Detailed description |
| `status` | TEXT | NOT NULL DEFAULT 'queued' | queued, in_progress, done, failed, blocked, waiting_on_lock, cancelled_eco |
| `tier` | TEXT | NOT NULL | local, cheap, standard, premium |
| `task_type` | TEXT | NOT NULL DEFAULT 'implementation' | implementation, test, review |
| `base_snapshot` | TEXT | | Git SHA this task was planned against |
| `eco_ref` | INTEGER | FK → eco_log | ECO that cancelled this task |
| `created_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |
| `completed_at` | DATETIME | | |

#### `task_dependencies`
Directed dependency edges between tasks.

| Column | Type | Constraints |
|--------|------|-------------|
| `task_id` | TEXT | FK → tasks, PK |
| `depends_on` | TEXT | FK → tasks, PK |

#### `task_target_files`
Declared file targets and lock scope per task.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `task_id` | TEXT | FK → tasks, PK | |
| `file_path` | TEXT | PK | Target file path |
| `lock_scope` | TEXT | NOT NULL DEFAULT 'file' | file, package, module, schema |
| `lock_resource_key` | TEXT | NOT NULL | Canonical lock key |

#### `task_srs_refs`
Maps tasks to SRS requirements.

| Column | Type | Constraints |
|--------|------|-------------|
| `task_id` | TEXT | FK → tasks, PK |
| `srs_ref` | TEXT | PK (e.g., "FR-001") |

### Execution

#### `task_attempts`
Individual execution attempts preserving retry history.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `task_id` | TEXT | FK → tasks | |
| `attempt_number` | INTEGER | NOT NULL | Attempt sequence number |
| `model_id` | TEXT | NOT NULL | Model used for this attempt |
| `model_family` | TEXT | NOT NULL | anthropic, openai, meta, local |
| `tier` | TEXT | NOT NULL DEFAULT 'standard' | Model tier at dispatch time (local, cheap, standard, premium). Used for per-tier retry counting. Added in migration 005. |
| `base_snapshot` | TEXT | NOT NULL | Git SHA for this attempt |
| `status` | TEXT | NOT NULL | running, passed, failed, escalated |
| `phase` | TEXT | NOT NULL DEFAULT 'executing' | executing, validating, reviewing, awaiting_orchestrator_gate, queued_for_merge, merging, succeeded, failed, escalated |
| `input_tokens` | INTEGER | | |
| `output_tokens` | INTEGER | | |
| `cost_usd` | REAL | DEFAULT 0 | |
| `failure_reason` | TEXT | | |
| `feedback` | TEXT | | Feedback for retry |
| `started_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |
| `completed_at` | DATETIME | | |

#### `validation_runs`
Validation check results per attempt.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `attempt_id` | INTEGER | FK → task_attempts | |
| `check_type` | TEXT | NOT NULL | compile, lint, test, security |
| `status` | TEXT | NOT NULL | pass, fail, skip |
| `output` | TEXT | | Error output if failed |
| `duration_ms` | INTEGER | | |
| `timestamp` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |

#### `review_runs`
Reviewer verdicts per attempt.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `attempt_id` | INTEGER | FK → task_attempts | |
| `reviewer_model` | TEXT | NOT NULL | |
| `reviewer_family` | TEXT | NOT NULL | |
| `verdict` | TEXT | NOT NULL | approve or reject |
| `feedback` | TEXT | | |
| `cost_usd` | REAL | DEFAULT 0 | |
| `timestamp` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |

### Concurrency

#### `task_locks`
Active write-set locks preventing concurrent modification.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `resource_type` | TEXT | PK | file, package, module, schema |
| `resource_key` | TEXT | PK | Canonical identifier |
| `task_id` | TEXT | FK → tasks | Lock holder |
| `locked_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |

#### `task_lock_waits`
Tasks blocked waiting for lock acquisition.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `task_id` | TEXT | PK, FK → tasks | |
| `wait_reason` | TEXT | NOT NULL | initial_dispatch or scope_expansion |
| `requested_resources` | TEXT | NOT NULL | JSON array of {resource_type, resource_key} |
| `blocked_by_task_id` | TEXT | FK → tasks | |
| `created_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |

### Audit & Observability

#### `events`
Full audit trail of all system activity.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `run_id` | TEXT | FK → project_runs | |
| `event_type` | TEXT | NOT NULL | |
| `task_id` | TEXT | | Related task |
| `agent_type` | TEXT | | orchestrator, sub_orchestrator, meeseeks, reviewer, engine |
| `agent_id` | TEXT | | |
| `details` | TEXT | | JSON payload |
| `timestamp` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |

#### `cost_log`
Inference cost tracking per request.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `run_id` | TEXT | FK → project_runs | |
| `task_id` | TEXT | FK → tasks | |
| `attempt_id` | INTEGER | FK → task_attempts | |
| `agent_type` | TEXT | NOT NULL | |
| `model_id` | TEXT | NOT NULL | |
| `input_tokens` | INTEGER | | |
| `output_tokens` | INTEGER | | |
| `cost_usd` | REAL | NOT NULL | |
| `timestamp` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |

#### `eco_log`
Engineering Change Order records.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `run_id` | TEXT | FK → project_runs | |
| `eco_code` | TEXT | NOT NULL | ECO-DEP, ECO-API, etc. |
| `category` | TEXT | NOT NULL | |
| `description` | TEXT | NOT NULL | |
| `affected_refs` | TEXT | NOT NULL | JSON array of SRS refs |
| `proposed_change` | TEXT | NOT NULL | |
| `status` | TEXT | NOT NULL DEFAULT 'proposed' | proposed, approved, rejected |
| `approved_by` | TEXT | | "user" or "claw:<identity>" |
| `created_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |
| `resolved_at` | DATETIME | | |

### Containers

#### `container_sessions`
Active and historical container metadata. FK constraints on `run_id` and `task_id` were relaxed in migration 002 so container lifecycle management (orphan cleanup, tracking) works independently of run/task context.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | Container name (axiom-<task-id>-<timestamp>-<seq>) |
| `run_id` | TEXT | NOT NULL DEFAULT '' | Associated run (no FK constraint) |
| `task_id` | TEXT | NOT NULL DEFAULT '' | Associated task (no FK constraint) |
| `container_type` | TEXT | NOT NULL | meeseeks, reviewer, validator, sub_orchestrator |
| `image` | TEXT | NOT NULL | Docker image used |
| `model_id` | TEXT | | Model assigned to this container |
| `cpu_limit` | REAL | | |
| `mem_limit` | TEXT | | |
| `started_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |
| `stopped_at` | DATETIME | | |
| `exit_reason` | TEXT | | completed, timeout, killed, error |

#### `task_artifacts`
File artifact tracking with content hashes.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `attempt_id` | INTEGER | FK ��� task_attempts | |
| `operation` | TEXT | NOT NULL | add, modify, delete, rename |
| `path_from` | TEXT | | Source path (for rename/modify) |
| `path_to` | TEXT | | Destination path |
| `sha256_before` | TEXT | | Content hash before change |
| `sha256_after` | TEXT | | Content hash after change |
| `size_before` | INTEGER | | File size before |
| `size_after` | INTEGER | | File size after |
| `timestamp` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |

### UI Sessions

#### `ui_sessions`
Interactive CLI/TUI session tracking.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | |
| `project_id` | TEXT | FK → projects | |
| `run_id` | TEXT | FK → project_runs | |
| `name` | TEXT | | Optional session name |
| `mode` | TEXT | NOT NULL | bootstrap, approval, execution, postrun |
| `created_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |
| `last_active_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |

#### `ui_messages`
Transcript entries for resumable sessions.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `session_id` | TEXT | FK → ui_sessions | |
| `seq` | INTEGER | NOT NULL, UNIQUE(session_id, seq) | Sequence number |
| `role` | TEXT | NOT NULL | user, assistant, system |
| `kind` | TEXT | NOT NULL | user, assistant, system_card, event, tool, approval, ephemeral |
| `content` | TEXT | NOT NULL | |
| `related_task_id` | TEXT | | |
| `request_id` | TEXT | | |
| `created_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |

#### `ui_session_summaries`
Compacted summaries for long sessions.

| Column | Type | Constraints |
|--------|------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT |
| `session_id` | TEXT | FK → ui_sessions |
| `summary_kind` | TEXT | NOT NULL (transcript_compaction, run_handoff) |
| `content` | TEXT | NOT NULL |
| `created_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP |

#### `ui_input_history`
Per-project CLI input history.

| Column | Type | Constraints |
|--------|------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT |
| `project_id` | TEXT | FK → projects |
| `session_id` | TEXT | FK → ui_sessions |
| `input_mode` | TEXT | NOT NULL (prompt, command, shell) |
| `content` | TEXT | NOT NULL |
| `created_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP |

### Model Registry (Phase 7)

#### `model_registry`
Aggregated model catalog from OpenRouter, BitNet, and shipped capability data. Per Architecture Section 18.3.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | Model ID (e.g., "anthropic/claude-opus-4.6") |
| `family` | TEXT | NOT NULL | Provider family (e.g., "anthropic", "openai", "falcon") |
| `source` | TEXT | NOT NULL, CHECK | openrouter, bitnet, or shipped |
| `tier` | TEXT | NOT NULL, CHECK | local, cheap, standard, premium |
| `context_window` | INTEGER | NOT NULL DEFAULT 0 | Maximum context tokens |
| `max_output` | INTEGER | NOT NULL DEFAULT 0 | Maximum output tokens |
| `prompt_per_million` | REAL | NOT NULL DEFAULT 0 | USD per million prompt tokens |
| `completion_per_million` | REAL | NOT NULL DEFAULT 0 | USD per million completion tokens |
| `strengths` | TEXT | | JSON array of capability tags |
| `weaknesses` | TEXT | | JSON array of limitation tags |
| `supports_tools` | INTEGER | NOT NULL DEFAULT 0 | Boolean: supports tool calling |
| `supports_vision` | INTEGER | NOT NULL DEFAULT 0 | Boolean: supports image input |
| `supports_grammar` | INTEGER | NOT NULL DEFAULT 0 | Boolean: supports GBNF grammar constraints |
| `recommended_for` | TEXT | | JSON array of recommended task types |
| `not_recommended_for` | TEXT | | JSON array of not-recommended task types |
| `historical_success_rate` | REAL | | 0.0-1.0, updated after project completion |
| `avg_cost_per_task` | REAL | | Average cost in USD |
| `last_updated` | DATETIME | NOT NULL DEFAULT CURRENT_TIMESTAMP | Last refresh time |

### Semantic Index (Phase 8)

#### `index_files`
Tracked source files with content hashes for incremental reindexing. Per Architecture Section 17.3.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `path` | TEXT | NOT NULL UNIQUE | Relative path from project root |
| `language` | TEXT | NOT NULL | go, typescript, javascript, python, rust |
| `hash` | TEXT | NOT NULL | SHA-256 of file content |
| `indexed_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | Last indexing time |

#### `index_symbols`
Symbols: functions, types, interfaces, constants, variables, fields, methods.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `file_id` | INTEGER | FK → index_files ON DELETE CASCADE | |
| `name` | TEXT | NOT NULL | Symbol name |
| `kind` | TEXT | NOT NULL, CHECK | function, type, interface, constant, variable, field, method |
| `line` | INTEGER | NOT NULL | Source line number |
| `signature` | TEXT | | Function signature |
| `return_type` | TEXT | | Return type for functions |
| `exported` | INTEGER | NOT NULL DEFAULT 0 | Boolean: publicly exported |
| `parent_symbol_id` | INTEGER | FK → index_symbols ON DELETE CASCADE | For methods/fields of a type |

#### `index_imports`
Import declarations per file.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `file_id` | INTEGER | FK → index_files ON DELETE CASCADE | |
| `import_path` | TEXT | NOT NULL | Imported package/module path |
| `alias` | TEXT | | Import alias if any |

#### `index_references`
Symbol references for reverse-dependency queries.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `file_id` | INTEGER | FK → index_files ON DELETE CASCADE | |
| `symbol_name` | TEXT | NOT NULL | Referenced symbol name |
| `line` | INTEGER | NOT NULL | Source line of reference |
| `usage_type` | TEXT | NOT NULL, CHECK | call, reference, implementation |

#### `index_packages`
Package/module identity for dependency graph.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `path` | TEXT | NOT NULL UNIQUE | Package/module path |
| `dir` | TEXT | NOT NULL | Directory on disk |

#### `index_package_deps`
Package dependency edges.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `package_id` | INTEGER | FK → index_packages ON DELETE CASCADE, PK | |
| `depends_on_id` | INTEGER | FK → index_packages ON DELETE CASCADE, PK | |

## Indexes

```sql
CREATE INDEX idx_project_runs_project ON project_runs(project_id);
CREATE INDEX idx_project_runs_status ON project_runs(status);
CREATE INDEX idx_tasks_run ON tasks(run_id);
CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_parent ON tasks(parent_id);
CREATE INDEX idx_task_attempts_task ON task_attempts(task_id);
CREATE INDEX idx_events_run ON events(run_id);
CREATE INDEX idx_events_type ON events(event_type);
CREATE INDEX idx_cost_log_run ON cost_log(run_id);
CREATE INDEX idx_ui_sessions_project ON ui_sessions(project_id);
CREATE INDEX idx_ui_messages_session ON ui_messages(session_id);
CREATE INDEX idx_model_registry_tier ON model_registry(tier);
CREATE INDEX idx_model_registry_family ON model_registry(family);
CREATE INDEX idx_model_registry_source ON model_registry(source);
CREATE INDEX idx_index_symbols_name ON index_symbols(name);
CREATE INDEX idx_index_symbols_kind ON index_symbols(kind);
CREATE INDEX idx_index_symbols_file ON index_symbols(file_id);
CREATE INDEX idx_index_symbols_parent ON index_symbols(parent_symbol_id);
CREATE INDEX idx_index_imports_file ON index_imports(file_id);
CREATE INDEX idx_index_imports_path ON index_imports(import_path);
CREATE INDEX idx_index_refs_symbol ON index_references(symbol_name);
CREATE INDEX idx_index_refs_file ON index_references(file_id);
CREATE INDEX idx_index_files_path ON index_files(path);
CREATE INDEX idx_index_packages_path ON index_packages(path);
```

### Convergence Tracking (Phase 13)

#### `convergence_pairs`
Links implementation tasks to their test-generation and fix tasks for convergence tracking. Per Architecture Section 11.5: completion criteria require both the implementation and its generated tests to converge.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | |
| `impl_task_id` | TEXT | NOT NULL, FK → tasks | Implementation task |
| `test_task_id` | TEXT | FK → tasks | Test-generation task |
| `fix_task_id` | TEXT | FK → tasks | Implementation-fix task (after test failure) |
| `status` | TEXT | NOT NULL DEFAULT 'pending' | pending, testing, fixing, converged, blocked |
| `impl_model_family` | TEXT | NOT NULL | Model family used for implementation (excluded for test task) |
| `iteration` | INTEGER | NOT NULL DEFAULT 1 | Fix loop iteration count |
| `created_at` | DATETIME | DEFAULT CURRENT_TIMESTAMP | |
| `converged_at` | DATETIME | | Timestamp when convergence was achieved |

**Indexes:**
```sql
CREATE INDEX idx_convergence_impl ON convergence_pairs(impl_task_id);
CREATE INDEX idx_convergence_test ON convergence_pairs(test_task_id);
CREATE INDEX idx_convergence_status ON convergence_pairs(status);
```

**Status lifecycle:**
```
pending → testing       (test task created)
testing → fixing        (test task failed, fix task created)
testing → converged     (test task passed)
fixing  → converged     (fix merged and tests pass)
fixing  → blocked       (fix retries exhausted)
pending → blocked       (unable to create test task)
```

## Repository API

All database access goes through typed repository methods on the `state.DB` struct. These provide CRUD operations, status transition enforcement, and transactional safety.

### Domain Models

Every table has a corresponding Go struct in `internal/state/models.go`. Typed string enums enforce valid values at the Go level:

| Type | Values |
|------|--------|
| `RunStatus` | `draft_srs`, `awaiting_srs_approval`, `active`, `paused`, `cancelled`, `completed`, `error` |
| `TaskStatus` | `queued`, `in_progress`, `waiting_on_lock`, `done`, `failed`, `blocked`, `cancelled_eco` |
| `AttemptStatus` | `running`, `passed`, `failed`, `escalated` |
| `AttemptPhase` | `executing`, `validating`, `reviewing`, `awaiting_orchestrator_gate`, `queued_for_merge`, `merging`, `succeeded`, `failed`, `escalated` |
| `ECOStatus` | `proposed`, `approved`, `rejected` |
| `TaskTier` | `local`, `cheap`, `standard`, `premium` |
| `TaskType` | `implementation`, `test`, `review` |
| `ContainerType` | `meeseeks`, `reviewer`, `validator`, `sub_orchestrator` |
| `SessionMode` | `bootstrap`, `approval`, `execution`, `postrun` |
| `SymbolKind` | `function`, `type`, `interface`, `constant`, `variable`, `field`, `method` |
| `UsageType` | `call`, `reference`, `implementation` |
| `ConvergenceStatus` | `pending`, `testing`, `fixing`, `converged`, `blocked` |

### Status Transition Invariants

Status update methods use `WithTx` (transactional read-then-write) to enforce valid transitions. Invalid transitions return `ErrInvalidTransition`.

**Run transitions:**
```
draft_srs → awaiting_srs_approval
awaiting_srs_approval → active | draft_srs
active → paused | cancelled | completed | error
paused → active | cancelled
```

**Task transitions:**
```
queued → in_progress | waiting_on_lock | cancelled_eco
waiting_on_lock → in_progress | queued | cancelled_eco
in_progress → done | failed | blocked | cancelled_eco
failed → queued   (retry or escalation per Section 15.4)
```

**Attempt transitions:** `running → passed | failed | escalated`

**Phase transitions:** `executing → validating → reviewing → awaiting_orchestrator_gate → queued_for_merge → merging → succeeded` (with `failed` or `escalated` reachable from any non-terminal phase)

**ECO transitions:** `proposed → approved | rejected`

### Repository Methods by Domain

**Projects** (`projects.go`):
`CreateProject`, `GetProject`, `GetProjectByRootPath`, `ListProjects`

**Runs** (`runs.go`):
`CreateRun`, `GetRun`, `GetActiveRun`, `GetLatestRunByProject`, `ListRunsByProject`, `UpdateRunStatus`, `UpdateRunSRSHash`

**Tasks** (`tasks.go`):
`CreateTask`, `GetTask`, `ListTasksByRun`, `ListTasksByStatus`, `UpdateTaskStatus`, `AddTaskDependency`, `GetTaskDependencies`, `AddTaskSRSRef`, `GetTaskSRSRefs`, `AddTaskTargetFile`, `GetTaskTargetFiles`, `AcquireLock`, `ReleaseLock`, `ReleaseTaskLocks`, `GetTaskLocks`, `AddLockWait`, `RemoveLockWait`, `ListLockWaits`

**Attempts** (`attempts.go`):
`CreateAttempt`, `GetAttempt`, `ListAttemptsByTask`, `UpdateAttemptStatus`, `UpdateAttemptPhase`, `CreateValidationRun`, `ListValidationRuns`, `CreateReviewRun`, `ListReviewRuns`, `CreateArtifact`, `ListArtifacts`

**Sessions** (`sessions.go`):
`CreateSession`, `GetSession`, `ListSessionsByProject`, `GetLatestSessionByProject`, `UpdateSessionActivity`, `UpdateSessionMode`, `UpdateSessionRunID`, `AddMessage`, `GetMessages`, `GetMessageCount`, `GetMaxSeqBySession`, `DeleteMessagesBySessionBefore`, `AddSessionSummary`, `GetSessionSummaries`, `AddInputHistory`, `GetInputHistoryByProject`

**Events & Costs** (`events.go`):
`CreateEvent`, `ListEventsByRun`, `ListEventsByType`, `CreateCostLog`, `ListCostLogByRun`, `TotalCostByRun`

**ECOs** (`eco.go`):
`CreateECO`, `GetECO`, `ListECOsByRun`, `UpdateECOStatus`

**Containers** (`containers.go`):
`CreateContainerSession`, `GetContainerSession`, `ListActiveContainers`, `ListContainersByRun`, `MarkContainerStopped`

**Model Registry** (`model_registry.go`):
`UpsertModel`, `GetModel`, `ListModels`, `ListModelsByTier`, `ListModelsByFamily`, `ListModelsByTierAndFamily`, `DeleteModel`, `DeleteModelsBySource`, `ModelCountByTier`, `UpdateModelPerformance`

**Semantic Index** (`index.go`):
`CreateIndexFile`, `GetIndexFile`, `DeleteIndexFile`, `UpdateIndexFileHash`, `ListIndexFiles`, `ClearIndex`, `CreateIndexSymbol`, `ListSymbolsByFile`, `LookupSymbol`, `ListExportedSymbolsByPackageDir`, `FindImplementations`, `CreateIndexImport`, `ListImportsByFile`, `ListImporterFiles`, `CreateIndexReference`, `ListReferencesBySymbol`, `CreateIndexPackage`, `GetIndexPackage`, `AddPackageDep`, `ListPackageDeps`

**Convergence** (`convergence.go`):
`CreateConvergencePair`, `GetConvergencePair`, `GetConvergencePairByImplTask`, `GetConvergencePairByTestTask`, `UpdateConvergencePairStatus`, `SetConvergenceTestTask`, `SetConvergenceFixTask`, `IncrementConvergenceIteration`, `ListConvergencePairsByRun`

### Sentinel Errors

| Error | Meaning |
|-------|---------|
| `ErrNotFound` | No row matched the query |
| `ErrInvalidTransition` | Status/phase transition violates the state machine |
| `ErrLockConflict` | Resource is already locked by a different task |

## Entity Relationships

```
projects 1──* project_runs
project_runs 1──* tasks
project_runs 1──* events
project_runs 1──* cost_log
project_runs 1──* eco_log
project_runs 1──* container_sessions  (logical, no FK constraint)
tasks 1──* task_attempts
tasks *──* task_dependencies (self-referencing)
tasks 1──* task_target_files
tasks 1──* task_srs_refs
tasks 0..1──1 task_lock_waits
task_attempts 1──* validation_runs
task_attempts 1──* review_runs
task_attempts 1──* task_artifacts
projects 1──* ui_sessions
ui_sessions 1──* ui_messages
ui_sessions 1──* ui_session_summaries
projects 1──* ui_input_history
model_registry            (standalone — no FK relationships)
index_files 1──* index_symbols
index_files 1──* index_imports
index_files 1──* index_references
index_symbols 0..1──* index_symbols (parent_symbol_id self-reference)
index_packages *──* index_package_deps (self-referencing)
tasks 1──0..1 convergence_pairs (impl_task_id)
tasks 1──0..1 convergence_pairs (test_task_id)
tasks 1──0..1 convergence_pairs (fix_task_id)
```
