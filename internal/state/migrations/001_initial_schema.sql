-- Axiom initial schema from Architecture Section 15.2

-- Projects: durable identity for a repository managed by Axiom
CREATE TABLE projects (
    id              TEXT PRIMARY KEY,
    root_path       TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Project runs: a single execution of Axiom against a project
CREATE TABLE project_runs (
    id                      TEXT PRIMARY KEY,
    project_id              TEXT NOT NULL REFERENCES projects(id),
    status                  TEXT NOT NULL,
    base_branch             TEXT NOT NULL,
    work_branch             TEXT NOT NULL,
    orchestrator_mode       TEXT NOT NULL,
    orchestrator_runtime    TEXT NOT NULL,
    orchestrator_identity   TEXT,
    srs_approval_delegate   TEXT NOT NULL,
    budget_max_usd          REAL NOT NULL,
    config_snapshot         TEXT NOT NULL,
    srs_hash                TEXT,
    started_at              DATETIME DEFAULT CURRENT_TIMESTAMP,
    paused_at               DATETIME,
    cancelled_at            DATETIME,
    completed_at            DATETIME
);

-- Interactive CLI/TUI sessions
CREATE TABLE ui_sessions (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects(id),
    run_id          TEXT REFERENCES project_runs(id),
    name            TEXT,
    mode            TEXT NOT NULL,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_active_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Transcript and UI cards for resumable terminal sessions
CREATE TABLE ui_messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      TEXT NOT NULL REFERENCES ui_sessions(id),
    seq             INTEGER NOT NULL,
    role            TEXT NOT NULL,
    kind            TEXT NOT NULL,
    content         TEXT NOT NULL,
    related_task_id TEXT,
    request_id      TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (session_id, seq)
);

-- Compacted summaries for long-lived sessions
CREATE TABLE ui_session_summaries (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      TEXT NOT NULL REFERENCES ui_sessions(id),
    summary_kind    TEXT NOT NULL,
    content         TEXT NOT NULL,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Per-project CLI input history
CREATE TABLE ui_input_history (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id      TEXT NOT NULL REFERENCES projects(id),
    session_id      TEXT REFERENCES ui_sessions(id),
    input_mode      TEXT NOT NULL,
    content         TEXT NOT NULL,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Tasks: durable identity and metadata
CREATE TABLE tasks (
    id              TEXT PRIMARY KEY,
    run_id          TEXT NOT NULL REFERENCES project_runs(id),
    parent_id       TEXT REFERENCES tasks(id),
    title           TEXT NOT NULL,
    description     TEXT,
    status          TEXT NOT NULL DEFAULT 'queued',
    tier            TEXT NOT NULL,
    task_type       TEXT NOT NULL DEFAULT 'implementation',
    base_snapshot   TEXT,
    eco_ref         INTEGER REFERENCES eco_log(id),
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at    DATETIME
);

-- Task to SRS requirement mapping
CREATE TABLE task_srs_refs (
    task_id     TEXT NOT NULL REFERENCES tasks(id),
    srs_ref     TEXT NOT NULL,
    PRIMARY KEY (task_id, srs_ref)
);

-- Task dependencies
CREATE TABLE task_dependencies (
    task_id    TEXT NOT NULL REFERENCES tasks(id),
    depends_on TEXT NOT NULL REFERENCES tasks(id),
    PRIMARY KEY (task_id, depends_on)
);

-- Task target files
CREATE TABLE task_target_files (
    task_id     TEXT NOT NULL REFERENCES tasks(id),
    file_path   TEXT NOT NULL,
    lock_scope  TEXT NOT NULL DEFAULT 'file',
    lock_resource_key TEXT NOT NULL,
    PRIMARY KEY (task_id, file_path)
);

-- Write-set locks
CREATE TABLE task_locks (
    resource_type TEXT NOT NULL,
    resource_key  TEXT NOT NULL,
    task_id       TEXT NOT NULL REFERENCES tasks(id),
    locked_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (resource_type, resource_key)
);

-- Tasks waiting on locks
CREATE TABLE task_lock_waits (
    task_id              TEXT PRIMARY KEY REFERENCES tasks(id),
    wait_reason          TEXT NOT NULL,
    requested_resources  TEXT NOT NULL,
    blocked_by_task_id   TEXT REFERENCES tasks(id),
    created_at           DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Individual execution attempts
CREATE TABLE task_attempts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id         TEXT NOT NULL REFERENCES tasks(id),
    attempt_number  INTEGER NOT NULL,
    model_id        TEXT NOT NULL,
    model_family    TEXT NOT NULL,
    base_snapshot   TEXT NOT NULL,
    status          TEXT NOT NULL,
    phase           TEXT NOT NULL DEFAULT 'executing',
    input_tokens    INTEGER,
    output_tokens   INTEGER,
    cost_usd        REAL DEFAULT 0,
    failure_reason  TEXT,
    feedback        TEXT,
    started_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at    DATETIME
);

-- Validation runs per attempt
CREATE TABLE validation_runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    attempt_id      INTEGER NOT NULL REFERENCES task_attempts(id),
    check_type      TEXT NOT NULL,
    status          TEXT NOT NULL,
    output          TEXT,
    duration_ms     INTEGER,
    timestamp       DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Review runs per attempt
CREATE TABLE review_runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    attempt_id      INTEGER NOT NULL REFERENCES task_attempts(id),
    reviewer_model  TEXT NOT NULL,
    reviewer_family TEXT NOT NULL,
    verdict         TEXT NOT NULL,
    feedback        TEXT,
    cost_usd        REAL DEFAULT 0,
    timestamp       DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Artifact tracking
CREATE TABLE task_artifacts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    attempt_id      INTEGER NOT NULL REFERENCES task_attempts(id),
    operation       TEXT NOT NULL,
    path_from       TEXT,
    path_to         TEXT,
    sha256_before   TEXT,
    sha256_after    TEXT,
    size_before     INTEGER,
    size_after      INTEGER,
    timestamp       DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Container sessions
CREATE TABLE container_sessions (
    id              TEXT PRIMARY KEY,
    run_id          TEXT NOT NULL REFERENCES project_runs(id),
    task_id         TEXT NOT NULL REFERENCES tasks(id),
    container_type  TEXT NOT NULL,
    image           TEXT NOT NULL,
    model_id        TEXT,
    cpu_limit       REAL,
    mem_limit       TEXT,
    started_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    stopped_at      DATETIME,
    exit_reason     TEXT
);

-- Event log
CREATE TABLE events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id          TEXT NOT NULL REFERENCES project_runs(id),
    event_type      TEXT NOT NULL,
    task_id         TEXT,
    agent_type      TEXT,
    agent_id        TEXT,
    details         TEXT,
    timestamp       DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Cost tracking
CREATE TABLE cost_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id          TEXT NOT NULL REFERENCES project_runs(id),
    task_id         TEXT REFERENCES tasks(id),
    attempt_id      INTEGER REFERENCES task_attempts(id),
    agent_type      TEXT NOT NULL,
    model_id        TEXT NOT NULL,
    input_tokens    INTEGER,
    output_tokens   INTEGER,
    cost_usd        REAL NOT NULL,
    timestamp       DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Engineering Change Orders
CREATE TABLE eco_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id          TEXT NOT NULL REFERENCES project_runs(id),
    eco_code        TEXT NOT NULL,
    category        TEXT NOT NULL,
    description     TEXT NOT NULL,
    affected_refs   TEXT NOT NULL,
    proposed_change TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'proposed',
    approved_by     TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    resolved_at     DATETIME
);

-- Indexes for common queries
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
