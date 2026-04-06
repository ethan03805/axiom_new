package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

func TestProposeECO(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createActiveTestRun(t, e, projID)

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	ecoID, err := e.ProposeECO(ECOProposal{
		RunID:          run.ID,
		Category:       "ECO-DEP",
		AffectedRefs:   "FR-001, AC-002",
		Description:    "Library passport-oauth2 is deprecated.",
		ProposedChange: "Replace with arctic v2.1.",
	})
	if err != nil {
		t.Fatalf("ProposeECO: %v", err)
	}

	if ecoID == 0 {
		t.Error("expected non-zero ECO ID")
	}

	// Verify ECO was created in DB
	eco, err := e.DB().GetECO(ecoID)
	if err != nil {
		t.Fatal(err)
	}
	if eco.Status != state.ECOProposed {
		t.Errorf("ECO status = %q, want %q", eco.Status, state.ECOProposed)
	}
	if eco.Category != "ECO-DEP" {
		t.Errorf("ECO category = %q, want ECO-DEP", eco.Category)
	}

	// Verify event
	select {
	case ev := <-ch:
		if ev.Type != events.ECOProposed {
			t.Errorf("event type = %q, want %q", ev.Type, events.ECOProposed)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for eco_proposed event")
	}
}

func TestProposeECO_InvalidCategory(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createActiveTestRun(t, e, projID)

	_, err := e.ProposeECO(ECOProposal{
		RunID:          run.ID,
		Category:       "ECO-NEW", // invalid
		AffectedRefs:   "FR-001",
		Description:    "Something.",
		ProposedChange: "Something else.",
	})
	if err == nil {
		t.Fatal("expected error for invalid ECO category")
	}
}

func TestProposeECO_RequiresActiveRun(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createTestRun(t, e, projID) // still in draft_srs

	_, err := e.ProposeECO(ECOProposal{
		RunID:          run.ID,
		Category:       "ECO-DEP",
		AffectedRefs:   "FR-001",
		Description:    "Issue.",
		ProposedChange: "Fix.",
	})
	if err == nil {
		t.Fatal("expected error when proposing ECO on non-active run")
	}
}

func TestApproveECO(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createActiveTestRun(t, e, projID)

	ecoID, err := e.ProposeECO(ECOProposal{
		RunID:          run.ID,
		Category:       "ECO-DEP",
		AffectedRefs:   "FR-001",
		Description:    "Library removed.",
		ProposedChange: "Use alternative.",
	})
	if err != nil {
		t.Fatal(err)
	}

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	approvedBy := "user"
	if err := e.ApproveECO(ecoID, approvedBy); err != nil {
		t.Fatalf("ApproveECO: %v", err)
	}

	// Verify status
	eco, err := e.DB().GetECO(ecoID)
	if err != nil {
		t.Fatal(err)
	}
	if eco.Status != state.ECOApproved {
		t.Errorf("ECO status = %q, want %q", eco.Status, state.ECOApproved)
	}
	if eco.ApprovedBy == nil || *eco.ApprovedBy != approvedBy {
		t.Errorf("ECO approved_by = %v, want %q", eco.ApprovedBy, approvedBy)
	}

	// Verify ECO file was written to .axiom/eco/
	ecoDir := filepath.Join(e.RootDir(), ".axiom", "eco")
	entries, err := os.ReadDir(ecoDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Error("expected ECO file to be written to .axiom/eco/")
	}

	// Verify event
	select {
	case ev := <-ch:
		if ev.Type != events.ECOResolved {
			t.Errorf("event type = %q, want %q", ev.Type, events.ECOResolved)
		}
		if ev.Details["resolution"] != "approved" {
			t.Errorf("event resolution = %v, want approved", ev.Details["resolution"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for eco_resolved event")
	}
}

func TestRejectECO(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createActiveTestRun(t, e, projID)

	ecoID, err := e.ProposeECO(ECOProposal{
		RunID:          run.ID,
		Category:       "ECO-API",
		AffectedRefs:   "FR-010",
		Description:    "API changed.",
		ProposedChange: "Update endpoint.",
	})
	if err != nil {
		t.Fatal(err)
	}

	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	if err := e.RejectECO(ecoID); err != nil {
		t.Fatalf("RejectECO: %v", err)
	}

	// Verify status
	eco, err := e.DB().GetECO(ecoID)
	if err != nil {
		t.Fatal(err)
	}
	if eco.Status != state.ECORejected {
		t.Errorf("ECO status = %q, want %q", eco.Status, state.ECORejected)
	}

	// Verify event
	select {
	case ev := <-ch:
		if ev.Type != events.ECOResolved {
			t.Errorf("event type = %q, want %q", ev.Type, events.ECOResolved)
		}
		if ev.Details["resolution"] != "rejected" {
			t.Errorf("event resolution = %v, want rejected", ev.Details["resolution"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for eco_resolved event")
	}
}

func TestApproveECO_AlreadyResolved(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createActiveTestRun(t, e, projID)

	ecoID, _ := e.ProposeECO(ECOProposal{
		RunID:          run.ID,
		Category:       "ECO-DEP",
		AffectedRefs:   "FR-001",
		Description:    "Issue.",
		ProposedChange: "Fix.",
	})

	if err := e.ApproveECO(ecoID, "user"); err != nil {
		t.Fatal(err)
	}

	// Try to approve again
	err := e.ApproveECO(ecoID, "user")
	if err == nil {
		t.Fatal("expected error when approving already-resolved ECO")
	}
}

func TestRejectECO_AlreadyResolved(t *testing.T) {
	e := testEngine(t)
	projID := seedTestProject(t, e)
	run := createActiveTestRun(t, e, projID)

	ecoID, _ := e.ProposeECO(ECOProposal{
		RunID:          run.ID,
		Category:       "ECO-DEP",
		AffectedRefs:   "FR-001",
		Description:    "Issue.",
		ProposedChange: "Fix.",
	})

	if err := e.RejectECO(ecoID); err != nil {
		t.Fatal(err)
	}

	// Try to reject again
	err := e.RejectECO(ecoID)
	if err == nil {
		t.Fatal("expected error when rejecting already-resolved ECO")
	}
}

// --- Test helpers ---

func createActiveTestRun(t *testing.T, e *Engine, projID string) *state.ProjectRun {
	t.Helper()
	run := createTestRun(t, e, projID)

	// Transition to active: draft_srs → awaiting_srs_approval → active
	if err := e.DB().UpdateRunStatus(run.ID, state.RunAwaitingSRSApproval); err != nil {
		t.Fatal(err)
	}
	if err := e.DB().UpdateRunStatus(run.ID, state.RunActive); err != nil {
		t.Fatal(err)
	}

	run, err := e.DB().GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}
