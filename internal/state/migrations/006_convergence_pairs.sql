-- Phase 13: Test-Generation Separation and Convergence Logic
-- Per Architecture Section 11.5: tracks implementation/test task pairs
-- and their convergence status.

CREATE TABLE convergence_pairs (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    impl_task_id      TEXT NOT NULL REFERENCES tasks(id),
    test_task_id      TEXT REFERENCES tasks(id),
    fix_task_id       TEXT REFERENCES tasks(id),
    status            TEXT NOT NULL DEFAULT 'pending',
    impl_model_family TEXT NOT NULL,
    iteration         INTEGER NOT NULL DEFAULT 1,
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    converged_at      DATETIME
);

CREATE INDEX idx_convergence_impl ON convergence_pairs(impl_task_id);
CREATE INDEX idx_convergence_test ON convergence_pairs(test_task_id);
CREATE INDEX idx_convergence_status ON convergence_pairs(status);
