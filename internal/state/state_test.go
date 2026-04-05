package state

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestOpenAndMigrate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	// Verify tables exist by querying them
	tables := []string{
		"projects", "project_runs", "tasks", "task_attempts",
		"events", "cost_log", "eco_log", "container_sessions",
		"validation_runs", "review_runs", "task_artifacts",
		"task_locks", "task_lock_waits", "task_dependencies",
		"task_target_files", "task_srs_refs",
		"ui_sessions", "ui_messages", "ui_session_summaries", "ui_input_history",
	}
	for _, table := range tables {
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
		if err != nil {
			t.Errorf("table %s should exist: %v", table, err)
		}
	}
}

func TestMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Run migrate twice — should be idempotent
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
}

func TestReopenExistingDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create and populate
	db1, err := Open(dbPath, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := db1.Migrate(); err != nil {
		t.Fatal(err)
	}

	// Insert a project
	_, err = db1.Exec(`INSERT INTO projects (id, root_path, name, slug) VALUES (?, ?, ?, ?)`,
		"proj-1", "/tmp/test", "test-project", "test-project")
	if err != nil {
		t.Fatal(err)
	}
	db1.Close()

	// Reopen and verify
	db2, err := Open(dbPath, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	if err := db2.Migrate(); err != nil {
		t.Fatal(err)
	}

	var name string
	err = db2.QueryRow("SELECT name FROM projects WHERE id = ?", "proj-1").Scan(&name)
	if err != nil {
		t.Fatal(err)
	}
	if name != "test-project" {
		t.Errorf("expected test-project, got %s", name)
	}
}

func TestForeignKeys(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	// Inserting a run with nonexistent project_id should fail
	_, err = db.Exec(`INSERT INTO project_runs
		(id, project_id, status, base_branch, work_branch, orchestrator_mode, orchestrator_runtime, srs_approval_delegate, budget_max_usd, config_snapshot)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"run-1", "nonexistent", "active", "main", "axiom/test", "embedded", "claw", "user", 10.0, "{}")
	if err == nil {
		t.Error("expected foreign key violation")
	}
}

func TestWALMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var mode string
	err = db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("expected WAL mode, got %s", mode)
	}
}
