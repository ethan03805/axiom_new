package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/cli"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/testfixtures"
	"github.com/spf13/cobra"
)

func TestCLIInitRunStatusFlow_ExistingProjectFixture(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	withWorkingDir(t, repoDir, func() {
		verbose = false

		output := executeCobra(t, initCmd(), "--name", "Fixture Existing")
		if !strings.Contains(output, "Axiom project initialized") {
			t.Fatalf("init output missing success message:\n%s", output)
		}

		statusOutput := executeCobra(t, statusCmd())
		if !strings.Contains(statusOutput, "idle (no active run)") {
			t.Fatalf("status output missing idle state:\n%s", statusOutput)
		}

		// Commit the .axiom/ init changes so the working tree is clean
		// (StartRun validates clean tree per architecture Section 28.2)
		gitCommitAll(t, repoDir, "axiom init")

		runOutput := executeCobra(t, cli.RunCmd(&verbose), "Build the first feature")
		if !strings.Contains(runOutput, "Run created") {
			t.Fatalf("run output missing creation summary:\n%s", runOutput)
		}
		if !strings.Contains(runOutput, "external orchestrator") {
			t.Fatalf("run output missing external orchestrator message:\n%s", runOutput)
		}
		if !strings.Contains(runOutput, "Build the first feature") {
			t.Fatalf("run output missing prompt:\n%s", runOutput)
		}

		statusAfterRun := executeCobra(t, statusCmd())
		if !strings.Contains(statusAfterRun, "draft_srs") {
			t.Fatalf("status after run missing draft_srs state:\n%s", statusAfterRun)
		}
		if !strings.Contains(statusAfterRun, "external") {
			t.Fatalf("status after run missing orchestrator mode:\n%s", statusAfterRun)
		}
	})
}

func TestCLIInitDefaultsNameFromGreenfieldDirectory(t *testing.T) {
	repoDir, err := testfixtures.Materialize("greenfield")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	withWorkingDir(t, repoDir, func() {
		verbose = false

		output := executeCobra(t, initCmd())
		expectedSlug := project.Slugify(filepath.Base(repoDir))
		if !strings.Contains(output, "Slug:    "+expectedSlug) {
			t.Fatalf("init output missing derived slug %q:\n%s", expectedSlug, output)
		}

		if _, err := os.Stat(filepath.Join(repoDir, ".axiom", "config.toml")); err != nil {
			t.Fatalf("config.toml not written: %v", err)
		}

		statusOutput := executeCobra(t, statusCmd())
		if !strings.Contains(statusOutput, "idle (no active run)") {
			t.Fatalf("status output missing idle state:\n%s", statusOutput)
		}
	})
}

func executeCobra(t *testing.T, cmd *cobra.Command, args ...string) string {
	t.Helper()

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(%v): %v\noutput:\n%s", args, err, buf.String())
	}

	return buf.String()
}

// gitCommitAll stages all changes and commits them in the given directory.
func gitCommitAll(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-m", msg, "--allow-empty"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, string(out))
		}
	}
}

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()

	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	fn()
}
