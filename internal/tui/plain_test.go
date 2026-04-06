package tui

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/session"
	"github.com/openaxiom/axiom/internal/state"
)

func plainTestSetup(t *testing.T) (*PlainRenderer, *state.DB, string) {
	t.Helper()
	db := testDB(t)
	projID := seedProject(t, db)
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

	mgr := session.New(eng, &cfg, testLogger())
	r := NewPlainRenderer(eng, mgr, &cfg, projID, testLogger())
	return r, db, projID
}

func TestNewPlainRenderer(t *testing.T) {
	r, _, _ := plainTestSetup(t)
	if r == nil {
		t.Fatal("NewPlainRenderer returned nil")
	}
}

func TestPlainRenderer_StartupFrame(t *testing.T) {
	r, _, _ := plainTestSetup(t)

	var buf bytes.Buffer
	if err := r.RenderStartup(&buf); err != nil {
		t.Fatalf("RenderStartup: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "test-project") {
		t.Errorf("startup should contain project name, got:\n%s", output)
	}
	if !strings.Contains(output, "bootstrap") {
		t.Errorf("startup should contain mode, got:\n%s", output)
	}
}

func TestPlainRenderer_StartupWithActiveRun(t *testing.T) {
	r, db, projID := plainTestSetup(t)

	run, err := r.engine.CreateRun(engine.RunOptions{
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

	var buf bytes.Buffer
	if err := r.RenderStartup(&buf); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "execution") {
		t.Errorf("startup should show execution mode, got:\n%s", output)
	}
}

func TestPlainRenderer_RenderMessage(t *testing.T) {
	r, _, _ := plainTestSetup(t)

	var buf bytes.Buffer
	r.RenderMessage(&buf, "user", "Hello world")

	output := buf.String()
	if !strings.Contains(output, "Hello world") {
		t.Errorf("expected message content, got: %s", output)
	}
	if !strings.Contains(output, ">") {
		t.Errorf("expected user prompt indicator, got: %s", output)
	}
}

func TestPlainRenderer_RenderSystemMessage(t *testing.T) {
	r, _, _ := plainTestSetup(t)

	var buf bytes.Buffer
	r.RenderMessage(&buf, "system", "SRS approved")

	output := buf.String()
	if !strings.Contains(output, "SRS approved") {
		t.Errorf("expected system message, got: %s", output)
	}
}

func TestPlainRenderer_RenderStatus(t *testing.T) {
	r, _, _ := plainTestSetup(t)

	var buf bytes.Buffer
	if err := r.RenderStatus(&buf); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "test-project") {
		t.Errorf("status should contain project name, got: %s", output)
	}
}

func TestPlainRenderer_RenderSessionList(t *testing.T) {
	r, _, projID := plainTestSetup(t)

	// Create a session
	sess, err := r.session.CreateSession(projID)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := r.RenderSessionList(&buf, projID); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, sess.ID[:8]) {
		t.Errorf("session list should contain session ID prefix, got:\n%s", output)
	}
}

func TestPlainRenderer_EmptySessionList(t *testing.T) {
	r, _, projID := plainTestSetup(t)

	var buf bytes.Buffer
	if err := r.RenderSessionList(&buf, projID); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "No sessions") {
		t.Errorf("expected 'No sessions' message, got: %s", output)
	}
}

func TestPlainRenderer_RenderExport(t *testing.T) {
	r, _, projID := plainTestSetup(t)

	sess, err := r.session.CreateSession(projID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.session.AddTranscriptMessage(sess.ID, "user", "user", "hello"); err != nil {
		t.Fatal(err)
	}

	export, err := r.session.ExportSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	r.RenderExport(&buf, export)

	output := buf.String()
	if !strings.Contains(output, "hello") {
		t.Errorf("export should contain message, got:\n%s", output)
	}
}

// testDB and seedProject need a distinct path for db to avoid conflicts
func init() {
	_ = filepath.Join // just to keep import valid
}
