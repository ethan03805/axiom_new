package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/cli"
	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/testfixtures"
	"github.com/spf13/cobra"
)

// patchConfigWithTestInferenceProvider rewrites the freshly-initialized
// .axiom/config.toml to set a fake OpenRouter API key so the production
// inference-plane health check (Issue 07 §4.3) passes. CLI integration
// tests that exercise app.Open must call this after initCmd runs.
//
// The key itself is synthetic and never leaves the test host — the
// broker is constructed but its cloud provider is configured to point
// at a loopback address that is not actually contacted by these tests,
// which stop well before any real inference request.
func patchConfigWithTestInferenceProvider(t *testing.T, repoDir string) {
	t.Helper()

	cfgPath := filepath.Join(repoDir, project.AxiomDir, project.ConfigFile)
	existing, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	// Parse the existing (default) config so we preserve its project
	// name / slug, then overlay the inference fields we care about.
	var cfg config.Config
	if err := unmarshalTOML(existing, &cfg); err != nil {
		// Fall back to a fresh default if the project name is unknown
		// to the caller; project.Init always writes a valid TOML so
		// this path is a defensive no-op.
		cfg = config.Default("fixture", "fixture")
	}
	cfg.Inference.OpenRouterAPIKey = "sk-test-fake-key"
	cfg.Inference.OpenRouterBase = "http://127.0.0.1:1"
	cfg.Inference.TimeoutSeconds = 1
	cfg.BitNet.Enabled = false

	data, err := config.Marshal(&cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// unmarshalTOML is a tiny adapter so the helper above can reuse the
// config package's Load path without the full filesystem dance.
func unmarshalTOML(data []byte, out *config.Config) error {
	tmp, err := os.CreateTemp("", "axiom-cfg-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	loaded, err := config.LoadFile(tmp.Name())
	if err != nil {
		return err
	}
	*out = *loaded
	return nil
}

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
		patchConfigWithTestInferenceProvider(t, repoDir)

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

// TestCLIRun_SwitchesToWorkBranch asserts that `axiom run` actually
// switches the repo onto the axiom/<slug> work branch — the contract
// Architecture §23.1 mandates and Issue 06 found was broken at the
// runtime layer.
func TestCLIRun_SwitchesToWorkBranch(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	withWorkingDir(t, repoDir, func() {
		verbose = false

		executeCobra(t, initCmd(), "--name", "Fixture Existing")
		patchConfigWithTestInferenceProvider(t, repoDir)
		gitCommitAll(t, repoDir, "axiom init")

		executeCobra(t, cli.RunCmd(&verbose), "Build the first feature")

		got := gitCurrentBranch(t, repoDir)
		want := "axiom/" + project.Slugify("Fixture Existing")
		if got != want {
			t.Errorf("current branch after axiom run = %q, want %q", got, want)
		}
	})
}

// TestCLIRun_RefusesDirtyTree asserts that `axiom run` exits non-zero
// when the working tree has uncommitted changes (Architecture §28.2).
func TestCLIRun_RefusesDirtyTree(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	withWorkingDir(t, repoDir, func() {
		verbose = false

		// `axiom init` writes .axiom/ artifacts; we intentionally do NOT
		// commit them so the working tree is dirty when we try to run.
		executeCobra(t, initCmd(), "--name", "Fixture Existing")
		patchConfigWithTestInferenceProvider(t, repoDir)

		output, err := executeCobraExpectError(t, cli.RunCmd(&verbose), "Build something")
		if err == nil {
			t.Fatalf("axiom run should have failed on a dirty tree; output:\n%s", output)
		}
		if !strings.Contains(err.Error(), "working tree has uncommitted changes") {
			t.Errorf("error should name the dirty-tree condition, got %q", err.Error())
		}
	})
}

// TestCLIRun_AllowDirtyBypass asserts that --allow-dirty lets `axiom run`
// proceed even with a dirty working tree — the recovery escape hatch
// added by the Issue 06 fix.
func TestCLIRun_AllowDirtyBypass(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	withWorkingDir(t, repoDir, func() {
		verbose = false

		// Leave .axiom/ uncommitted on purpose.
		executeCobra(t, initCmd(), "--name", "Fixture Existing")
		patchConfigWithTestInferenceProvider(t, repoDir)

		executeCobra(t, cli.RunCmd(&verbose), "--allow-dirty", "Recover after crash")

		got := gitCurrentBranch(t, repoDir)
		want := "axiom/" + project.Slugify("Fixture Existing")
		if got != want {
			t.Errorf("current branch after --allow-dirty run = %q, want %q", got, want)
		}
	})
}

// TestCLICancel_CleansUpAndReturnsToBase is the single most important
// regression test for Issue 06. It exercises the full cancel protocol:
// start a run (switches to axiom/<slug>), seed an untracked file on the
// work branch, cancel the run, and assert that the cancel reverted the
// uncommitted change and returned the repo to main while preserving the
// axiom/<slug> branch (§23.4 — committed work is preserved).
func TestCLICancel_CleansUpAndReturnsToBase(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	withWorkingDir(t, repoDir, func() {
		verbose = false

		executeCobra(t, initCmd(), "--name", "Fixture Existing")
		patchConfigWithTestInferenceProvider(t, repoDir)
		gitCommitAll(t, repoDir, "axiom init")

		executeCobra(t, cli.RunCmd(&verbose), "Build the first feature")

		workBranch := "axiom/" + project.Slugify("Fixture Existing")
		if got := gitCurrentBranch(t, repoDir); got != workBranch {
			t.Fatalf("precondition: branch = %q, want %q", got, workBranch)
		}

		// Seed an untracked scratch file on the work branch. CancelCleanup
		// should scrub it via `git clean -fd`.
		scratchPath := filepath.Join(repoDir, "scratch.txt")
		if err := os.WriteFile(scratchPath, []byte("transient\n"), 0o644); err != nil {
			t.Fatalf("seed scratch file: %v", err)
		}

		executeCobra(t, cli.CancelCmd(&verbose))

		// After cancel: repo should be back on main.
		if got := gitCurrentBranch(t, repoDir); got != "main" {
			t.Errorf("after cancel: branch = %q, want main", got)
		}

		// Scratch file should be gone (git clean -fd).
		if _, err := os.Stat(scratchPath); !os.IsNotExist(err) {
			t.Errorf("scratch file should be removed by cancel cleanup; stat err = %v", err)
		}

		// Work branch should still exist — committed work is preserved.
		if !gitBranchExists(t, repoDir, workBranch) {
			t.Errorf("work branch %q should still exist after cancel (§23.4 preserves committed work)", workBranch)
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
		patchConfigWithTestInferenceProvider(t, repoDir)

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

// executeCobraExpectError runs a cobra command and returns the resulting
// error plus captured output, without failing the test. Used by the
// Issue 06 regression tests that need to assert dirty-tree refusal.
func executeCobraExpectError(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	err := cmd.Execute()
	return buf.String(), err
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

// gitCurrentBranch returns the current branch in the given directory.
func gitCurrentBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse: %s", string(out))
	}
	return strings.TrimSpace(string(out))
}

// gitBranchExists reports whether the given local branch exists.
func gitBranchExists(t *testing.T, dir, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--verify", "refs/heads/"+branch)
	return cmd.Run() == nil
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
