package cli

import (
	"strings"
	"testing"
)

// Phase 15: Tests for real session and TUI command implementations.

func TestSessionList_WithProject(t *testing.T) {
	application, proj := testAppWithProject(t)

	// Create a session through the manager
	mgr := newSessionManager(application)
	_, err := mgr.CreateSession(proj.ID)
	if err != nil {
		t.Fatal(err)
	}

	verbose := false
	cmd := SessionCmd(&verbose)

	// Inject the app for testing (use the appOverride pattern)
	sessionAppOverride = application
	defer func() { sessionAppOverride = nil }()

	output, err := executeCmd(cmd, "list")
	if err != nil {
		t.Fatalf("session list error: %v", err)
	}

	// Should show a session, not a stub message
	if strings.Contains(output, "not yet implemented") {
		t.Errorf("session list should be implemented, got stub: %s", output)
	}
	if !strings.Contains(output, "bootstrap") {
		t.Errorf("session list should show mode, got: %s", output)
	}
}

func TestSessionList_Empty(t *testing.T) {
	application, _ := testAppWithProject(t)

	verbose := false
	cmd := SessionCmd(&verbose)

	sessionAppOverride = application
	defer func() { sessionAppOverride = nil }()

	output, err := executeCmd(cmd, "list")
	if err != nil {
		t.Fatalf("session list error: %v", err)
	}

	if !strings.Contains(output, "No sessions") {
		t.Errorf("expected 'No sessions' message, got: %s", output)
	}
}

func TestSessionExport_WithMessages(t *testing.T) {
	application, proj := testAppWithProject(t)

	mgr := newSessionManager(application)
	sess, err := mgr.CreateSession(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AddTranscriptMessage(sess.ID, "user", "user", "Build an API"); err != nil {
		t.Fatal(err)
	}

	verbose := false
	cmd := SessionCmd(&verbose)

	sessionAppOverride = application
	defer func() { sessionAppOverride = nil }()

	output, err := executeCmd(cmd, "export", sess.ID)
	if err != nil {
		t.Fatalf("session export error: %v", err)
	}

	if !strings.Contains(output, "Build an API") {
		t.Errorf("export should contain message, got:\n%s", output)
	}
	if !strings.Contains(output, "Transcript") {
		t.Errorf("export should contain Transcript header, got:\n%s", output)
	}
}

func TestSessionResume_Exists(t *testing.T) {
	application, proj := testAppWithProject(t)

	mgr := newSessionManager(application)
	sess, err := mgr.CreateSession(proj.ID)
	if err != nil {
		t.Fatal(err)
	}

	verbose := false
	cmd := SessionCmd(&verbose)

	sessionAppOverride = application
	defer func() { sessionAppOverride = nil }()

	output, err := executeCmd(cmd, "resume", sess.ID)
	if err != nil {
		t.Fatalf("session resume error: %v", err)
	}

	if !strings.Contains(output, "Resumed") {
		t.Errorf("expected resume confirmation, got: %s", output)
	}
}

func TestSessionResume_NotFound(t *testing.T) {
	application, _ := testAppWithProject(t)

	verbose := false
	cmd := SessionCmd(&verbose)

	sessionAppOverride = application
	defer func() { sessionAppOverride = nil }()

	_, err := executeCmd(cmd, "resume", "nonexistent-id")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestTUICmd_HasPlainFlag(t *testing.T) {
	verbose := false
	cmd := TUICmd(&verbose)

	flag := cmd.Flags().Lookup("plain")
	if flag == nil {
		t.Error("tui command should have --plain flag")
	}
}

func TestTUICmd_PlainMode(t *testing.T) {
	application, _ := testAppWithProject(t)

	verbose := false
	cmd := TUICmd(&verbose)

	tuiAppOverride = application
	defer func() { tuiAppOverride = nil }()

	output, err := executeCmd(cmd, "--plain")
	if err != nil {
		t.Fatalf("tui --plain error: %v", err)
	}

	// Plain mode should render the startup frame
	if !strings.Contains(output, "test-project") {
		t.Errorf("plain TUI should show project name, got:\n%s", output)
	}
	if !strings.Contains(output, "bootstrap") {
		t.Errorf("plain TUI should show mode, got:\n%s", output)
	}
}
