package state

import (
	"testing"
)

func TestCreateRun(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	r := &ProjectRun{
		ID:                  "run-1",
		ProjectID:           projID,
		Status:              RunDraftSRS,
		BaseBranch:          "main",
		WorkBranch:          "axiom/test-project",
		OrchestratorMode:    "embedded",
		OrchestratorRuntime: "claw",
		SRSApprovalDelegate: "user",
		BudgetMaxUSD:        10.0,
		ConfigSnapshot:      `{"project":{"name":"test"}}`,
	}
	if err := db.CreateRun(r); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	got, err := db.GetRun("run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Status != RunDraftSRS {
		t.Errorf("Status = %q, want %q", got.Status, RunDraftSRS)
	}
	if got.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want %q", got.BaseBranch, "main")
	}
	if got.BudgetMaxUSD != 10.0 {
		t.Errorf("BudgetMaxUSD = %v, want 10.0", got.BudgetMaxUSD)
	}
	if got.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
	if got.PausedAt != nil {
		t.Error("PausedAt should be nil")
	}
}

func TestGetRunNotFound(t *testing.T) {
	db := testDB(t)

	_, err := db.GetRun("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListRunsByProject(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	for _, id := range []string{"run-a", "run-b"} {
		r := &ProjectRun{
			ID: id, ProjectID: projID, Status: RunDraftSRS,
			BaseBranch: "main", WorkBranch: "axiom/" + id,
			OrchestratorMode: "embedded", OrchestratorRuntime: "claw",
			SRSApprovalDelegate: "user", BudgetMaxUSD: 5.0, ConfigSnapshot: "{}",
		}
		if err := db.CreateRun(r); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := db.ListRunsByProject(projID)
	if err != nil {
		t.Fatalf("ListRunsByProject: %v", err)
	}
	if len(runs) != 2 {
		t.Errorf("len = %d, want 2", len(runs))
	}
}

func TestGetActiveRun(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	// No active run yet
	_, err := db.GetActiveRun(projID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for empty project, got %v", err)
	}

	// Create an active run
	r := &ProjectRun{
		ID: "run-active", ProjectID: projID, Status: RunActive,
		BaseBranch: "main", WorkBranch: "axiom/test",
		OrchestratorMode: "embedded", OrchestratorRuntime: "claw",
		SRSApprovalDelegate: "user", BudgetMaxUSD: 10.0, ConfigSnapshot: "{}",
	}
	if err := db.CreateRun(r); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetActiveRun(projID)
	if err != nil {
		t.Fatalf("GetActiveRun: %v", err)
	}
	if got.ID != "run-active" {
		t.Errorf("ID = %q, want %q", got.ID, "run-active")
	}
}

func TestUpdateRunStatus_ValidTransitions(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	transitions := []struct {
		from RunStatus
		to   RunStatus
	}{
		{RunDraftSRS, RunAwaitingSRSApproval},
		{RunAwaitingSRSApproval, RunActive},
		{RunActive, RunPaused},
		{RunPaused, RunActive},
		{RunActive, RunCompleted},
	}

	for i, tr := range transitions {
		r := &ProjectRun{
			ID: "run-tr-" + string(rune('a'+i)), ProjectID: projID, Status: tr.from,
			BaseBranch: "main", WorkBranch: "axiom/tr",
			OrchestratorMode: "embedded", OrchestratorRuntime: "claw",
			SRSApprovalDelegate: "user", BudgetMaxUSD: 10.0, ConfigSnapshot: "{}",
		}
		if err := db.CreateRun(r); err != nil {
			t.Fatal(err)
		}

		if err := db.UpdateRunStatus(r.ID, tr.to); err != nil {
			t.Errorf("transition %s→%s failed: %v", tr.from, tr.to, err)
		}

		got, _ := db.GetRun(r.ID)
		if got.Status != tr.to {
			t.Errorf("after transition, Status = %q, want %q", got.Status, tr.to)
		}
	}
}

func TestUpdateRunStatus_InvalidTransition(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	r := &ProjectRun{
		ID: "run-invalid", ProjectID: projID, Status: RunDraftSRS,
		BaseBranch: "main", WorkBranch: "axiom/inv",
		OrchestratorMode: "embedded", OrchestratorRuntime: "claw",
		SRSApprovalDelegate: "user", BudgetMaxUSD: 10.0, ConfigSnapshot: "{}",
	}
	if err := db.CreateRun(r); err != nil {
		t.Fatal(err)
	}

	// draft_srs → completed is not valid
	err := db.UpdateRunStatus("run-invalid", RunCompleted)
	if err == nil {
		t.Error("expected error for invalid transition draft_srs → completed")
	}

	// Verify status unchanged
	got, _ := db.GetRun("run-invalid")
	if got.Status != RunDraftSRS {
		t.Errorf("Status changed to %q despite invalid transition", got.Status)
	}
}

func TestUpdateRunStatus_SetsTimestamps(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	// Test paused_at
	r := &ProjectRun{
		ID: "run-ts", ProjectID: projID, Status: RunActive,
		BaseBranch: "main", WorkBranch: "axiom/ts",
		OrchestratorMode: "embedded", OrchestratorRuntime: "claw",
		SRSApprovalDelegate: "user", BudgetMaxUSD: 10.0, ConfigSnapshot: "{}",
	}
	if err := db.CreateRun(r); err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateRunStatus("run-ts", RunPaused); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetRun("run-ts")
	if got.PausedAt == nil {
		t.Error("PausedAt should be set after pausing")
	}

	// Resume and then cancel
	if err := db.UpdateRunStatus("run-ts", RunActive); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus("run-ts", RunCancelled); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetRun("run-ts")
	if got.CancelledAt == nil {
		t.Error("CancelledAt should be set after cancellation")
	}
}

func TestUpdateRunStatus_TerminalStatesReject(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	r := &ProjectRun{
		ID: "run-terminal", ProjectID: projID, Status: RunCompleted,
		BaseBranch: "main", WorkBranch: "axiom/term",
		OrchestratorMode: "embedded", OrchestratorRuntime: "claw",
		SRSApprovalDelegate: "user", BudgetMaxUSD: 10.0, ConfigSnapshot: "{}",
	}
	if err := db.CreateRun(r); err != nil {
		t.Fatal(err)
	}

	err := db.UpdateRunStatus("run-terminal", RunActive)
	if err == nil {
		t.Error("expected error transitioning from terminal state completed")
	}
}
