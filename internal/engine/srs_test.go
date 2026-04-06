package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

func TestSubmitSRS(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createTestRun(t, e, projID)

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	content := validTestSRS()
	if err := e.SubmitSRS(run.ID, content); err != nil {
		t.Fatalf("SubmitSRS: %v", err)
	}

	// Verify run transitioned to awaiting_srs_approval
	got, err := e.DB().GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunAwaitingSRSApproval {
		t.Errorf("status = %q, want %q", got.Status, state.RunAwaitingSRSApproval)
	}

	// Verify event emitted
	select {
	case ev := <-ch:
		if ev.Type != events.SRSSubmitted {
			t.Errorf("event type = %q, want %q", ev.Type, events.SRSSubmitted)
		}
		if ev.RunID != run.ID {
			t.Errorf("event run_id = %q, want %q", ev.RunID, run.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for srs_submitted event")
	}
}

func TestSubmitSRS_InvalidStructure(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createTestRun(t, e, projID)

	// Submit invalid SRS (missing required sections)
	err := e.SubmitSRS(run.ID, "# Not a valid SRS\n\nJust some text.")
	if err == nil {
		t.Fatal("expected error for invalid SRS structure")
	}

	// Verify run stayed in draft_srs
	got, err := e.DB().GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunDraftSRS {
		t.Errorf("status = %q, want %q (should not transition on invalid SRS)", got.Status, state.RunDraftSRS)
	}
}

func TestSubmitSRS_WrongRunStatus(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createTestRun(t, e, projID)

	// Transition past draft_srs
	content := validTestSRS()
	if err := e.SubmitSRS(run.ID, content); err != nil {
		t.Fatal(err)
	}

	// Try to submit again from awaiting_srs_approval
	err := e.SubmitSRS(run.ID, content)
	if err == nil {
		t.Fatal("expected error when submitting SRS from wrong status")
	}
}

func TestApproveSRS(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createTestRun(t, e, projID)

	content := validTestSRS()
	if err := e.SubmitSRS(run.ID, content); err != nil {
		t.Fatal(err)
	}

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	if err := e.ApproveSRS(run.ID); err != nil {
		t.Fatalf("ApproveSRS: %v", err)
	}

	// Verify run transitioned to active
	got, err := e.DB().GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunActive {
		t.Errorf("status = %q, want %q", got.Status, state.RunActive)
	}

	// Verify SRS hash was stored
	if got.SRSHash == nil || *got.SRSHash == "" {
		t.Error("expected SRS hash to be stored on run")
	}

	// Verify SRS file was written
	srsPath := filepath.Join(e.RootDir(), ".axiom", "srs.md")
	if _, err := os.Stat(srsPath); os.IsNotExist(err) {
		t.Error("expected SRS file to exist")
	}

	// Verify SRS file is read-only
	info, err := os.Stat(srsPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o200 != 0 {
		t.Errorf("SRS file should be read-only, got mode %o", info.Mode().Perm())
	}

	// Verify SRS hash file exists
	hashPath := filepath.Join(e.RootDir(), ".axiom", "srs.md.sha256")
	if _, err := os.Stat(hashPath); os.IsNotExist(err) {
		t.Error("expected SRS hash file to exist")
	}

	// Verify event emitted
	select {
	case ev := <-ch:
		if ev.Type != events.SRSApproved {
			t.Errorf("event type = %q, want %q", ev.Type, events.SRSApproved)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for srs_approved event")
	}
}

func TestApproveSRS_WrongStatus(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createTestRun(t, e, projID)

	// Try to approve from draft_srs (must be awaiting_srs_approval)
	err := e.ApproveSRS(run.ID)
	if err == nil {
		t.Fatal("expected error when approving SRS from draft_srs status")
	}
}

func TestRejectSRS(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createTestRun(t, e, projID)

	content := validTestSRS()
	if err := e.SubmitSRS(run.ID, content); err != nil {
		t.Fatal(err)
	}

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	feedback := "Please add more detail to the data model section."
	if err := e.RejectSRS(run.ID, feedback); err != nil {
		t.Fatalf("RejectSRS: %v", err)
	}

	// Verify run transitioned back to draft_srs
	got, err := e.DB().GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunDraftSRS {
		t.Errorf("status = %q, want %q", got.Status, state.RunDraftSRS)
	}

	// Verify event
	select {
	case ev := <-ch:
		if ev.Type != events.SRSRejected {
			t.Errorf("event type = %q, want %q", ev.Type, events.SRSRejected)
		}
		if ev.Details["feedback"] != feedback {
			t.Errorf("event feedback = %q, want %q", ev.Details["feedback"], feedback)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for srs_rejected event")
	}
}

func TestRejectSRS_WrongStatus(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createTestRun(t, e, projID)

	// Try to reject from draft_srs (must be awaiting_srs_approval)
	err := e.RejectSRS(run.ID, "feedback")
	if err == nil {
		t.Fatal("expected error when rejecting SRS from draft_srs status")
	}
}

func TestSRSRoundTrip_SubmitRejectResubmitApprove(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createTestRun(t, e, projID)

	// Submit → Reject → Resubmit → Approve
	content := validTestSRS()
	if err := e.SubmitSRS(run.ID, content); err != nil {
		t.Fatal(err)
	}
	if err := e.RejectSRS(run.ID, "needs more detail"); err != nil {
		t.Fatal(err)
	}

	// Run should be back in draft_srs
	got, _ := e.DB().GetRun(run.ID)
	if got.Status != state.RunDraftSRS {
		t.Fatalf("expected draft_srs after rejection, got %q", got.Status)
	}

	// Resubmit
	if err := e.SubmitSRS(run.ID, content); err != nil {
		t.Fatal(err)
	}

	// Approve
	if err := e.ApproveSRS(run.ID); err != nil {
		t.Fatal(err)
	}

	got, _ = e.DB().GetRun(run.ID)
	if got.Status != state.RunActive {
		t.Fatalf("expected active after approval, got %q", got.Status)
	}
}

func TestApproveSRS_ImmutableAfterApproval(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createTestRun(t, e, projID)

	content := validTestSRS()
	if err := e.SubmitSRS(run.ID, content); err != nil {
		t.Fatal(err)
	}
	if err := e.ApproveSRS(run.ID); err != nil {
		t.Fatal(err)
	}

	// Verify SRS file cannot be modified (read-only permissions)
	srsPath := filepath.Join(e.RootDir(), ".axiom", "srs.md")
	err := os.WriteFile(srsPath, []byte("modified"), 0o644)
	if err == nil {
		t.Error("expected error writing to read-only SRS file")
	}
}

// --- Test helpers ---

func seedTestProject(t *testing.T, e *Engine) string {
	t.Helper()
	projID := "proj-srs-test"
	if err := e.DB().CreateProject(&state.Project{
		ID:       projID,
		RootPath: e.RootDir(),
		Name:     "srs-test",
		Slug:     "srs-test",
	}); err != nil {
		t.Fatal(err)
	}
	return projID
}

func createTestRun(t *testing.T, e *Engine, projID string) *state.ProjectRun {
	t.Helper()
	// Ensure .axiom/ directory structure exists for SRS/ECO file operations
	for _, sub := range []string{"", "eco"} {
		dir := filepath.Join(e.RootDir(), ".axiom", sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run, err := e.CreateRun(RunOptions{
		ProjectID:  projID,
		BaseBranch: "main",
		BudgetUSD:  5.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func validTestSRS() string {
	return `# SRS: Test Project

## 1. Architecture

### 1.1 System Overview
A simple test system.

### 1.2 Component Breakdown
- Component A: core logic

### 1.3 Technology Decisions
Go for the backend.

### 1.4 Data Model
SQLite database.

### 1.5 Directory Structure
cmd/, internal/

## 2. Requirements & Constraints

### 2.1 Functional Requirements
- FR-001: The system SHALL work.

### 2.2 Non-Functional Requirements
- NFR-001: The system SHALL be fast.

### 2.3 Constraints
None.

### 2.4 Assumptions
None.

## 3. Test Strategy

### 3.1 Unit Testing
Standard Go tests.

### 3.2 Integration Testing
Docker-based.

## 4. Acceptance Criteria

### 4.1 Per-Component Criteria
- [ ] AC-001: It works.

### 4.2 Integration Criteria
- [ ] IC-001: Components integrate.

### 4.3 Completion Definition
All tests pass.
`
}
