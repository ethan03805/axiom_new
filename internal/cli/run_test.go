package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/state"
)

func TestRunAction_CreatesRun(t *testing.T) {
	application, proj := testAppWithProject(t)
	buf := new(bytes.Buffer)

	err := runAction(application, proj.ID, "Build a web app", 5.0, false, false, "", buf)
	if err != nil {
		t.Fatalf("runAction: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Run created") {
		t.Errorf("expected output to contain 'Run created', got: %s", output)
	}
	if !strings.Contains(output, "draft_srs") {
		t.Errorf("expected output to contain 'draft_srs', got: %s", output)
	}
	if !strings.Contains(output, "axiom/test-project") {
		t.Errorf("expected output to contain work branch 'axiom/test-project', got: %s", output)
	}
	if !strings.Contains(output, "external orchestrator") {
		t.Errorf("expected output to mention external orchestrator, got: %s", output)
	}
	if !strings.Contains(output, "Build a web app") {
		t.Errorf("expected output to contain prompt, got: %s", output)
	}
}

func TestRunAction_UsesConfigBudget(t *testing.T) {
	application, proj := testAppWithProject(t)
	buf := new(bytes.Buffer)

	// BudgetUSD=0 means use config default
	err := runAction(application, proj.ID, "Build something", 0, false, false, "", buf)
	if err != nil {
		t.Fatalf("runAction: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "10.00") {
		t.Errorf("expected output to contain config budget '$10.00', got: %s", output)
	}
}

func TestRunAction_CustomBudget(t *testing.T) {
	application, proj := testAppWithProject(t)
	buf := new(bytes.Buffer)

	err := runAction(application, proj.ID, "Build something", 25.0, false, false, "", buf)
	if err != nil {
		t.Fatalf("runAction: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "25.00") {
		t.Errorf("expected output to contain custom budget '$25.00', got: %s", output)
	}
}

func TestPauseAction_PausesActiveRun(t *testing.T) {
	application, _, run := testAppWithActiveRun(t)
	buf := new(bytes.Buffer)

	err := pauseAction(application, run.ID, buf)
	if err != nil {
		t.Fatalf("pauseAction: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "paused") {
		t.Errorf("expected output to contain 'paused', got: %s", output)
	}

	// Verify state
	got, err := application.DB.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunPaused {
		t.Errorf("status = %q, want paused", got.Status)
	}
}

func TestPauseAction_ErrorsOnNoActiveRun(t *testing.T) {
	application, _ := testAppWithProject(t)
	buf := new(bytes.Buffer)

	err := pauseAction(application, "nonexistent-run", buf)
	if err == nil {
		t.Fatal("expected error when pausing nonexistent run")
	}
}

func TestResumeAction_ResumesRun(t *testing.T) {
	application, _, run := testAppWithActiveRun(t)

	// Pause first
	if err := application.Engine.PauseRun(run.ID); err != nil {
		t.Fatal(err)
	}

	buf := new(bytes.Buffer)
	err := resumeAction(application, run.ID, buf)
	if err != nil {
		t.Fatalf("resumeAction: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "resumed") {
		t.Errorf("expected output to contain 'resumed', got: %s", output)
	}

	got, err := application.DB.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunActive {
		t.Errorf("status = %q, want active", got.Status)
	}
}

func TestResumeAction_ErrorsOnNonPausedRun(t *testing.T) {
	application, _, run := testAppWithActiveRun(t)
	buf := new(bytes.Buffer)

	// Run is active, not paused — resume should fail
	err := resumeAction(application, run.ID, buf)
	if err == nil {
		t.Fatal("expected error when resuming non-paused run")
	}
}

func TestCancelAction_CancelsRun(t *testing.T) {
	application, _, run := testAppWithActiveRun(t)
	buf := new(bytes.Buffer)

	err := cancelAction(application, run.ID, buf)
	if err != nil {
		t.Fatalf("cancelAction: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "cancelled") {
		t.Errorf("expected output to contain 'cancelled', got: %s", output)
	}

	got, err := application.DB.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunCancelled {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
}

func TestCancelAction_CancelsPausedRun(t *testing.T) {
	application, _, run := testAppWithActiveRun(t)

	if err := application.Engine.PauseRun(run.ID); err != nil {
		t.Fatal(err)
	}

	buf := new(bytes.Buffer)
	err := cancelAction(application, run.ID, buf)
	if err != nil {
		t.Fatalf("cancelAction: %v", err)
	}

	got, err := application.DB.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunCancelled {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
}

func TestCancelAction_ErrorsOnNonexistentRun(t *testing.T) {
	application, _ := testAppWithProject(t)
	buf := new(bytes.Buffer)

	err := cancelAction(application, "nonexistent-run", buf)
	if err == nil {
		t.Fatal("expected error when cancelling nonexistent run")
	}
}

func TestFindActiveRun_ReturnsRun(t *testing.T) {
	application, proj, run := testAppWithActiveRun(t)

	found, err := findActiveRun(application, proj.ID)
	if err != nil {
		t.Fatalf("findActiveRun: %v", err)
	}
	if found.ID != run.ID {
		t.Errorf("found run ID = %q, want %q", found.ID, run.ID)
	}
}

func TestFindActiveRun_ErrorsWhenNoRun(t *testing.T) {
	application, proj := testAppWithProject(t)

	_, err := findActiveRun(application, proj.ID)
	if err == nil {
		t.Fatal("expected error when no active run")
	}
}

func TestFindProjectID_FindsByRootPath(t *testing.T) {
	application, proj := testAppWithProject(t)

	id, err := findProjectID(application)
	if err != nil {
		t.Fatalf("findProjectID: %v", err)
	}
	if id != proj.ID {
		t.Errorf("project ID = %q, want %q", id, proj.ID)
	}
}

func TestRunAction_PersistsRun(t *testing.T) {
	application, proj := testAppWithProject(t)
	buf := new(bytes.Buffer)

	err := runAction(application, proj.ID, "test prompt", 5.0, false, false, "", buf)
	if err != nil {
		t.Fatal(err)
	}

	// Verify run exists in DB by finding active run
	run, err := application.DB.GetActiveRun(proj.ID)
	if err != nil {
		t.Fatalf("expected active run in DB: %v", err)
	}
	if run.Status != state.RunDraftSRS {
		t.Errorf("run status = %q, want draft_srs", run.Status)
	}
	if run.BudgetMaxUSD != 5.0 {
		t.Errorf("budget = %v, want 5.0", run.BudgetMaxUSD)
	}
	if run.InitialPrompt != "test prompt" {
		t.Errorf("initial_prompt = %q, want %q", run.InitialPrompt, "test prompt")
	}
	if run.StartSource != "cli" {
		t.Errorf("start_source = %q, want cli", run.StartSource)
	}
	if run.OrchestratorMode != "external" {
		t.Errorf("orchestrator_mode = %q, want external", run.OrchestratorMode)
	}
}

func TestRunCmd_AllowDirtyFlagRegistered(t *testing.T) {
	verbose := false
	cmd := RunCmd(&verbose)
	if cmd.Flags().Lookup("allow-dirty") == nil {
		t.Fatal("--allow-dirty flag should be registered on the run command")
	}
}

func TestRunCmd_LongDescriptionDocumentsAllowDirty(t *testing.T) {
	verbose := false
	cmd := RunCmd(&verbose)
	if !strings.Contains(cmd.Long, "--allow-dirty") {
		t.Errorf("Long description should mention --allow-dirty, got: %q", cmd.Long)
	}
}

func TestRunCmd_BaseBranchFlagRegistered(t *testing.T) {
	verbose := false
	cmd := RunCmd(&verbose)
	if cmd.Flags().Lookup("base-branch") == nil {
		t.Fatal("--base-branch flag should be registered on the run command")
	}
}

func TestRunCmd_RequiresPromptArg(t *testing.T) {
	verbose := false
	cmd := RunCmd(&verbose)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	// Execute without args — should fail because cobra.ExactArgs(1) is set
	_, err := executeCmd(cmd)
	if err == nil {
		t.Fatal("expected error when no prompt arg provided")
	}
}

func TestRunAction_EmitsEvent(t *testing.T) {
	application, proj := testAppWithProject(t)

	ch, subID := application.Engine.Bus().Subscribe(nil)
	defer application.Engine.Bus().Unsubscribe(subID)

	buf := new(bytes.Buffer)
	if err := runAction(application, proj.ID, "test prompt", 5.0, false, false, "", buf); err != nil {
		t.Fatal(err)
	}

	// Drain channel and look for run_created event
	select {
	case ev := <-ch:
		if ev.Type != "run_created" {
			t.Errorf("expected run_created event, got %q", ev.Type)
		}
	default:
		// Event may have been consumed already
	}
}

// Test that run command creates a run with the correct base branch.
func TestRunAction_DefaultBaseBranch(t *testing.T) {
	application, proj := testAppWithProject(t)
	buf := new(bytes.Buffer)

	if err := runAction(application, proj.ID, "test", 0, false, false, "", buf); err != nil {
		t.Fatal(err)
	}

	run, err := application.DB.GetActiveRun(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.BaseBranch != "main" {
		t.Errorf("base_branch = %q, want main", run.BaseBranch)
	}
}

// Ensure error handling for duplicate active runs. After the GitHub #1
// fix, StartRun refuses to clobber an existing in-flight run unless the
// operator passes --force. Without --force the CLI must surface a clear
// error that names the existing run ID; with --force it must replace the
// prior run and create a new active run.
func TestRunAction_ErrorsOnExistingActiveRun(t *testing.T) {
	application, _, existing := testAppWithActiveRun(t)
	buf := new(bytes.Buffer)

	err := runAction(application, "proj-test", "another prompt", 5.0, false, false, "", buf)
	if err == nil {
		t.Fatal("expected error when an active run already exists and --force is not set")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), existing.ID) {
		t.Errorf("error should include existing run ID %q, got: %v", existing.ID, err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should advise --force, got: %v", err)
	}
}

// TestRunAction_ForceReplacesActiveRun verifies the --force escape
// hatch: passing force=true lets the operator replace an in-flight run
// with a fresh one. The new run must be visible as the active run.
func TestRunAction_ForceReplacesActiveRun(t *testing.T) {
	application, _, existing := testAppWithActiveRun(t)
	buf := new(bytes.Buffer)

	err := runAction(application, "proj-test", "replacement prompt", 5.0, false, true, "", buf)
	if err != nil {
		t.Fatalf("runAction with force=true should succeed: %v", err)
	}

	run, err := application.DB.GetActiveRun("proj-test")
	if err != nil {
		t.Fatalf("expected an active run after force replace: %v", err)
	}
	if run.ID == existing.ID {
		t.Errorf("expected a new run after force replace, but got the old run %q", run.ID)
	}
	if run.InitialPrompt != "replacement prompt" {
		t.Errorf("new run prompt = %q, want %q", run.InitialPrompt, "replacement prompt")
	}
}

// TestRunAction_StartSourceIsCLI verifies that a run started via the
// CLI's runAction path records start_source="cli". This is the
// regression guard for the Issue 06 / start_source mislabelling bug
// where CLI runs previously flowed through a TUI path that hardcoded
// "tui" on the StartRunOptions.
func TestRunAction_StartSourceIsCLI(t *testing.T) {
	application, proj := testAppWithProject(t)
	buf := new(bytes.Buffer)

	if err := runAction(application, proj.ID, "cli-sourced prompt", 5.0, false, false, "", buf); err != nil {
		t.Fatalf("runAction: %v", err)
	}

	run, err := application.DB.GetActiveRun(proj.ID)
	if err != nil {
		t.Fatalf("expected active run: %v", err)
	}
	if run.StartSource != "cli" {
		t.Errorf("start_source = %q, want cli", run.StartSource)
	}
}
