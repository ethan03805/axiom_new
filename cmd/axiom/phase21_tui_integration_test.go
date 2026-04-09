package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/cli"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/openaxiom/axiom/internal/testfixtures"
)

// TestTUIPrompt_CreatesRunViaCompositionRoot is the Issue 08
// composition-root regression guard. Prior to the fix, typing a prompt
// in the TUI's bootstrap mode silently swallowed the input and never
// called Engine.StartRun. This test walks the full
// `init → tui --prompt` pipeline through the real cobra composition
// root, then asserts — by reading the project DB directly — that a
// project_runs row was created with StartSource="tui" and the expected
// InitialPrompt.
//
// Any future refactor that detaches the TUI surface from Engine.StartRun
// will fail here before it reaches the unit tests.
func TestTUIPrompt_CreatesRunViaCompositionRoot(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	withWorkingDir(t, repoDir, func() {
		verbose = false

		// Initialize the project.
		initOut := executeCobra(t, initCmd(), "--name", "Fixture TUI")
		if !strings.Contains(initOut, "Axiom project initialized") {
			t.Fatalf("init output missing success message:\n%s", initOut)
		}
		patchConfigWithTestInferenceProvider(t, repoDir)
		// Commit the .axiom/ init changes so StartRun's clean-tree
		// check passes (Architecture §28.2).
		gitCommitAll(t, repoDir, "axiom init")

		// Drive the TUI's bootstrap-mode write path via the new
		// --prompt flag. This routes through runPromptMode →
		// PlainRenderer.RunOnce → Engine.StartRun(Source="tui").
		const prompt = "Build a REST API from the TUI"
		tuiOut := executeCobra(t, cli.TUICmd(&verbose), "--prompt", prompt)
		if !strings.Contains(tuiOut, "Run created") {
			t.Fatalf("tui --prompt output missing 'Run created':\n%s", tuiOut)
		}

		// Open the project DB directly and assert a run row exists
		// with the expected source and prompt.
		db, err := state.Open(project.DBPath(repoDir), slog.Default())
		if err != nil {
			t.Fatalf("state.Open: %v", err)
		}
		defer db.Close()

		projSlug := project.Slugify("Fixture TUI")
		runs, err := db.ListRunsByProject(projSlug)
		if err != nil {
			t.Fatalf("ListRunsByProject: %v", err)
		}
		if len(runs) != 1 {
			t.Fatalf("expected exactly 1 run, got %d", len(runs))
		}
		run := runs[0]
		if run.InitialPrompt != prompt {
			t.Errorf("run.InitialPrompt = %q, want %q", run.InitialPrompt, prompt)
		}
		if run.StartSource != "tui" {
			t.Errorf("run.StartSource = %q, want %q", run.StartSource, "tui")
		}
		if run.Status != state.RunDraftSRS {
			t.Errorf("run.Status = %q, want draft_srs", run.Status)
		}
	})
}

// TestTUIPrompt_RefusesDirtyTree asserts that the TUI --prompt path
// honors the clean-tree contract without a bypass. This is the
// composition-root mirror of
// internal/tui.TestSubmitInput_BootstrapMode_DirtyTreeReportsError and
// catches any refactor that inadvertently exposes --allow-dirty via the
// TUI (explicitly forbidden by Issue 08 §4.1).
func TestTUIPrompt_RefusesDirtyTree(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	withWorkingDir(t, repoDir, func() {
		verbose = false

		// Init but do NOT commit — .axiom/ remains uncommitted so the
		// working tree is dirty.
		executeCobra(t, initCmd(), "--name", "Fixture TUI")
		patchConfigWithTestInferenceProvider(t, repoDir)

		output, err := executeCobraExpectError(t, cli.TUICmd(&verbose),
			"--prompt", "Dirty submission should be refused")
		if err == nil {
			t.Fatalf("tui --prompt should fail on a dirty tree; output:\n%s", output)
		}
		if !strings.Contains(err.Error(), "working tree has uncommitted changes") {
			t.Errorf("error should name the dirty-tree condition, got %q", err.Error())
		}
	})
}

func TestTUIPlain_InheritsGlobalOpenRouterKeyAfterInit(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	writeGlobalOpenRouterConfig(t, home, "sk-global-tui-key")

	withWorkingDir(t, repoDir, func() {
		verbose = false

		initOut := executeCobra(t, initCmd(), "--name", "Fixture TUI Global")
		if !strings.Contains(initOut, "Axiom project initialized") {
			t.Fatalf("init output missing success message:\n%s", initOut)
		}

		tuiOut := executeCobra(t, cli.TUICmd(&verbose), "--plain")
		if !strings.Contains(tuiOut, "Axiom") {
			t.Fatalf("tui --plain output missing startup frame:\n%s", tuiOut)
		}
	})
}
