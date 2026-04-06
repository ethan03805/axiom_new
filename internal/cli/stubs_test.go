package cli

import (
	"strings"
	"testing"
)

// Stub commands must exist and return informational messages about their
// planned implementation phase. They should not panic or return errors.
// Phase 15 commands (TUI, Session) have been implemented and have their
// own tests in session_test.go.

func TestAPICmd_Exists(t *testing.T) {
	verbose := false
	cmd := APICmd(&verbose)
	if cmd == nil {
		t.Fatal("APICmd returned nil")
	}
}

func TestAPIStartStub(t *testing.T) {
	verbose := false
	cmd := APICmd(&verbose)

	output, err := executeCmd(cmd, "start")
	if err != nil {
		t.Fatalf("api start error: %v", err)
	}

	if !strings.Contains(output, "Phase 16") && !strings.Contains(output, "not yet implemented") {
		t.Errorf("expected stub message, got: %s", output)
	}
}

func TestAPIStopStub(t *testing.T) {
	verbose := false
	cmd := APICmd(&verbose)

	output, err := executeCmd(cmd, "stop")
	if err != nil {
		t.Fatalf("api stop error: %v", err)
	}

	if !strings.Contains(output, "Phase 16") && !strings.Contains(output, "not yet implemented") {
		t.Errorf("expected stub message, got: %s", output)
	}
}

func TestAPITokenSubcommands(t *testing.T) {
	verbose := false
	cmd := APICmd(&verbose)

	for _, sub := range []string{"generate", "list"} {
		output, err := executeCmd(cmd, "token", sub)
		if err != nil {
			t.Fatalf("api token %s error: %v", sub, err)
		}
		if !strings.Contains(output, "Phase 16") && !strings.Contains(output, "not yet implemented") {
			t.Errorf("api token %s: expected stub message, got: %s", sub, output)
		}
		// Reset args for next iteration
		cmd.SetArgs(nil)
	}
}

func TestAPITokenRevokeStub(t *testing.T) {
	verbose := false
	cmd := APICmd(&verbose)

	output, err := executeCmd(cmd, "token", "revoke", "some-token-id")
	if err != nil {
		t.Fatalf("api token revoke error: %v", err)
	}

	if !strings.Contains(output, "Phase 16") && !strings.Contains(output, "not yet implemented") {
		t.Errorf("expected stub message, got: %s", output)
	}
}

func TestTunnelCmd_Exists(t *testing.T) {
	verbose := false
	cmd := TunnelCmd(&verbose)
	if cmd == nil {
		t.Fatal("TunnelCmd returned nil")
	}
}

func TestTunnelStartStub(t *testing.T) {
	verbose := false
	cmd := TunnelCmd(&verbose)

	output, err := executeCmd(cmd, "start")
	if err != nil {
		t.Fatalf("tunnel start error: %v", err)
	}

	if !strings.Contains(output, "Phase 16") && !strings.Contains(output, "not yet implemented") {
		t.Errorf("expected stub message, got: %s", output)
	}
}

func TestTunnelStopStub(t *testing.T) {
	verbose := false
	cmd := TunnelCmd(&verbose)

	output, err := executeCmd(cmd, "stop")
	if err != nil {
		t.Fatalf("tunnel stop error: %v", err)
	}

	if !strings.Contains(output, "Phase 16") && !strings.Contains(output, "not yet implemented") {
		t.Errorf("expected stub message, got: %s", output)
	}
}

func TestSkillCmd_Exists(t *testing.T) {
	verbose := false
	cmd := SkillCmd(&verbose)
	if cmd == nil {
		t.Fatal("SkillCmd returned nil")
	}
}

func TestSkillGenerateStub(t *testing.T) {
	verbose := false
	cmd := SkillCmd(&verbose)

	output, err := executeCmd(cmd, "generate", "--runtime", "claw")
	if err != nil {
		t.Fatalf("skill generate error: %v", err)
	}

	if !strings.Contains(output, "Phase 17") && !strings.Contains(output, "not yet implemented") {
		t.Errorf("expected stub message, got: %s", output)
	}
}

func TestDoctorCmd_Exists(t *testing.T) {
	verbose := false
	cmd := DoctorCmd(&verbose)
	if cmd == nil {
		t.Fatal("DoctorCmd returned nil")
	}
}

func TestDoctorStub(t *testing.T) {
	verbose := false
	cmd := DoctorCmd(&verbose)

	output, err := executeCmd(cmd)
	if err != nil {
		t.Fatalf("doctor error: %v", err)
	}

	if !strings.Contains(output, "Phase 19") && !strings.Contains(output, "not yet implemented") {
		t.Errorf("expected stub message, got: %s", output)
	}
}
