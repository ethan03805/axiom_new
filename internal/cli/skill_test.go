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

	if _, err := os.Stat(filepath.Join(application.ProjectRoot, ".claude", "CLAUDE.md")); err != nil {
		t.Fatalf("expected generated CLAUDE.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(application.ProjectRoot, ".claude", "settings.json")); err != nil {
		t.Fatalf("expected generated Claude settings: %v", err)
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
