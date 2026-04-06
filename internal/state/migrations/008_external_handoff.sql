-- Migration 008: Add external orchestrator handoff fields to project_runs.
-- Per Issue 01 (P0): axiom run must persist the user prompt and start source
-- so an external orchestrator can retrieve and continue the run.

ALTER TABLE project_runs ADD COLUMN initial_prompt TEXT NOT NULL DEFAULT '';
ALTER TABLE project_runs ADD COLUMN start_source TEXT NOT NULL DEFAULT 'cli';
