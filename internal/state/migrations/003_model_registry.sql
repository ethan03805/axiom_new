-- Phase 7: Model Registry and BitNet Operations
-- Per Architecture Section 18.3

CREATE TABLE model_registry (
    id                  TEXT PRIMARY KEY,
    family              TEXT NOT NULL,
    source              TEXT NOT NULL CHECK (source IN ('openrouter', 'bitnet', 'shipped')),
    tier                TEXT NOT NULL CHECK (tier IN ('local', 'cheap', 'standard', 'premium')),
    context_window      INTEGER NOT NULL DEFAULT 0,
    max_output          INTEGER NOT NULL DEFAULT 0,
    prompt_per_million  REAL NOT NULL DEFAULT 0.0,
    completion_per_million REAL NOT NULL DEFAULT 0.0,
    strengths           TEXT,     -- JSON array
    weaknesses          TEXT,     -- JSON array
    supports_tools      INTEGER NOT NULL DEFAULT 0,
    supports_vision     INTEGER NOT NULL DEFAULT 0,
    supports_grammar    INTEGER NOT NULL DEFAULT 0,
    recommended_for     TEXT,     -- JSON array
    not_recommended_for TEXT,     -- JSON array
    historical_success_rate REAL,
    avg_cost_per_task   REAL,
    last_updated        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_model_registry_tier ON model_registry(tier);
CREATE INDEX idx_model_registry_family ON model_registry(family);
CREATE INDEX idx_model_registry_source ON model_registry(source);
