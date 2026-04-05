package state

import (
	"path/filepath"
	"testing"
)

// testDB creates a fresh, migrated database in a temp directory.
// The database is automatically closed when the test completes.
func testDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedProject inserts a project and returns its ID.
func seedProject(t *testing.T, db *DB) string {
	t.Helper()
	id := "proj-test"
	_, err := db.Exec(`INSERT INTO projects (id, root_path, name, slug) VALUES (?, ?, ?, ?)`,
		id, "/tmp/test-project", "test-project", "test-project")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// seedRun inserts a project run and returns its ID. Requires a project.
func seedRun(t *testing.T, db *DB, projectID string) string {
	t.Helper()
	id := "run-test"
	_, err := db.Exec(`INSERT INTO project_runs
		(id, project_id, status, base_branch, work_branch, orchestrator_mode,
		 orchestrator_runtime, srs_approval_delegate, budget_max_usd, config_snapshot)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, projectID, string(RunActive), "main", "axiom/test-project",
		"embedded", "claw", "user", 10.0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// seedTask inserts a task and returns its ID. Requires a run.
func seedTask(t *testing.T, db *DB, runID string) string {
	t.Helper()
	id := "task-test"
	_, err := db.Exec(`INSERT INTO tasks (id, run_id, title, status, tier, task_type) VALUES (?, ?, ?, ?, ?, ?)`,
		id, runID, "test task", string(TaskQueued), string(TierStandard), string(TaskTypeImplementation))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// seedAttempt inserts an attempt and returns its ID. Requires a task.
func seedAttempt(t *testing.T, db *DB, taskID string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO task_attempts
		(task_id, attempt_number, model_id, model_family, base_snapshot, status, phase)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		taskID, 1, "anthropic/claude-4-sonnet", "anthropic", "abc123", string(AttemptRunning), string(PhaseExecuting))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}
