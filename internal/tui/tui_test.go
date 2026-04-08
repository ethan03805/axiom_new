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

// fakeGitService is a configurable in-memory GitService used by the TUI
// regression tests. Individual tests tweak its fields (dirty,
// validateErr, diffOutput) to simulate the specific conditions required
// by each assertion.
type fakeGitService struct {
	dirty        bool
	validateErr  error
	diffOutput   string
	diffErr      error
	resumeCalled bool
}

func (g *fakeGitService) CurrentBranch(dir string) (string, error) { return "main", nil }
func (g *fakeGitService) CreateBranch(dir, name string) error      { return nil }
func (g *fakeGitService) CurrentHEAD(dir string) (string, error)   { return "sha", nil }
func (g *fakeGitService) IsDirty(dir string) (bool, error)         { return g.dirty, nil }
func (g *fakeGitService) ValidateClean(dir string) error {
	if g.validateErr != nil {
		return g.validateErr
	}
	return nil
}
func (g *fakeGitService) SetupWorkBranch(dir, baseBranch, workBranch string) error { return nil }
func (g *fakeGitService) SetupWorkBranchAllowDirty(dir, baseBranch, workBranch string) error {
	return nil
}
func (g *fakeGitService) CancelCleanup(dir, baseBranch string) error { return nil }
func (g *fakeGitService) AddFiles(dir string, files []string) error  { return nil }
func (g *fakeGitService) Commit(dir string, message string) (string, error) {
	return "commit-sha", nil
}
func (g *fakeGitService) ChangedFilesSince(dir, sinceRef string) ([]string, error) {
	return nil, nil
}
func (g *fakeGitService) DiffRange(dir, base, head string) (string, error) {
	return g.diffOutput, g.diffErr
}

// testSetupWithGit creates a TUI model with an injected fake git service
// and a project root directory on disk. Returns the model, DB, project
// ID, the fake git service (for mutation), and the project root path.
func testSetupWithGit(t *testing.T, git *fakeGitService) (*Model, *state.DB, string, *fakeGitService, string) {
	t.Helper()
	db := testDB(t)
	projID := seedProject(t, db)
	cfg := config.Default("test-project", "test-project")
	rootDir := t.TempDir()
	// Create .axiom directory so SRS draft persistence has somewhere to live.
	if err := os.MkdirAll(filepath.Join(rootDir, ".axiom"), 0o755); err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(engine.Options{
		Config:  &cfg,
		DB:      db,
		RootDir: rootDir,
		Log:     testLogger(),
		Git:     git,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Stop() })

	mgr := session.New(eng, &cfg, testLogger())
	m := NewModel(eng, mgr, &cfg, projID, testLogger())
	return m, db, projID, git, rootDir
}

func testSetup(t *testing.T) (*Model, *state.DB, string) {
	t.Helper()
	m, db, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
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

// TestSubmitInput_UserMessage asserts that a bootstrap-mode prompt is
// echoed to the transcript AND creates a project_runs row via the
// engine's StartRun lifecycle. Prior to the Issue 08 fix, this test only
// asserted the transcript echo and therefore encoded the bug.
func TestSubmitInput_UserMessage(t *testing.T) {
	m, db, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	cmd := m.submitInput("Build me an API")
	// The async tea.Cmd returned by submitInput calls StartRun on the
	// Bubble Tea goroutine; in tests we call it directly so the side
	// effects land synchronously.
	if cmd == nil {
		t.Fatal("expected submitInput in bootstrap mode to return a non-nil tea.Cmd")
	}
	msg := cmd()
	started, ok := msg.(runStartedMsg)
	if !ok {
		t.Fatalf("expected runStartedMsg, got %T (%+v)", msg, msg)
	}
	if started.run == nil {
		t.Fatal("runStartedMsg carries nil run")
	}

	// Verify the transcript contains the prompt.
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

	// Verify a run row was created with the right metadata.
	run, err := db.GetActiveRun(projID)
	if err != nil {
		t.Fatalf("expected an active run after submitInput in bootstrap mode; got err=%v", err)
	}
	if run.InitialPrompt != "Build me an API" {
		t.Errorf("run.InitialPrompt = %q, want %q", run.InitialPrompt, "Build me an API")
	}
	if run.StartSource != "tui" {
		t.Errorf("run.StartSource = %q, want %q", run.StartSource, "tui")
	}
	if run.Status != state.RunDraftSRS {
		t.Errorf("run.Status = %q, want draft_srs", run.Status)
	}
}

// --- Issue 08 regression tests: mode-aware submitInput routing ---

func TestSubmitInput_BootstrapMode_StartsRun(t *testing.T) {
	m, db, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	cmd := m.submitInput("Build a REST API with JWT auth")
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd")
	}
	msg := cmd()
	if _, ok := msg.(runStartedMsg); !ok {
		t.Fatalf("expected runStartedMsg, got %T", msg)
	}

	run, err := db.GetActiveRun(projID)
	if err != nil {
		t.Fatalf("expected run to exist: %v", err)
	}
	if run.InitialPrompt != "Build a REST API with JWT auth" {
		t.Errorf("run.InitialPrompt = %q", run.InitialPrompt)
	}
	if run.StartSource != "tui" {
		t.Errorf("run.StartSource = %q", run.StartSource)
	}
}

func TestSubmitInput_BootstrapMode_DirtyTreeReportsError(t *testing.T) {
	git := &fakeGitService{validateErr: errDirty{}}
	m, db, projID, _, _ := testSetupWithGit(t, git)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	cmd := m.submitInput("Build something")
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd")
	}
	msg := cmd()
	failed, ok := msg.(runStartFailedMsg)
	if !ok {
		t.Fatalf("expected runStartFailedMsg, got %T", msg)
	}
	if failed.err == nil {
		t.Fatal("runStartFailedMsg has nil err")
	}

	// Feed the failure back through Update to exercise the transcript
	// append path.
	m.Update(failed)
	foundErr := false
	for _, entry := range m.transcript {
		if entry.Role == "system" && strings.Contains(entry.Content, "Failed to start run") {
			foundErr = true
			break
		}
	}
	if !foundErr {
		t.Error("expected failure message in transcript")
	}

	// No run should have been created.
	if _, err := db.GetActiveRun(projID); err == nil {
		t.Error("expected no active run after dirty-tree failure")
	}
}

func TestSubmitInput_ApprovalMode_ShowsApprovalHint(t *testing.T) {
	m, _, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	// Seed a run in awaiting_srs_approval.
	seedRunInStatus(t, m.engine, projID, state.RunAwaitingSRSApproval)
	m.handleStartupSummary()

	cmd := m.submitInput("Here are my thoughts on the SRS")
	if cmd != nil {
		if _, ok := cmd().(runStartedMsg); ok {
			t.Error("approval-mode input should not start a run")
		}
	}

	foundHint := false
	for _, entry := range m.transcript {
		if strings.Contains(entry.Content, "/srs") && strings.Contains(entry.Content, "/approve") {
			foundHint = true
			break
		}
	}
	if !foundHint {
		t.Error("expected approval-mode hint pointing at /srs and /approve")
	}
}

func TestSubmitInput_ExecutionMode_ShowsExecutionHint(t *testing.T) {
	m, _, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	seedRunInStatus(t, m.engine, projID, state.RunActive)
	m.handleStartupSummary()

	m.submitInput("What's happening with task 3?")
	foundHint := false
	for _, entry := range m.transcript {
		if strings.Contains(entry.Content, "not yet routed") || strings.Contains(entry.Content, "clarifications") {
			foundHint = true
			break
		}
	}
	if !foundHint {
		t.Error("expected execution-mode hint about clarifications")
	}
}

// --- Slash command regression tests ---

func TestSlashCommand_New_WithPromptStartsRun(t *testing.T) {
	m, db, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	result := m.handleSlashCommand("/new Build a REST API")
	_ = result
	if m.pendingCmd == nil {
		t.Fatal("expected /new with prompt to set pendingCmd")
	}
	msg := m.pendingCmd()
	m.pendingCmd = nil
	if _, ok := msg.(runStartedMsg); !ok {
		t.Fatalf("expected runStartedMsg from /new, got %T", msg)
	}
	run, err := db.GetActiveRun(projID)
	if err != nil {
		t.Fatalf("expected a run to exist: %v", err)
	}
	if run.InitialPrompt != "Build a REST API" {
		t.Errorf("run.InitialPrompt = %q, want %q", run.InitialPrompt, "Build a REST API")
	}
}

func TestSlashCommand_New_BareShowsHint(t *testing.T) {
	m, db, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	result := m.handleSlashCommand("/new")
	if !strings.Contains(result, "Type your prompt") {
		t.Errorf("expected hint, got %q", result)
	}
	if m.pendingCmd != nil {
		t.Error("/new should not create a pending command")
	}
	if _, err := db.GetActiveRun(projID); err == nil {
		t.Error("bare /new should not create a run")
	}
}

func TestSlashCommand_New_RefusesWhenRunInProgress(t *testing.T) {
	m, _, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	seedRunInStatus(t, m.engine, projID, state.RunActive)
	m.handleStartupSummary()

	result := m.handleSlashCommand("/new Build something else")
	if !strings.Contains(result, "already in progress") {
		t.Errorf("expected refusal message, got %q", result)
	}
	if m.pendingCmd != nil {
		t.Error("/new should not create a pending command when refused")
	}
}

func TestSlashCommand_Resume_PausedRunResumes(t *testing.T) {
	m, db, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	run := seedRunInStatus(t, m.engine, projID, state.RunPaused)
	m.handleStartupSummary()

	result := m.handleSlashCommand("/resume")
	if !strings.Contains(result, "resumed") {
		t.Errorf("expected resume confirmation, got %q", result)
	}
	fresh, err := db.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Status != state.RunActive {
		t.Errorf("run.Status = %q, want active", fresh.Status)
	}
}

func TestSlashCommand_Resume_NoPausedRunShowsStatus(t *testing.T) {
	m, _, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	seedRunInStatus(t, m.engine, projID, state.RunActive)
	m.handleStartupSummary()

	result := m.handleSlashCommand("/resume")
	if !strings.Contains(result, "No paused run") {
		t.Errorf("expected 'No paused run' message, got %q", result)
	}
}

func TestSlashCommand_Resume_NoRunShowsHint(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	result := m.handleSlashCommand("/resume")
	if !strings.Contains(result, "No runs found") && !strings.Contains(result, "/new") {
		t.Errorf("expected 'No runs found' hint, got %q", result)
	}
}

func TestSlashCommand_SRS_DraftSRSShowsWaitingMessage(t *testing.T) {
	m, _, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	seedRunInStatus(t, m.engine, projID, state.RunDraftSRS)
	m.handleStartupSummary()

	result := m.handleSlashCommand("/srs")
	if !strings.Contains(result, "draft_srs") && !strings.Contains(result, "Waiting") {
		t.Errorf("expected draft_srs waiting message, got %q", result)
	}
}

func TestSlashCommand_SRS_AwaitingApprovalShowsDraftAndActions(t *testing.T) {
	m, _, projID, _, rootDir := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	run := seedRunInStatus(t, m.engine, projID, state.RunAwaitingSRSApproval)
	// Write a draft file so ReadSRSDraft succeeds.
	draftPath := filepath.Join(rootDir, ".axiom", "srs-draft-"+run.ID+".md")
	if err := os.WriteFile(draftPath, []byte("# SRS: Test\n\n## 1. Architecture\nstuff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.handleStartupSummary()

	result := m.handleSlashCommand("/srs")
	if !strings.Contains(result, "# SRS: Test") {
		t.Errorf("expected draft content, got %q", result)
	}
	if !strings.Contains(result, "/approve") || !strings.Contains(result, "/reject") {
		t.Errorf("expected /approve and /reject instructions, got %q", result)
	}
}

func TestSlashCommand_SRS_ActiveShowsApprovedSRS(t *testing.T) {
	m, _, projID, _, rootDir := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	seedRunInStatus(t, m.engine, projID, state.RunActive)
	srsPath := filepath.Join(rootDir, ".axiom", "srs.md")
	if err := os.WriteFile(srsPath, []byte("APPROVED SRS CONTENT\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	m.handleStartupSummary()

	result := m.handleSlashCommand("/srs")
	if !strings.Contains(result, "APPROVED SRS CONTENT") {
		t.Errorf("expected approved SRS content, got %q", result)
	}
}

func TestSlashCommand_Approve_CallsApproveSRS(t *testing.T) {
	m, db, projID, _, rootDir := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	run := seedRunInStatus(t, m.engine, projID, state.RunAwaitingSRSApproval)
	// Write a valid SRS draft so ApproveSRS can read it.
	draft := "# SRS: Test\n\n## 1. Architecture\nfoo\n\n## 2. Requirements & Constraints\nbar\n\n## 3. Test Strategy\nbaz\n\n## 4. Acceptance Criteria\nqux\n"
	draftPath := filepath.Join(rootDir, ".axiom", "srs-draft-"+run.ID+".md")
	if err := os.WriteFile(draftPath, []byte(draft), 0o644); err != nil {
		t.Fatal(err)
	}
	m.handleStartupSummary()

	result := m.handleSlashCommand("/approve")
	if !strings.Contains(result, "approved") {
		t.Errorf("expected approval confirmation, got %q", result)
	}

	fresh, err := db.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Status != state.RunActive {
		t.Errorf("run.Status = %q, want active", fresh.Status)
	}
}

func TestSlashCommand_Approve_WrongStatusReportsError(t *testing.T) {
	m, _, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	seedRunInStatus(t, m.engine, projID, state.RunDraftSRS)
	m.handleStartupSummary()

	result := m.handleSlashCommand("/approve")
	if !strings.Contains(result, "Cannot approve") {
		t.Errorf("expected 'Cannot approve' error, got %q", result)
	}
}

func TestSlashCommand_Reject_RequiresFeedback(t *testing.T) {
	m, _, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	seedRunInStatus(t, m.engine, projID, state.RunAwaitingSRSApproval)
	m.handleStartupSummary()

	result := m.handleSlashCommand("/reject")
	if !strings.Contains(result, "feedback") {
		t.Errorf("expected feedback-required error, got %q", result)
	}
}

func TestSlashCommand_Reject_WithFeedbackCallsRejectSRS(t *testing.T) {
	m, db, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	run := seedRunInStatus(t, m.engine, projID, state.RunAwaitingSRSApproval)
	m.handleStartupSummary()

	result := m.handleSlashCommand(`/reject "needs section 4.2"`)
	if !strings.Contains(result, "rejected") {
		t.Errorf("expected rejection confirmation, got %q", result)
	}
	fresh, err := db.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Status != state.RunDraftSRS {
		t.Errorf("run.Status = %q, want draft_srs", fresh.Status)
	}
}

func TestSlashCommand_ECO_NoActiveRun(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	result := m.handleSlashCommand("/eco")
	if !strings.Contains(result, "No active run") {
		t.Errorf("expected 'No active run' message, got %q", result)
	}
}

func TestSlashCommand_ECO_EmptyListShowsNoneMessage(t *testing.T) {
	m, _, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	seedRunInStatus(t, m.engine, projID, state.RunActive)
	m.handleStartupSummary()

	result := m.handleSlashCommand("/eco")
	if !strings.Contains(result, "No ECOs proposed") {
		t.Errorf("expected 'No ECOs proposed' message, got %q", result)
	}
}

func TestSlashCommand_ECO_ListsProposed(t *testing.T) {
	m, db, projID, _, _ := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	run := seedRunInStatus(t, m.engine, projID, state.RunActive)
	if _, err := db.CreateECO(&state.ECOLogEntry{
		RunID:          run.ID,
		ECOCode:        "ECO-001",
		Category:       "scope",
		Description:    "test",
		AffectedRefs:   "FR-001",
		ProposedChange: "change",
		Status:         state.ECOStatus("proposed"),
	}); err != nil {
		t.Fatal(err)
	}
	m.handleStartupSummary()

	result := m.handleSlashCommand("/eco")
	if !strings.Contains(result, "ECO-001") {
		t.Errorf("expected ECO-001 in listing, got %q", result)
	}
	if !strings.Contains(result, "proposed") {
		t.Errorf("expected status 'proposed' in listing, got %q", result)
	}
}

func TestSlashCommand_Diff_NoActiveRun(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	result := m.handleSlashCommand("/diff")
	if !strings.Contains(result, "No active run") {
		t.Errorf("expected 'No active run' message, got %q", result)
	}
}

func TestSlashCommand_Diff_EmptyRangeReportsEmpty(t *testing.T) {
	git := &fakeGitService{diffOutput: ""}
	m, _, projID, _, _ := testSetupWithGit(t, git)
	m.width = 80
	m.height = 24
	seedRunInStatus(t, m.engine, projID, state.RunActive)
	m.handleStartupSummary()

	result := m.handleSlashCommand("/diff")
	if !strings.Contains(result, "No diff") {
		t.Errorf("expected 'No diff' message, got %q", result)
	}
}

func TestSlashCommand_Diff_TruncatesLargeOutput(t *testing.T) {
	bigDiff := strings.Repeat("x", 5000)
	git := &fakeGitService{diffOutput: bigDiff}
	m, _, projID, _, _ := testSetupWithGit(t, git)
	m.width = 80
	m.height = 24
	seedRunInStatus(t, m.engine, projID, state.RunActive)
	m.handleStartupSummary()

	result := m.handleSlashCommand("/diff")
	if !strings.Contains(result, "truncated") {
		t.Errorf("expected truncation marker, got output len=%d", len(result))
	}
	if !strings.Contains(result, "more bytes") {
		t.Errorf("expected 'more bytes' marker, got %q", result[:min(200, len(result))])
	}
}

func TestSlashCommand_Theme_Removed(t *testing.T) {
	m, _, _ := testSetup(t)
	m.width = 80
	m.height = 24
	m.handleStartupSummary()

	result := m.handleSlashCommand("/theme")
	if !strings.Contains(result, "Unknown command") {
		t.Errorf("expected /theme to be unknown after removal, got %q", result)
	}
}

func TestHelp_ListsApproveAndRejectCommands(t *testing.T) {
	m, _, _ := testSetup(t)
	result := m.cmdHelp()
	if !strings.Contains(result, "/approve") {
		t.Error("expected /help to list /approve")
	}
	if !strings.Contains(result, "/reject") {
		t.Error("expected /help to list /reject")
	}
}

func TestHelp_DoesNotListTheme(t *testing.T) {
	m, _, _ := testSetup(t)
	result := m.cmdHelp()
	if strings.Contains(result, "/theme") {
		t.Error("expected /help to NOT list /theme after removal")
	}
}

func TestRefreshAfterStateChange_UpdatesStartupSummary(t *testing.T) {
	m, _, projID, _, rootDir := testSetupWithGit(t, &fakeGitService{})
	m.width = 80
	m.height = 24
	run := seedRunInStatus(t, m.engine, projID, state.RunAwaitingSRSApproval)
	draft := "# SRS: Test\n\n## 1. Architecture\na\n\n## 2. Requirements & Constraints\nb\n\n## 3. Test Strategy\nc\n\n## 4. Acceptance Criteria\nd\n"
	draftPath := filepath.Join(rootDir, ".axiom", "srs-draft-"+run.ID+".md")
	if err := os.WriteFile(draftPath, []byte(draft), 0o644); err != nil {
		t.Fatal(err)
	}
	m.handleStartupSummary()

	beforeMode := m.mode
	if beforeMode != state.SessionApproval {
		t.Fatalf("precondition: mode = %q, want approval", beforeMode)
	}

	m.handleSlashCommand("/approve")
	if m.mode != state.SessionExecution {
		t.Errorf("after /approve: mode = %q, want execution", m.mode)
	}
	if m.startup == nil || !strings.Contains(m.startup.ActionCard, "Execution") {
		actionCard := ""
		if m.startup != nil {
			actionCard = m.startup.ActionCard
		}
		t.Errorf("action card after /approve = %q, want execution text", actionCard)
	}
}

// --- Test helpers ---

// errDirty is a sentinel error matching the gitops "dirty tree" message.
type errDirty struct{}

func (errDirty) Error() string {
	return "working tree has uncommitted changes; commit or stash before running axiom"
}

// seedRunInStatus creates a project_runs row for the test project and
// transitions it to the requested status via valid state transitions.
// Returns the run record as re-read from the DB so start_source and
// initial_prompt are populated.
func seedRunInStatus(t *testing.T, eng *engine.Engine, projID string, target state.RunStatus) *state.ProjectRun {
	t.Helper()
	run, err := eng.CreateRun(engine.RunOptions{
		ProjectID:  projID,
		BaseBranch: "main",
		BudgetUSD:  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Walk the status ladder: draft_srs -> awaiting -> active/paused.
	steps := []state.RunStatus{}
	switch target {
	case state.RunDraftSRS:
		// already there
	case state.RunAwaitingSRSApproval:
		steps = []state.RunStatus{state.RunAwaitingSRSApproval}
	case state.RunActive:
		steps = []state.RunStatus{state.RunAwaitingSRSApproval, state.RunActive}
	case state.RunPaused:
		steps = []state.RunStatus{state.RunAwaitingSRSApproval, state.RunActive, state.RunPaused}
	default:
		t.Fatalf("unsupported seed target: %s", target)
	}
	for _, s := range steps {
		if err := eng.DB().UpdateRunStatus(run.ID, s); err != nil {
			t.Fatalf("UpdateRunStatus(%s): %v", s, err)
		}
	}
	fresh, err := eng.DB().GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return fresh
}
