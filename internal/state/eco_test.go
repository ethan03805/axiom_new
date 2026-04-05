package state

import (
	"testing"
)

func TestCreateECO(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	eco := &ECOLogEntry{
		RunID:          runID,
		ECOCode:        "ECO-DEP",
		Category:       "dependency_unavailable",
		Description:    "left-pad removed from npm",
		AffectedRefs:   `["FR-003"]`,
		ProposedChange: "Replace with string-left-pad",
		Status:         ECOProposed,
	}
	id, err := db.CreateECO(eco)
	if err != nil {
		t.Fatalf("CreateECO: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive ID")
	}

	got, err := db.GetECO(id)
	if err != nil {
		t.Fatalf("GetECO: %v", err)
	}
	if got.ECOCode != "ECO-DEP" {
		t.Errorf("ECOCode = %q", got.ECOCode)
	}
	if got.Status != ECOProposed {
		t.Errorf("Status = %q", got.Status)
	}
}

func TestGetECONotFound(t *testing.T) {
	db := testDB(t)

	_, err := db.GetECO(9999)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListECOsByRun(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	for _, code := range []string{"ECO-DEP", "ECO-API"} {
		eco := &ECOLogEntry{
			RunID: runID, ECOCode: code, Category: "test",
			Description: "desc", AffectedRefs: "[]",
			ProposedChange: "change", Status: ECOProposed,
		}
		if _, err := db.CreateECO(eco); err != nil {
			t.Fatal(err)
		}
	}

	ecos, err := db.ListECOsByRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ecos) != 2 {
		t.Errorf("len = %d, want 2", len(ecos))
	}
}

func TestUpdateECOStatus_Approve(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	eco := &ECOLogEntry{
		RunID: runID, ECOCode: "ECO-DEP", Category: "test",
		Description: "desc", AffectedRefs: "[]",
		ProposedChange: "change", Status: ECOProposed,
	}
	id, _ := db.CreateECO(eco)

	approvedBy := "user"
	if err := db.UpdateECOStatus(id, ECOApproved, &approvedBy); err != nil {
		t.Fatalf("UpdateECOStatus: %v", err)
	}

	got, _ := db.GetECO(id)
	if got.Status != ECOApproved {
		t.Errorf("Status = %q, want %q", got.Status, ECOApproved)
	}
	if got.ApprovedBy == nil || *got.ApprovedBy != "user" {
		t.Error("ApprovedBy should be 'user'")
	}
	if got.ResolvedAt == nil {
		t.Error("ResolvedAt should be set")
	}
}

func TestUpdateECOStatus_InvalidTransition(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	eco := &ECOLogEntry{
		RunID: runID, ECOCode: "ECO-DEP", Category: "test",
		Description: "desc", AffectedRefs: "[]",
		ProposedChange: "change", Status: ECOProposed,
	}
	id, _ := db.CreateECO(eco)

	// Approve it
	approvedBy := "user"
	if err := db.UpdateECOStatus(id, ECOApproved, &approvedBy); err != nil {
		t.Fatal(err)
	}

	// Try to reject after approval (invalid)
	err := db.UpdateECOStatus(id, ECORejected, nil)
	if err == nil {
		t.Error("expected error for invalid transition approved → rejected")
	}
}
