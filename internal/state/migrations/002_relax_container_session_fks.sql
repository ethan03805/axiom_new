-- Phase 5: Relax container_sessions foreign key constraints.
-- Container lifecycle management (orphan cleanup, tracking) needs to work
-- independently of run/task context. run_id and task_id remain NOT NULL
-- but are no longer FK-constrained, allowing container tracking for
-- containers not yet associated with a specific run/task.

CREATE TABLE container_sessions_new (
    id              TEXT PRIMARY KEY,
    run_id          TEXT NOT NULL DEFAULT '',
    task_id         TEXT NOT NULL DEFAULT '',
    container_type  TEXT NOT NULL,
    image           TEXT NOT NULL,
    model_id        TEXT,
    cpu_limit       REAL,
    mem_limit       TEXT,
    started_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    stopped_at      DATETIME,
    exit_reason     TEXT
);

INSERT INTO container_sessions_new SELECT * FROM container_sessions;
DROP TABLE container_sessions;
ALTER TABLE container_sessions_new RENAME TO container_sessions;
