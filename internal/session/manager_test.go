package session

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func testDB(t *testing.T) *state.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := state.Open(dbPath, testLogger())
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

func seedProject(t *testing.T, db *state.DB) string {
	t.Helper()
	id := "proj-test"
	if err := db.CreateProject(&state.Project{
		ID: id, RootPath: "/tmp/test", Name: "test-project", Slug: "test-project",
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func testEngine(t *testing.T, db *state.DB) *engine.Engine {
	t.Helper()
	cfg := config.Default("test-project", "test-project")
	eng, err := engine.New(engine.Options{
		Config:  &cfg,
		DB:      db,
		RootDir: t.TempDir(),
		Log:     testLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Stop() })
	return eng
}

func testManager(t *testing.T) (*Manager, *state.DB, string) {
	t.Helper()
	db := testDB(t)
	projID := seedProject(t, db)
	eng := testEngine(t, db)
	cfg := config.Default("test-project", "test-project")
	m := New(eng, &cfg, testLogger())
	return m, db, projID
}

// --- Manager creation ---

func TestNewManager(t *testing.T) {
	db := testDB(t)
	_ = seedProject(t, db)
	eng := testEngine(t, db)
	cfg := config.Default("test-project", "test-project")

	m := New(eng, &cfg, testLogger())
	if m == nil {
		t.Fatal("New returned nil")
	}
}

// --- Session create/resume ---

func TestCreateSession(t *testing.T) {
	m, db, projID := testManager(t)

	sess, err := m.CreateSession(projID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.ID == "" {
		t.Error("expected non-empty session ID")
	}
	if sess.ProjectID != projID {
		t.Errorf("ProjectID = %q, want %q", sess.ProjectID, projID)
	}
	if sess.Mode != state.SessionBootstrap {
		t.Errorf("Mode = %q, want %q", sess.Mode, state.SessionBootstrap)
	}

	// Verify persisted
	got, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ProjectID != projID {
		t.Errorf("persisted ProjectID = %q", got.ProjectID)
	}
}

func TestCreateSessionWithActiveRun(t *testing.T) {
	m, db, projID := testManager(t)

	// Create an active run
	run, err := m.engine.CreateRun(engine.RunOptions{
		ProjectID:  projID,
		BaseBranch: "main",
		BudgetUSD:  10.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Transition to active
	if err := db.UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}

	sess, err := m.CreateSession(projID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.RunID == nil || *sess.RunID != run.ID {
		t.Errorf("RunID = %v, want %q", sess.RunID, run.ID)
	}
	if sess.Mode != state.SessionExecution {
		t.Errorf("Mode = %q, want %q", sess.Mode, state.SessionExecution)
	}
}

func TestResumeSession(t *testing.T) {
	m, _, projID := testManager(t)

	created, err := m.CreateSession(projID)
	if err != nil {
		t.Fatal(err)
	}

	resumed, err := m.ResumeSession(created.ID)
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if resumed.ID != created.ID {
		t.Errorf("resumed ID = %q, want %q", resumed.ID, created.ID)
	}
}

func TestResumeSessionNotFound(t *testing.T) {
	m, _, _ := testManager(t)

	_, err := m.ResumeSession("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestResumeOrCreateSession(t *testing.T) {
	m, _, projID := testManager(t)

	// With no existing session, should create a new one
	sess, err := m.ResumeOrCreateSession(projID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" {
		t.Error("expected non-empty session ID")
	}

	// Second call should resume the same session
	sess2, err := m.ResumeOrCreateSession(projID)
	if err != nil {
		t.Fatal(err)
	}
	if sess2.ID != sess.ID {
		t.Errorf("expected same session %q, got %q", sess.ID, sess2.ID)
	}
}

// --- Mode determination ---

func TestDetermineMode_NoRun(t *testing.T) {
	m, _, projID := testManager(t)

	mode := m.DetermineMode(projID)
	if mode != state.SessionBootstrap {
		t.Errorf("mode = %q, want bootstrap", mode)
	}
}

func TestDetermineMode_DraftSRS(t *testing.T) {
	m, _, projID := testManager(t)

	_, err := m.engine.CreateRun(engine.RunOptions{
		ProjectID:  projID,
		BaseBranch: "main",
		BudgetUSD:  10.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	mode := m.DetermineMode(projID)
	if mode != state.SessionBootstrap {
		t.Errorf("mode = %q, want bootstrap", mode)
	}
}

func TestDetermineMode_AwaitingSRS(t *testing.T) {
	m, db, projID := testManager(t)

	run, err := m.engine.CreateRun(engine.RunOptions{
		ProjectID:  projID,
		BaseBranch: "main",
		BudgetUSD:  10.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}

	mode := m.DetermineMode(projID)
	if mode != state.SessionApproval {
		t.Errorf("mode = %q, want approval", mode)
	}
}

func TestDetermineMode_Active(t *testing.T) {
	m, db, projID := testManager(t)

	run, err := m.engine.CreateRun(engine.RunOptions{
		ProjectID:  projID,
		BaseBranch: "main",
		BudgetUSD:  10.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}

	mode := m.DetermineMode(projID)
	if mode != state.SessionExecution {
		t.Errorf("mode = %q, want execution", mode)
	}
}

func TestDetermineMode_Completed(t *testing.T) {
	m, db, projID := testManager(t)

	run, err := m.engine.CreateRun(engine.RunOptions{
		ProjectID:  projID,
		BaseBranch: "main",
		BudgetUSD:  10.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(run.ID, state.RunCompleted); err != nil {
		t.Fatal(err)
	}

	mode := m.DetermineMode(projID)
	if mode != state.SessionPostrun {
		t.Errorf("mode = %q, want postrun", mode)
	}
}

// --- Startup summary ---

func TestStartupSummary_NoRun(t *testing.T) {
	m, _, projID := testManager(t)

	summary, err := m.StartupSummary(projID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Mode != state.SessionBootstrap {
		t.Errorf("Mode = %q, want bootstrap", summary.Mode)
	}
	if summary.ProjectName == "" {
		t.Error("ProjectName should be set")
	}
	if summary.ActionCard == "" {
		t.Error("ActionCard should not be empty")
	}
}

func TestStartupSummary_ActiveRun(t *testing.T) {
	m, db, projID := testManager(t)

	run, err := m.engine.CreateRun(engine.RunOptions{
		ProjectID:  projID,
		BaseBranch: "main",
		BudgetUSD:  10.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}

	summary, err := m.StartupSummary(projID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Mode != state.SessionExecution {
		t.Errorf("Mode = %q, want execution", summary.Mode)
	}
	if summary.RunID == "" {
		t.Error("RunID should be set for active run")
	}
}

// --- Transcript ---

func TestAddTranscriptMessage(t *testing.T) {
	m, db, projID := testManager(t)

	sess, err := m.CreateSession(projID)
	if err != nil {
		t.Fatal(err)
	}

	seq, err := m.AddTranscriptMessage(sess.ID, "user", "user", "Build me an API")
	if err != nil {
		t.Fatalf("AddTranscriptMessage: %v", err)
	}
	if seq != 1 {
		t.Errorf("seq = %d, want 1", seq)
	}

	// Add another
	seq2, err := m.AddTranscriptMessage(sess.ID, "assistant", "assistant", "I will create an API...")
	if err != nil {
		t.Fatal(err)
	}
	if seq2 != 2 {
		t.Errorf("seq = %d, want 2", seq2)
	}

	// Verify persisted
	msgs, err := db.GetMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len = %d, want 2", len(msgs))
	}
}

// --- Compaction ---

func TestCompactSession(t *testing.T) {
	m, db, projID := testManager(t)

	sess, err := m.CreateSession(projID)
	if err != nil {
		t.Fatal(err)
	}

	// Add messages to exceed threshold
	for i := 1; i <= 5; i++ {
		if _, err := m.AddTranscriptMessage(sess.ID, "user", "user", "message"); err != nil {
			t.Fatal(err)
		}
	}

	// Compact: keep only the last 2 messages
	err = m.CompactSession(sess.ID, 2)
	if err != nil {
		t.Fatalf("CompactSession: %v", err)
	}

	// Check that a summary was created
	sums, err := db.GetSessionSummaries(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(sums))
	}
	if sums[0].SummaryKind != "transcript_compaction" {
		t.Errorf("summary kind = %q", sums[0].SummaryKind)
	}

	// Remaining messages should be 2
	msgs, err := db.GetMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Errorf("remaining messages = %d, want 2", len(msgs))
	}
}

// --- Export ---

func TestExportSession(t *testing.T) {
	m, _, projID := testManager(t)

	sess, err := m.CreateSession(projID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.AddTranscriptMessage(sess.ID, "user", "user", "Build an API"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddTranscriptMessage(sess.ID, "assistant", "assistant", "Starting..."); err != nil {
		t.Fatal(err)
	}

	export, err := m.ExportSession(sess.ID)
	if err != nil {
		t.Fatalf("ExportSession: %v", err)
	}
	if export.SessionID != sess.ID {
		t.Errorf("SessionID = %q", export.SessionID)
	}
	if len(export.Messages) != 2 {
		t.Errorf("Messages = %d, want 2", len(export.Messages))
	}
}

func TestExportSessionNotFound(t *testing.T) {
	m, _, _ := testManager(t)

	_, err := m.ExportSession("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

// --- Prompt suggestions ---

func TestPromptSuggestions_Bootstrap(t *testing.T) {
	m, _, projID := testManager(t)

	suggestions := m.PromptSuggestions(projID)
	if len(suggestions) == 0 {
		t.Error("expected at least one suggestion in bootstrap mode")
	}
}

func TestPromptSuggestions_WithActiveRun(t *testing.T) {
	m, db, projID := testManager(t)

	run, err := m.engine.CreateRun(engine.RunOptions{
		ProjectID:  projID,
		BaseBranch: "main",
		BudgetUSD:  10.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}

	suggestions := m.PromptSuggestions(projID)
	if len(suggestions) == 0 {
		t.Error("expected suggestions for active run")
	}
}

// --- Event emission ---

func TestStartupSummaryEmitsEvent(t *testing.T) {
	m, _, projID := testManager(t)

	// Subscribe to startup_summary events
	ch, subID := m.engine.Bus().Subscribe(func(ev events.EngineEvent) bool {
		return ev.Type == events.StartupSummary
	})
	defer m.engine.Bus().Unsubscribe(subID)

	_, err := m.StartupSummary(projID)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Type != events.StartupSummary {
			t.Errorf("event type = %q", ev.Type)
		}
	default:
		t.Error("expected startup_summary event to be emitted")
	}
}

// --- Input history ---

func TestRecordAndRecallInputHistory(t *testing.T) {
	m, _, projID := testManager(t)

	sess, err := m.CreateSession(projID)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.RecordInput(projID, sess.ID, "prompt", "build an API"); err != nil {
		t.Fatalf("RecordInput: %v", err)
	}
	if err := m.RecordInput(projID, sess.ID, "prompt", "add auth"); err != nil {
		t.Fatal(err)
	}

	history, err := m.InputHistory(projID, 10)
	if err != nil {
		t.Fatalf("InputHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("len = %d, want 2", len(history))
	}
	// Most recent first
	if history[0] != "add auth" {
		t.Errorf("first = %q, want %q", history[0], "add auth")
	}
}
