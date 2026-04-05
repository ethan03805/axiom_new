package state

import (
	"testing"
)

func TestCreateAttempt(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)

	a := &TaskAttempt{
		TaskID:        taskID,
		AttemptNumber: 1,
		ModelID:       "anthropic/claude-4-sonnet",
		ModelFamily:   "anthropic",
		BaseSnapshot:  "abc123",
		Status:        AttemptRunning,
		Phase:         PhaseExecuting,
	}
	id, err := db.CreateAttempt(a)
	if err != nil {
		t.Fatalf("CreateAttempt: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive ID, got %d", id)
	}

	got, err := db.GetAttempt(id)
	if err != nil {
		t.Fatalf("GetAttempt: %v", err)
	}
	if got.TaskID != taskID {
		t.Errorf("TaskID = %q, want %q", got.TaskID, taskID)
	}
	if got.Status != AttemptRunning {
		t.Errorf("Status = %q, want %q", got.Status, AttemptRunning)
	}
	if got.Phase != PhaseExecuting {
		t.Errorf("Phase = %q, want %q", got.Phase, PhaseExecuting)
	}
	if got.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
}

func TestGetAttemptNotFound(t *testing.T) {
	db := testDB(t)

	_, err := db.GetAttempt(9999)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListAttemptsByTask(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)

	for i := 1; i <= 3; i++ {
		a := &TaskAttempt{
			TaskID: taskID, AttemptNumber: i,
			ModelID: "model", ModelFamily: "test",
			BaseSnapshot: "abc", Status: AttemptRunning, Phase: PhaseExecuting,
		}
		if _, err := db.CreateAttempt(a); err != nil {
			t.Fatal(err)
		}
	}

	attempts, err := db.ListAttemptsByTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 3 {
		t.Errorf("len = %d, want 3", len(attempts))
	}
}

func TestUpdateAttemptStatus_ValidTransition(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)
	attemptID := seedAttempt(t, db, taskID)

	if err := db.UpdateAttemptStatus(attemptID, AttemptPassed); err != nil {
		t.Fatalf("UpdateAttemptStatus: %v", err)
	}

	got, _ := db.GetAttempt(attemptID)
	if got.Status != AttemptPassed {
		t.Errorf("Status = %q, want %q", got.Status, AttemptPassed)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt should be set after passing")
	}
}

func TestUpdateAttemptStatus_InvalidTransition(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)
	attemptID := seedAttempt(t, db, taskID)

	// First transition to passed (valid)
	if err := db.UpdateAttemptStatus(attemptID, AttemptPassed); err != nil {
		t.Fatal(err)
	}

	// Then try passed → running (invalid)
	err := db.UpdateAttemptStatus(attemptID, AttemptRunning)
	if err == nil {
		t.Error("expected error for invalid transition passed → running")
	}
}

func TestUpdateAttemptPhase_ValidTransition(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)
	attemptID := seedAttempt(t, db, taskID)

	if err := db.UpdateAttemptPhase(attemptID, PhaseValidating); err != nil {
		t.Fatalf("UpdateAttemptPhase: %v", err)
	}

	got, _ := db.GetAttempt(attemptID)
	if got.Phase != PhaseValidating {
		t.Errorf("Phase = %q, want %q", got.Phase, PhaseValidating)
	}
}

func TestUpdateAttemptPhase_InvalidTransition(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)
	attemptID := seedAttempt(t, db, taskID)

	// executing → merging is not valid (skips phases)
	err := db.UpdateAttemptPhase(attemptID, PhaseMerging)
	if err == nil {
		t.Error("expected error for invalid phase transition executing → merging")
	}
}

func TestCreateValidationRun(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)
	attemptID := seedAttempt(t, db, taskID)

	vr := &ValidationRun{
		AttemptID:  attemptID,
		CheckType:  CheckCompile,
		Status:     ValidationPass,
		DurationMs: ptrInt64(1500),
	}
	id, err := db.CreateValidationRun(vr)
	if err != nil {
		t.Fatalf("CreateValidationRun: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive ID")
	}

	runs, err := db.ListValidationRuns(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("len = %d, want 1", len(runs))
	}
	if runs[0].CheckType != CheckCompile {
		t.Errorf("CheckType = %q", runs[0].CheckType)
	}
	if runs[0].Status != ValidationPass {
		t.Errorf("Status = %q", runs[0].Status)
	}
}

func TestCreateReviewRun(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)
	attemptID := seedAttempt(t, db, taskID)

	feedback := "looks good"
	rr := &ReviewRun{
		AttemptID:      attemptID,
		ReviewerModel:  "openai/gpt-4o",
		ReviewerFamily: "openai",
		Verdict:        ReviewApprove,
		Feedback:       &feedback,
		CostUSD:        0.05,
	}
	id, err := db.CreateReviewRun(rr)
	if err != nil {
		t.Fatalf("CreateReviewRun: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive ID")
	}

	runs, err := db.ListReviewRuns(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("len = %d, want 1", len(runs))
	}
	if runs[0].Verdict != ReviewApprove {
		t.Errorf("Verdict = %q", runs[0].Verdict)
	}
}

func TestCreateArtifact(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)
	attemptID := seedAttempt(t, db, taskID)

	pathTo := "internal/auth/auth.go"
	sha := "abc123def456"
	size := int64(1024)
	art := &TaskArtifact{
		AttemptID:   attemptID,
		Operation:   ArtifactAdd,
		PathTo:      &pathTo,
		SHA256After: &sha,
		SizeAfter:   &size,
	}
	id, err := db.CreateArtifact(art)
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive ID")
	}

	arts, err := db.ListArtifacts(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 {
		t.Fatalf("len = %d, want 1", len(arts))
	}
	if arts[0].Operation != ArtifactAdd {
		t.Errorf("Operation = %q", arts[0].Operation)
	}
	if arts[0].PathTo == nil || *arts[0].PathTo != "internal/auth/auth.go" {
		t.Error("PathTo mismatch")
	}
}

// ptrInt64 returns a pointer to v.
func ptrInt64(v int64) *int64 {
	return &v
}
