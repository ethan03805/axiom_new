package tui

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/session"
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

func testSetup(t *testing.T) (*Model, *state.DB, string) {
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
	m := NewModel(eng, mgr, &cfg, projID, testLogger())
	return m, db, projID
}

// --- Model creation ---

func TestNewModel(t *testing.T) {
	m, _, _ := testSetup(t)
	if m == nil {
		t.Fatal("NewModel returned nil")
	}
}

// --- Init creates session ---

func TestModelInit(t *testing.T) {
	m, _, _ := testSetup(t)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init should return a command")
	}
}

// --- View renders startup frame ---

func TestModelView_HasStatusBar(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24

	// Trigger init to populate startup state
	m.handleStartupSummary()
	view := m.View()

	if !strings.Contains(view, "test-project") {
		t.Errorf("view should contain project name, got:\n%s", view)
	}
}

func TestModelView_HasActionCard(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	view := m.View()
	if !strings.Contains(view, "Describe what you want to build") {
		t.Errorf("view should contain bootstrap action card, got:\n%s", view)
	}
}

func TestModelView_HasComposer(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	view := m.View()
	// Footer should show mode or prompt indicator
	if !strings.Contains(view, "bootstrap") && !strings.Contains(view, ">") {
		t.Errorf("view should contain mode indicator or prompt, got:\n%s", view)
	}
}

// --- Input handling ---

func TestModelUpdate_TextInput(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	// Type a character
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	model := updated.(*Model)
	if model == nil {
		t.Fatal("Update returned nil model")
	}
}

func TestModelUpdate_QuitOnCtrlC(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c should return a quit command")
	}
}

// --- Slash commands ---

func TestSlashCommand_Status(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	result := m.handleSlashCommand("/status")
	if result == "" {
		t.Error("expected /status to produce output")
	}
}

func TestSlashCommand_Help(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	result := m.handleSlashCommand("/help")
	if result == "" {
		t.Error("expected /help to produce output")
	}
	if !strings.Contains(result, "/status") {
		t.Error("help should list /status command")
	}
}

func TestSlashCommand_Tasks(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	result := m.handleSlashCommand("/tasks")
	if result == "" {
		t.Error("expected /tasks to produce output")
	}
}

func TestSlashCommand_Budget(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	result := m.handleSlashCommand("/budget")
	if result == "" {
		t.Error("expected /budget to produce output")
	}
}

func TestSlashCommand_Clear(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	// Add some transcript content
	m.transcript = append(m.transcript, TranscriptEntry{Role: "user", Content: "hello"})

	result := m.handleSlashCommand("/clear")
	if result != "" {
		t.Errorf("expected empty result for /clear, got %q", result)
	}
	if len(m.transcript) != 0 {
		t.Errorf("transcript should be cleared, got %d entries", len(m.transcript))
	}
}

func TestSlashCommand_Unknown(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24

	result := m.handleSlashCommand("/nonexistent")
	if !strings.Contains(result, "Unknown command") {
		t.Errorf("expected unknown command message, got %q", result)
	}
}

// --- Overlay ---

func TestToggleOverlay(t *testing.T) {
	m, _, _ := testSetup(t)

	if m.overlay != OverlayNone {
		t.Errorf("initial overlay = %d, want OverlayNone", m.overlay)
	}

	m.overlay = OverlayHelp
	if m.overlay != OverlayHelp {
		t.Errorf("overlay = %d, want OverlayHelp", m.overlay)
	}
}

// --- Status bar rendering ---

func TestStatusBarContent(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.handleStartupSummary()

	bar := m.renderStatusBar()
	if bar == "" {
		t.Error("status bar should not be empty")
	}
	if !strings.Contains(bar, "test-project") {
		t.Errorf("status bar should contain project name, got: %s", bar)
	}
}

// --- Task rail rendering ---

func TestTaskRailRendering(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.handleStartupSummary()

	rail := m.renderTaskRail()
	// In bootstrap mode with no tasks, rail should indicate no tasks
	if !strings.Contains(rail, "No tasks") && !strings.Contains(rail, "Tasks") {
		t.Errorf("task rail should indicate tasks section, got: %s", rail)
	}
}

// --- Resize handling ---

func TestModelUpdate_WindowSize(t *testing.T) {
	m, _, _ := testSetup(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model := updated.(*Model)
	if model.width != 120 {
		t.Errorf("width = %d, want 120", model.width)
	}
	if model.height != 40 {
		t.Errorf("height = %d, want 40", model.height)
	}
}

// --- Transcript ---

func TestTranscriptAppend(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24

	m.appendTranscript("user", "user", "hello world")
	if len(m.transcript) != 1 {
		t.Fatalf("transcript len = %d, want 1", len(m.transcript))
	}
	if m.transcript[0].Content != "hello world" {
		t.Errorf("content = %q", m.transcript[0].Content)
	}
}

// --- Submit input ---

func TestSubmitInput_SlashCommand(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	cmd := m.submitInput("/help")
	// Should produce a system message in transcript
	found := false
	for _, entry := range m.transcript {
		if entry.Role == "system" && strings.Contains(entry.Content, "/status") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected help content in transcript after /help")
	}
	_ = cmd
}

func TestSubmitInput_UserMessage(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	m.submitInput("Build me an API")
	found := false
	for _, entry := range m.transcript {
		if entry.Role == "user" && entry.Content == "Build me an API" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected user message in transcript")
	}
}
