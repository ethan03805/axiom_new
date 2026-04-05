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
| `status` | TEXT | NOT NULL DEFAULT 'queued' | queued, in_progress, completed, failed, blocked, waiting_on_lock, cancelled_eco |
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
Active and historical container metadata.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | Container name (axiom-<task-id>-<timestamp>) |
| `run_id` | TEXT | FK → project_runs | |
| `task_id` | TEXT | FK → tasks | |
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
```

## Repository API

All database access goes through typed repository methods on the `state.DB` struct. These provide CRUD operations, status transition enforcement, and transactional safety.

### Domain Models

Every table has a corresponding Go struct in `internal/state/models.go`. Typed string enums enforce valid values at the Go level:

| Type | Values |
|------|--------|
| `RunStatus` | `draft_srs`, `awaiting_srs_approval`, `active`, `paused`, `cancelled`, `completed`, `error` |
| `TaskStatus` | `queued`, `in_progress`, `waiting_on_lock`, `completed`, `failed`, `blocked`, `cancelled_eco` |
| `AttemptStatus` | `running`, `passed`, `failed`, `escalated` |
| `AttemptPhase` | `executing`, `validating`, `reviewing`, `awaiting_orchestrator_gate`, `queued_for_merge`, `merging`, `succeeded`, `failed`, `escalated` |
| `ECOStatus` | `proposed`, `approved`, `rejected` |
| `TaskTier` | `local`, `cheap`, `standard`, `premium` |
| `TaskType` | `implementation`, `test`, `review` |
| `ContainerType` | `meeseeks`, `reviewer`, `validator`, `sub_orchestrator` |
| `SessionMode` | `bootstrap`, `approval`, `execution`, `postrun` |

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
in_progress → completed | failed | blocked | cancelled_eco
```

**Attempt transitions:** `running → passed | failed | escalated`

**Phase transitions:** `executing → validating → reviewing → awaiting_orchestrator_gate → queued_for_merge → merging → succeeded` (with `failed` or `escalated` reachable from any non-terminal phase)

**ECO transitions:** `proposed → approved | rejected`

### Repository Methods by Domain

**Projects** (`projects.go`):
`CreateProject`, `GetProject`, `GetProjectByRootPath`, `ListProjects`

**Runs** (`runs.go`):
`CreateRun`, `GetRun`, `GetActiveRun`, `ListRunsByProject`, `UpdateRunStatus`

**Tasks** (`tasks.go`):
`CreateTask`, `GetTask`, `ListTasksByRun`, `ListTasksByStatus`, `UpdateTaskStatus`, `AddTaskDependency`, `GetTaskDependencies`, `AddTaskSRSRef`, `GetTaskSRSRefs`, `AddTaskTargetFile`, `GetTaskTargetFiles`, `AcquireLock`, `ReleaseLock`, `ReleaseTaskLocks`, `GetTaskLocks`, `AddLockWait`, `RemoveLockWait`, `ListLockWaits`

**Attempts** (`attempts.go`):
`CreateAttempt`, `GetAttempt`, `ListAttemptsByTask`, `UpdateAttemptStatus`, `UpdateAttemptPhase`, `CreateValidationRun`, `ListValidationRuns`, `CreateReviewRun`, `ListReviewRuns`, `CreateArtifact`, `ListArtifacts`

**Sessions** (`sessions.go`):
`CreateSession`, `GetSession`, `ListSessionsByProject`, `UpdateSessionActivity`, `AddMessage`, `GetMessages`, `AddSessionSummary`, `AddInputHistory`

**Events & Costs** (`events.go`):
`CreateEvent`, `ListEventsByRun`, `ListEventsByType`, `CreateCostLog`, `ListCostLogByRun`, `TotalCostByRun`

**ECOs** (`eco.go`):
`CreateECO`, `GetECO`, `ListECOsByRun`, `UpdateECOStatus`

**Containers** (`containers.go`):
`CreateContainerSession`, `GetContainerSession`, `ListActiveContainers`, `ListContainersByRun`, `MarkContainerStopped`

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
project_runs 1──* container_sessions
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
```
