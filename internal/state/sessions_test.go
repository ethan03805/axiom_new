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
