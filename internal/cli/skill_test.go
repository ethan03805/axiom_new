package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillGenerateCommandWritesArtifacts(t *testing.T) {
	application := testApp(t)
	application.Config.API.Port = 4777

	skillAppOverride = application
	t.Cleanup(func() { skillAppOverride = nil })

	verbose := false
	cmd := SkillCmd(&verbose)
	output, err := executeCmd(cmd, "generate", "--runtime", "claude-code")
	if err != nil {
		t.Fatalf("skill generate command error: %v", err)
	}

	if !strings.Contains(output, "Phase 17") {
		t.Fatalf("expected phase marker in output, got: %s", output)
	}
	if !strings.Contains(output, "Generated") {
		t.Fatalf("expected generation summary in output, got: %s", output)
	}

	// Fix A: surfacing the session-start hook caveat so users do not assume the
	// guard hook is live in the current Claude Code session.
	if !strings.Contains(output, "WARNING: The guard hook is active only in NEW Claude Code sessions.") {
		t.Fatalf("expected guard-hook session warning in output, got: %s", output)
	}
	if !strings.Contains(output, "restart Claude Code") {
		t.Fatalf("expected 'restart Claude Code' guidance in output, got: %s", output)
	}

	if _, err := os.Stat(filepath.Join(application.ProjectRoot, ".claude", "CLAUDE.md")); err != nil {
		t.Fatalf("expected generated CLAUDE.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(application.ProjectRoot, ".claude", "settings.json")); err != nil {
		t.Fatalf("expected generated Claude settings: %v", err)
	}
}

func TestSkillGenerateCommandOmitsClaudeHookWarningForOtherRuntimes(t *testing.T) {
	application := testApp(t)
	application.Config.API.Port = 4778

	skillAppOverride = application
	t.Cleanup(func() { skillAppOverride = nil })

	verbose := false
	cmd := SkillCmd(&verbose)
	output, err := executeCmd(cmd, "generate", "--runtime", "claw")
	if err != nil {
		t.Fatalf("skill generate command error: %v", err)
	}

	if strings.Contains(output, "guard hook is active") {
		t.Fatalf("did not expect Claude Code hook warning for claw runtime, got: %s", output)
	}
}

func TestSkillGenerateCommandRejectsUnknownRuntime(t *testing.T) {
	application := testApp(t)

	skillAppOverride = application
	t.Cleanup(func() { skillAppOverride = nil })

	verbose := false
	cmd := SkillCmd(&verbose)
	_, err := executeCmd(cmd, "generate", "--runtime", "bogus")
	if err == nil {
		t.Fatal("expected invalid runtime error")
	}
	if !strings.Contains(err.Error(), "invalid runtime") {
		t.Fatalf("expected invalid runtime error, got: %v", err)
	}
}
