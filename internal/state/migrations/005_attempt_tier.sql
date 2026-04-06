-- Phase 10: Add tier column to task_attempts so per-tier retry counting is accurate.
-- Per Architecture Section 30.1: max 3 retries at the same model tier.
ALTER TABLE task_attempts ADD COLUMN tier TEXT NOT NULL DEFAULT 'standard';
