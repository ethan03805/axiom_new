package state

import (
	"testing"
)

func TestCreateSession(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	s := &UISession{
		ID:        "sess-1",
		ProjectID: projID,
		Mode:      SessionBootstrap,
	}
	if err := db.CreateSession(s); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := db.GetSession("sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Mode != SessionBootstrap {
		t.Errorf("Mode = %q, want %q", got.Mode, SessionBootstrap)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestGetSessionNotFound(t *testing.T) {
	db := testDB(t)

	_, err := db.GetSession("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListSessionsByProject(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	for _, id := range []string{"s-1", "s-2"} {
		s := &UISession{ID: id, ProjectID: projID, Mode: SessionBootstrap}
		if err := db.CreateSession(s); err != nil {
			t.Fatal(err)
		}
	}

	sessions, err := db.ListSessionsByProject(projID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Errorf("len = %d, want 2", len(sessions))
	}
}

func TestUpdateSessionActivity(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	s := &UISession{ID: "sess-act", ProjectID: projID, Mode: SessionBootstrap}
	if err := db.CreateSession(s); err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateSessionActivity("sess-act"); err != nil {
		t.Fatalf("UpdateSessionActivity: %v", err)
	}

	got, _ := db.GetSession("sess-act")
	if got.LastActiveAt.IsZero() {
		t.Error("LastActiveAt should be set")
	}
}

func TestAddMessage(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	s := &UISession{ID: "sess-msg", ProjectID: projID, Mode: SessionBootstrap}
	if err := db.CreateSession(s); err != nil {
		t.Fatal(err)
	}

	msg := &UIMessage{
		SessionID: "sess-msg",
		Seq:       1,
		Role:      "user",
		Kind:      "user",
		Content:   "Build me an API",
	}
	id, err := db.AddMessage(msg)
	if err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive ID")
	}

	msgs, err := db.GetMessages("sess-msg")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len = %d, want 1", len(msgs))
	}
	if msgs[0].Content != "Build me an API" {
		t.Errorf("Content = %q", msgs[0].Content)
	}
	if msgs[0].Role != "user" {
		t.Errorf("Role = %q", msgs[0].Role)
	}
}

func TestMessageSequenceUniqueness(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	s := &UISession{ID: "sess-dup", ProjectID: projID, Mode: SessionBootstrap}
	if err := db.CreateSession(s); err != nil {
		t.Fatal(err)
	}

	msg1 := &UIMessage{SessionID: "sess-dup", Seq: 1, Role: "user", Kind: "user", Content: "first"}
	if _, err := db.AddMessage(msg1); err != nil {
		t.Fatal(err)
	}

	msg2 := &UIMessage{SessionID: "sess-dup", Seq: 1, Role: "user", Kind: "user", Content: "duplicate"}
	_, err := db.AddMessage(msg2)
	if err == nil {
		t.Error("expected error on duplicate (session_id, seq)")
	}
}

func TestAddSessionSummary(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	s := &UISession{ID: "sess-sum", ProjectID: projID, Mode: SessionExecution}
	if err := db.CreateSession(s); err != nil {
		t.Fatal(err)
	}

	sum := &UISessionSummary{
		SessionID:   "sess-sum",
		SummaryKind: "transcript_compaction",
		Content:     "User requested API implementation...",
	}
	id, err := db.AddSessionSummary(sum)
	if err != nil {
		t.Fatalf("AddSessionSummary: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive ID")
	}
}

func TestAddInputHistory(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	entry := &UIInputHistory{
		ProjectID: projID,
		InputMode: "prompt",
		Content:   "Build me a REST API",
	}
	id, err := db.AddInputHistory(entry)
	if err != nil {
		t.Fatalf("AddInputHistory: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive ID")
	}
}

// --- Phase 15: Additional session DB methods ---

func TestUpdateSessionMode(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	s := &UISession{ID: "sess-mode", ProjectID: projID, Mode: SessionBootstrap}
	if err := db.CreateSession(s); err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateSessionMode("sess-mode", SessionExecution); err != nil {
		t.Fatalf("UpdateSessionMode: %v", err)
	}

	got, err := db.GetSession("sess-mode")
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != SessionExecution {
		t.Errorf("Mode = %q, want %q", got.Mode, SessionExecution)
	}
}

func TestUpdateSessionModeNotFound(t *testing.T) {
	db := testDB(t)

	err := db.UpdateSessionMode("nonexistent", SessionExecution)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateSessionRunID(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	s := &UISession{ID: "sess-run", ProjectID: projID, Mode: SessionBootstrap}
	if err := db.CreateSession(s); err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateSessionRunID("sess-run", &runID); err != nil {
		t.Fatalf("UpdateSessionRunID: %v", err)
	}

	got, err := db.GetSession("sess-run")
	if err != nil {
		t.Fatal(err)
	}
	if got.RunID == nil || *got.RunID != runID {
		t.Errorf("RunID = %v, want %q", got.RunID, runID)
	}
}

func TestUpdateSessionRunIDNotFound(t *testing.T) {
	db := testDB(t)

	runID := "run-test"
	err := db.UpdateSessionRunID("nonexistent", &runID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetSessionSummaries(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	s := &UISession{ID: "sess-sums", ProjectID: projID, Mode: SessionExecution}
	if err := db.CreateSession(s); err != nil {
		t.Fatal(err)
	}

	for i, content := range []string{"Summary 1", "Summary 2"} {
		sum := &UISessionSummary{
			SessionID:   "sess-sums",
			SummaryKind: "transcript_compaction",
			Content:     content,
		}
		_, err := db.AddSessionSummary(sum)
		if err != nil {
			t.Fatalf("AddSessionSummary %d: %v", i, err)
		}
	}

	sums, err := db.GetSessionSummaries("sess-sums")
	if err != nil {
		t.Fatalf("GetSessionSummaries: %v", err)
	}
	if len(sums) != 2 {
		t.Fatalf("len = %d, want 2", len(sums))
	}
	if sums[0].Content != "Summary 1" {
		t.Errorf("first summary = %q", sums[0].Content)
	}
	if sums[1].Content != "Summary 2" {
		t.Errorf("second summary = %q", sums[1].Content)
	}
}

func TestGetSessionSummariesEmpty(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	s := &UISession{ID: "sess-nosums", ProjectID: projID, Mode: SessionBootstrap}
	if err := db.CreateSession(s); err != nil {
		t.Fatal(err)
	}

	sums, err := db.GetSessionSummaries("sess-nosums")
	if err != nil {
		t.Fatalf("GetSessionSummaries: %v", err)
	}
	if len(sums) != 0 {
		t.Errorf("expected 0 summaries, got %d", len(sums))
	}
}

func TestGetInputHistoryByProject(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	for i, content := range []string{"first", "second", "third"} {
		entry := &UIInputHistory{
			ProjectID: projID,
			InputMode: "prompt",
			Content:   content,
		}
		if _, err := db.AddInputHistory(entry); err != nil {
			t.Fatalf("AddInputHistory %d: %v", i, err)
		}
	}

	history, err := db.GetInputHistoryByProject(projID, 2)
	if err != nil {
		t.Fatalf("GetInputHistoryByProject: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("len = %d, want 2", len(history))
	}
	// Most recent first
	if history[0].Content != "third" {
		t.Errorf("first entry = %q, want %q", history[0].Content, "third")
	}
	if history[1].Content != "second" {
		t.Errorf("second entry = %q, want %q", history[1].Content, "second")
	}
}

func TestGetMessageCount(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	s := &UISession{ID: "sess-count", ProjectID: projID, Mode: SessionBootstrap}
	if err := db.CreateSession(s); err != nil {
		t.Fatal(err)
	}

	// Initially zero
	count, err := db.GetMessageCount("sess-count")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	// Add messages
	for i := 1; i <= 3; i++ {
		msg := &UIMessage{SessionID: "sess-count", Seq: i, Role: "user", Kind: "user", Content: "msg"}
		if _, err := db.AddMessage(msg); err != nil {
			t.Fatal(err)
		}
	}

	count, err = db.GetMessageCount("sess-count")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestDeleteMessagesBySessionBefore(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	s := &UISession{ID: "sess-del", ProjectID: projID, Mode: SessionExecution}
	if err := db.CreateSession(s); err != nil {
		t.Fatal(err)
	}

	// Add 5 messages
	for i := 1; i <= 5; i++ {
		msg := &UIMessage{SessionID: "sess-del", Seq: i, Role: "user", Kind: "user", Content: "msg"}
		if _, err := db.AddMessage(msg); err != nil {
			t.Fatal(err)
		}
	}

	// Delete messages with seq < 3 (should delete seq 1 and 2)
	deleted, err := db.DeleteMessagesBySessionBefore("sess-del", 3)
	if err != nil {
		t.Fatalf("DeleteMessagesBySessionBefore: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	msgs, err := db.GetMessages("sess-del")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("remaining = %d, want 3", len(msgs))
	}
	if msgs[0].Seq != 3 {
		t.Errorf("first remaining seq = %d, want 3", msgs[0].Seq)
	}
}

func TestGetLatestSessionByProject(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)

	// No sessions — should get ErrNotFound
	_, err := db.GetLatestSessionByProject(projID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	// Create two sessions and update activity on the first
	s1 := &UISession{ID: "sess-old", ProjectID: projID, Mode: SessionBootstrap}
	s2 := &UISession{ID: "sess-new", ProjectID: projID, Mode: SessionExecution}
	if err := db.CreateSession(s1); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(s2); err != nil {
		t.Fatal(err)
	}

	// Touch s2 to make it more recent
	if err := db.UpdateSessionActivity("sess-new"); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetLatestSessionByProject(projID)
	if err != nil {
		t.Fatalf("GetLatestSessionByProject: %v", err)
	}
	if got.ID != "sess-new" {
		t.Errorf("ID = %q, want sess-new", got.ID)
	}
}
