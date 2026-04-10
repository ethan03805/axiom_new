package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/openaxiom/axiom/internal/state"
)

// dirtyGitService returns a ValidateClean error to simulate a dirty working
// tree. Used by the AllowDirty bypass tests.
type dirtyGitService struct {
	validateCleanCalls   int
	setupWorkBranchCalls int
	cancelCleanupCalls   int
}

func (g *dirtyGitService) CurrentBranch(string) (string, error) { return "main", nil }
func (g *dirtyGitService) CreateBranch(string, string) error    { return nil }
func (g *dirtyGitService) CurrentHEAD(string) (string, error)   { return "sha", nil }
func (g *dirtyGitService) IsDirty(string) (bool, error)         { return true, nil }
func (g *dirtyGitService) ValidateClean(string) error {
	g.validateCleanCalls++
	return errors.New("working tree has uncommitted changes; commit or stash before running axiom")
}
func (g *dirtyGitService) DetectBaseBranch(string) (string, error) { return "main", nil }
func (g *dirtyGitService) SetupWorkBranch(string, string, string) error {
	g.setupWorkBranchCalls++
	return nil
}
func (g *dirtyGitService) SetupWorkBranchAllowDirty(string, string, string) error {
	g.setupWorkBranchCalls++
	return nil
}
func (g *dirtyGitService) CancelCleanup(string, string) error {
	g.cancelCleanupCalls++
	return nil
}
func (g *dirtyGitService) AddFiles(string, []string) error           { return nil }
func (g *dirtyGitService) Commit(string, string) (string, error)     { return "sha", nil }
func (g *dirtyGitService) ChangedFilesSince(string, string) ([]string, error) {
	return nil, nil
}
func (g *dirtyGitService) DiffRange(string, string, string) (string, error) {
	return "", nil
}

func TestStartRun_PersistsPromptAndMetadata(t *testing.T) {
	e := newTestEngine(t)

	projectID := seedProject(t, e, "startrun-test")

	run, err := e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "Build a REST API with auth",
		Source:    "cli",
		BudgetUSD: 5.0,
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	if run.Status != state.RunDraftSRS {
		t.Errorf("status = %q, want draft_srs", run.Status)
	}
	if run.InitialPrompt != "Build a REST API with auth" {
		t.Errorf("initial_prompt = %q, want %q", run.InitialPrompt, "Build a REST API with auth")
	}
	if run.StartSource != "cli" {
		t.Errorf("start_source = %q, want cli", run.StartSource)
	}
	if run.OrchestratorMode != "external" {
		t.Errorf("orchestrator_mode = %q, want external", run.OrchestratorMode)
	}

	// Verify persistence via re-read
	reread, err := e.db.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if reread.InitialPrompt != "Build a REST API with auth" {
		t.Errorf("re-read prompt = %q, want %q", reread.InitialPrompt, "Build a REST API with auth")
	}
	if reread.OrchestratorMode != "external" {
		t.Errorf("re-read orchestrator_mode = %q, want external", reread.OrchestratorMode)
	}
}

func TestStartRun_RequiresPrompt(t *testing.T) {
	e := newTestEngine(t)

	projectID := seedProject(t, e, "no-prompt")

	_, err := e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "",
		Source:    "cli",
	})
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestStartRun_DefaultsSourceToCLI(t *testing.T) {
	e := newTestEngine(t)

	projectID := seedProject(t, e, "default-source")

	run, err := e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "test prompt",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if run.StartSource != "cli" {
		t.Errorf("start_source = %q, want cli", run.StartSource)
	}
}

func TestExternalHandoff_FullLifecycle(t *testing.T) {
	e := newTestEngine(t)

	projectID := seedProject(t, e, "lifecycle-test")

	// 1. Start run
	run, err := e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "Build a chat app",
		Source:    "api",
		BudgetUSD: 10.0,
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if run.Status != state.RunDraftSRS {
		t.Fatalf("after start: status = %q, want draft_srs", run.Status)
	}

	// 2. External orchestrator submits SRS
	srsContent := `# SRS: Chat App

## 1. Architecture
Single-page app with WebSocket backend.

## 2. Requirements & Constraints
- Real-time messaging
- User authentication

## 3. Test Strategy
Unit and integration tests.

## 4. Acceptance Criteria
- Users can send and receive messages in real time.
`
	if err := e.SubmitSRS(run.ID, srsContent); err != nil {
		t.Fatalf("SubmitSRS: %v", err)
	}

	// Verify transition to awaiting_srs_approval
	run, err = e.db.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != state.RunAwaitingSRSApproval {
		t.Fatalf("after submit: status = %q, want awaiting_srs_approval", run.Status)
	}

	// 3. Read draft back
	draft, err := e.ReadSRSDraft(run.ID)
	if err != nil {
		t.Fatalf("ReadSRSDraft: %v", err)
	}
	if draft != srsContent {
		t.Errorf("draft content mismatch")
	}

	// 4. Approve SRS
	if err := e.ApproveSRS(run.ID); err != nil {
		t.Fatalf("ApproveSRS: %v", err)
	}

	run, err = e.db.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != state.RunActive {
		t.Fatalf("after approve: status = %q, want active", run.Status)
	}
	if run.SRSHash == nil || *run.SRSHash == "" {
		t.Error("srs_hash should be set after approval")
	}

	// 5. Verify .axiom/srs.md and .axiom/srs.md.sha256 exist
	srsPath := filepath.Join(e.rootDir, ".axiom", "srs.md")
	if _, err := os.Stat(srsPath); err != nil {
		t.Errorf("srs.md not found: %v", err)
	}
	hashPath := filepath.Join(e.rootDir, ".axiom", "srs.md.sha256")
	if _, err := os.Stat(hashPath); err != nil {
		t.Errorf("srs.md.sha256 not found: %v", err)
	}
}

func TestExternalHandoff_RejectAndResubmit(t *testing.T) {
	e := newTestEngine(t)

	projectID := seedProject(t, e, "reject-test")

	run, err := e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "Build something",
		Source:    "cli",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Submit first draft
	draft1 := `# SRS: Something

## 1. Architecture
Monolith.

## 2. Requirements & Constraints
None.

## 3. Test Strategy
None.

## 4. Acceptance Criteria
It works.
`
	if err := e.SubmitSRS(run.ID, draft1); err != nil {
		t.Fatalf("SubmitSRS: %v", err)
	}

	// Reject
	if err := e.RejectSRS(run.ID, "Needs more detail"); err != nil {
		t.Fatalf("RejectSRS: %v", err)
	}

	run, err = e.db.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != state.RunDraftSRS {
		t.Fatalf("after reject: status = %q, want draft_srs", run.Status)
	}

	// Resubmit
	draft2 := `# SRS: Something Better

## 1. Architecture
Microservices with API gateway.

## 2. Requirements & Constraints
- High availability
- Horizontal scaling

## 3. Test Strategy
Full integration test suite.

## 4. Acceptance Criteria
All services respond within 100ms.
`
	if err := e.SubmitSRS(run.ID, draft2); err != nil {
		t.Fatalf("SubmitSRS after reject: %v", err)
	}

	run, err = e.db.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != state.RunAwaitingSRSApproval {
		t.Fatalf("after resubmit: status = %q, want awaiting_srs_approval", run.Status)
	}
}

func TestStartRun_SubmitInvalidSRSFails(t *testing.T) {
	e := newTestEngine(t)

	projectID := seedProject(t, e, "invalid-srs")

	run, err := e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "Test",
		Source:    "cli",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Submit invalid SRS (missing required sections)
	err = e.SubmitSRS(run.ID, "This is not a valid SRS")
	if err == nil {
		t.Fatal("expected error for invalid SRS structure")
	}

	// Run should still be in draft_srs
	run, err = e.db.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != state.RunDraftSRS {
		t.Errorf("status = %q, want draft_srs after invalid submit", run.Status)
	}
}

func TestRestartRecovery_PromptPersisted(t *testing.T) {
	e := newTestEngine(t)

	projectID := seedProject(t, e, "recovery-test")

	run, err := e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "Build a dashboard",
		Source:    "api",
		BudgetUSD: 15.0,
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Simulate restart: read run from DB with a fresh query
	recovered, err := e.db.GetActiveRun(projectID)
	if err != nil {
		t.Fatalf("GetActiveRun after restart: %v", err)
	}

	if recovered.InitialPrompt != "Build a dashboard" {
		t.Errorf("recovered prompt = %q, want %q", recovered.InitialPrompt, "Build a dashboard")
	}
	if recovered.StartSource != "api" {
		t.Errorf("recovered source = %q, want api", recovered.StartSource)
	}
	if recovered.OrchestratorMode != "external" {
		t.Errorf("recovered mode = %q, want external", recovered.OrchestratorMode)
	}
	if recovered.ID != run.ID {
		t.Errorf("recovered ID = %q, want %q", recovered.ID, run.ID)
	}
}

// TestStartRun_AllowDirtyBypassesValidateClean verifies that setting
// StartRunOptions.AllowDirty skips the clean-tree check entirely. This is
// the recovery-mode escape hatch — Architecture §28.2 requires a clean
// tree by default, but crash-recovery scenarios need an opt-in bypass.
func TestStartRun_AllowDirtyBypassesValidateClean(t *testing.T) {
	gitSvc := &dirtyGitService{}
	e := newTestEngineWithGit(t, gitSvc)

	projectID := seedProject(t, e, "allow-dirty")

	run, err := e.StartRun(StartRunOptions{
		ProjectID:  projectID,
		Prompt:     "recovery scenario",
		Source:     "cli",
		AllowDirty: true,
	})
	if err != nil {
		t.Fatalf("StartRun with AllowDirty: %v", err)
	}
	if run == nil {
		t.Fatal("expected run record, got nil")
	}
	if gitSvc.validateCleanCalls != 0 {
		t.Errorf("ValidateClean calls = %d, want 0 when AllowDirty is set", gitSvc.validateCleanCalls)
	}
	if gitSvc.setupWorkBranchCalls != 1 {
		t.Errorf("SetupWorkBranch calls = %d, want 1 (work branch still created)", gitSvc.setupWorkBranchCalls)
	}
}

// TestStartRun_RefusesWhenActiveRunExists verifies the GitHub #1 fix:
// if the project already has an in-flight run (draft_srs / awaiting /
// active / paused), StartRun must refuse the second StartRun instead
// of silently clobbering the prior run's state. The refusal must
// surface as an ActiveRunExistsError carrying the existing run's ID
// and status.
func TestStartRun_RefusesWhenActiveRunExists(t *testing.T) {
	e := newTestEngine(t)
	projectID := seedProject(t, e, "refuse-clobber")

	first, err := e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "first prompt",
		Source:    "cli",
	})
	if err != nil {
		t.Fatalf("first StartRun: %v", err)
	}

	_, err = e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "second prompt",
		Source:    "tui",
	})
	if err == nil {
		t.Fatal("expected StartRun to refuse second call with an active run")
	}
	if !errors.Is(err, ErrActiveRunExists) {
		t.Errorf("expected ErrActiveRunExists, got %T: %v", err, err)
	}
	var activeErr *ActiveRunExistsError
	if !errors.As(err, &activeErr) {
		t.Fatalf("expected *ActiveRunExistsError, got %T", err)
	}
	if activeErr.RunID != first.ID {
		t.Errorf("ActiveRunExistsError.RunID = %q, want %q", activeErr.RunID, first.ID)
	}
	if activeErr.Status != state.RunDraftSRS {
		t.Errorf("ActiveRunExistsError.Status = %q, want draft_srs", activeErr.Status)
	}

	// The first run's metadata must be intact — the second call must not
	// have mutated the DB state.
	reread, err := e.db.GetRun(first.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if reread.InitialPrompt != "first prompt" {
		t.Errorf("first run's prompt was clobbered: got %q", reread.InitialPrompt)
	}
}

// TestStartRun_ForceReplacesActiveRun verifies that Force=true is the
// documented escape hatch: it bypasses the ActiveRunExistsError guard
// and allows a new run to be created over the prior one. The prior
// run's draft state is left on disk (audit trail); the new run becomes
// the active run.
func TestStartRun_ForceReplacesActiveRun(t *testing.T) {
	e := newTestEngine(t)
	projectID := seedProject(t, e, "force-replace")

	first, err := e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "first prompt",
		Source:    "cli",
	})
	if err != nil {
		t.Fatalf("first StartRun: %v", err)
	}

	second, err := e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "replacement prompt",
		Source:    "cli",
		Force:     true,
	})
	if err != nil {
		t.Fatalf("StartRun with Force=true should succeed: %v", err)
	}
	if second.ID == first.ID {
		t.Error("expected a new run ID after Force replace")
	}
	if second.InitialPrompt != "replacement prompt" {
		t.Errorf("new run prompt = %q, want replacement prompt", second.InitialPrompt)
	}

	// The first run still exists as an orphan for audit trail purposes.
	// GetActiveRun returns the most recent; both should still be in the
	// runs table (neither has been deleted).
	if _, err := e.db.GetRun(first.ID); err != nil {
		t.Errorf("first run should still be queryable for audit trail, got: %v", err)
	}
}

// TestStartRun_RefusesDirtyTreeByDefault verifies that without AllowDirty,
// a dirty working tree blocks the run.
func TestStartRun_RefusesDirtyTreeByDefault(t *testing.T) {
	gitSvc := &dirtyGitService{}
	e := newTestEngineWithGit(t, gitSvc)

	projectID := seedProject(t, e, "refuse-dirty")

	_, err := e.StartRun(StartRunOptions{
		ProjectID: projectID,
		Prompt:    "normal run",
		Source:    "cli",
	})
	if err == nil {
		t.Fatal("expected error when working tree is dirty and AllowDirty is unset")
	}
	if gitSvc.validateCleanCalls != 1 {
		t.Errorf("ValidateClean calls = %d, want 1 in default mode", gitSvc.validateCleanCalls)
	}
	if gitSvc.setupWorkBranchCalls != 0 {
		t.Errorf("SetupWorkBranch calls = %d, want 0 when ValidateClean fails", gitSvc.setupWorkBranchCalls)
	}
}

// --- helpers ---

func newTestEngine(t *testing.T) *Engine {
	return newTestEngineWithGit(t, &noopGitService{})
}

func newTestEngineWithGit(t *testing.T, git GitService) *Engine {
	t.Helper()
	db := testDB(t)
	cfg := testConfig()
	dir := t.TempDir()

	// Create .axiom directory for SRS draft persistence
	if err := os.MkdirAll(filepath.Join(dir, ".axiom"), 0o755); err != nil {
		t.Fatal(err)
	}

	e, err := New(Options{
		Config:  cfg,
		DB:      db,
		RootDir: dir,
		Log:     testLogger(),
		Git:     git,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Stop() })
	return e
}

func seedProject(t *testing.T, e *Engine, slug string) string {
	t.Helper()
	proj := &state.Project{
		ID:       slug,
		RootPath: e.rootDir,
		Name:     slug,
		Slug:     slug,
	}
	if err := e.db.CreateProject(proj); err != nil {
		t.Fatal(err)
	}
	return proj.ID
}

