package state

import (
	"testing"
)

func TestCreateEvent(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	taskID := "task-ev"
	agentType := "meeseeks"
	details := `{"message":"task started"}`
	ev := &Event{
		RunID:     runID,
		EventType: "task_started",
		TaskID:    &taskID,
		AgentType: &agentType,
		Details:   &details,
	}
	id, err := db.CreateEvent(ev)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive ID")
	}
}

func TestListEventsByRun(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	for _, etype := range []string{"run_started", "task_started", "task_completed"} {
		ev := &Event{RunID: runID, EventType: etype}
		if _, err := db.CreateEvent(ev); err != nil {
			t.Fatal(err)
		}
	}

	events, err := db.ListEventsByRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Errorf("len = %d, want 3", len(events))
	}
}

func TestListEventsByType(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	for _, etype := range []string{"task_started", "task_completed", "task_started"} {
		ev := &Event{RunID: runID, EventType: etype}
		if _, err := db.CreateEvent(ev); err != nil {
			t.Fatal(err)
		}
	}

	events, err := db.ListEventsByType(runID, "task_started")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Errorf("len = %d, want 2", len(events))
	}
}

func TestCreateCostLog(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	input := int64(500)
	output := int64(200)
	entry := &CostLogEntry{
		RunID:        runID,
		AgentType:    "meeseeks",
		ModelID:      "anthropic/claude-4-sonnet",
		InputTokens:  &input,
		OutputTokens: &output,
		CostUSD:      0.015,
	}
	id, err := db.CreateCostLog(entry)
	if err != nil {
		t.Fatalf("CreateCostLog: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive ID")
	}
}

func TestListCostLogByRun(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	for _, cost := range []float64{0.01, 0.02, 0.03} {
		entry := &CostLogEntry{
			RunID: runID, AgentType: "meeseeks",
			ModelID: "model", CostUSD: cost,
		}
		if _, err := db.CreateCostLog(entry); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := db.ListCostLogByRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("len = %d, want 3", len(entries))
	}
}

func TestTotalCostByRun(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	for _, cost := range []float64{0.01, 0.02, 0.03} {
		entry := &CostLogEntry{
			RunID: runID, AgentType: "meeseeks",
			ModelID: "model", CostUSD: cost,
		}
		if _, err := db.CreateCostLog(entry); err != nil {
			t.Fatal(err)
		}
	}

	total, err := db.TotalCostByRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	// 0.01 + 0.02 + 0.03 = 0.06
	if total < 0.059 || total > 0.061 {
		t.Errorf("total = %v, want ~0.06", total)
	}
}

func TestTotalCostByRun_Empty(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	total, err := db.TotalCostByRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("total = %v, want 0", total)
	}
}
