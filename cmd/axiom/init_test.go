package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitCmd_AutoInitsGitInGreenfieldDir verifies GitHub Issue #2: when
// 'axiom init' is run in a directory that is not a git repository, it
// should automatically run `git init -b main` before initializing the
// .axiom/ project state, so downstream commands like `axiom run` (which
// require a git work tree) succeed without manual intervention.
func TestInitCmd_AutoInitsGitInGreenfieldDir(t *testing.T) {
	dir := t.TempDir()

	// Precondition: no .git directory.
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected no .git before init, stat err = %v", err)
	}

	withWorkingDir(t, dir, func() {
		verbose = false

		output := executeCobra(t, initCmd(), "--name", "Greenfield Auto Git")
		if !strings.Contains(output, "Initialized empty git repository") {
			t.Errorf("init output missing auto-init notice:\n%s", output)
		}
		if !strings.Contains(output, "Axiom project initialized") {
			t.Errorf("init output missing success message:\n%s", output)
		}
	})

	// Postcondition: .git now exists and HEAD points at main.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf(".git missing after init: %v", err)
	}
	if got := revParseHead(t, dir); got != "main" {
		t.Errorf("default branch after init: got %q, want main", got)
	}

	// .axiom/ should also be present.
	if _, err := os.Stat(filepath.Join(dir, ".axiom", "config.toml")); err != nil {
		t.Errorf("config.toml missing after init: %v", err)
	}
}

// TestInitCmd_ReportsExistingRepo verifies that running 'axiom init' inside
// an existing git repository prints the detection notice and does NOT try
// to re-initialize the repo.
func TestInitCmd_ReportsExistingRepo(t *testing.T) {
	dir := t.TempDir()
	gitInitMain(t, dir)

	withWorkingDir(t, dir, func() {
		verbose = false

		output := executeCobra(t, initCmd(), "--name", "Existing Repo")
		if !strings.Contains(output, "Git repository detected:") {
			t.Errorf("init output missing detection notice:\n%s", output)
		}
		if strings.Contains(output, "Initialized empty git repository") {
			t.Errorf("init should NOT re-initialize existing repo:\n%s", output)
		}
		if !strings.Contains(output, "Axiom project initialized") {
			t.Errorf("init output missing success message:\n%s", output)
		}
	})
}

// TestInitCmd_NoGitFlagSkipsAutoInitAndWarns verifies --no-git: the flag
// should suppress the auto `git init`, emit a warning about downstream
// commands failing, and still complete the .axiom/ scaffold.
func TestInitCmd_NoGitFlagSkipsAutoInitAndWarns(t *testing.T) {
	dir := t.TempDir()

	withWorkingDir(t, dir, func() {
		verbose = false

		output := executeCobra(t, initCmd(), "--name", "No Git Mode", "--no-git")
		if !strings.Contains(output, "not a git repository and --no-git was set") {
			t.Errorf("init output missing --no-git warning:\n%s", output)
		}
		if strings.Contains(output, "Initialized empty git repository") {
			t.Errorf("init with --no-git should NOT auto-initialize git:\n%s", output)
		}
		if !strings.Contains(output, "Axiom project initialized") {
			t.Errorf("init output missing success message:\n%s", output)
		}
	})

	// .git must NOT exist.
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Errorf("--no-git should leave .git absent, stat err = %v", err)
	}
	// .axiom/ should still be scaffolded.
	if _, err := os.Stat(filepath.Join(dir, ".axiom", "config.toml")); err != nil {
		t.Errorf("config.toml missing after --no-git init: %v", err)
	}
}

// gitInitMain initializes a bare git repo with main as the default branch,
// matching the fallback pattern used by internal/testfixtures.
func gitInitMain(t *testing.T, dir string) {
	t.Helper()
	if err := runGit(dir, "init", "-b", "main"); err != nil {
		if err := runGit(dir, "init"); err != nil {
			t.Fatalf("git init: %v", err)
		}
		if err := runGit(dir, "branch", "-M", "main"); err != nil {
			t.Fatalf("git branch -M main: %v", err)
		}
	}
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	return cmd.Run()
}

func revParseHead(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("symbolic-ref: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}
