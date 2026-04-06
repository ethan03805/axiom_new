-- API authentication tokens for external orchestration (Section 24.3).
CREATE TABLE api_tokens (
    id          TEXT PRIMARY KEY,
    token_hash  TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    scope       TEXT NOT NULL DEFAULT 'full-control' CHECK (scope IN ('read-only', 'full-control')),
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at  DATETIME NOT NULL,
    revoked_at  DATETIME,
    last_used_at DATETIME
);

-- API audit log for all API requests (Section 24.3).
-- Separate from events table because events require a valid run_id.
CREATE TABLE api_audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    token_id    TEXT,
    method      TEXT NOT NULL,
    path        TEXT NOT NULL,
    status_code INTEGER NOT NULL,
    source_ip   TEXT,
    timestamp   DATETIME DEFAULT CURRENT_TIMESTAMP
);
