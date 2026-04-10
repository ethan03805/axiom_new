package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

// validSRSForCLITest returns a minimal SRS string that passes
// srs.ValidateStructure. It intentionally duplicates the structure used in
// engine tests so the CLI test has no engine-internal dependency.
func validSRSForCLITest() string {
	return `# SRS: CLI Stdin Test Project

## 1. Architecture

### 1.1 System Overview
CLI stdin smoke test.

## 2. Requirements & Constraints

### 2.1 Functional Requirements
- FR-001: Submit SRS from stdin.

## 3. Test Strategy

### 3.1 Unit Testing
Go unit tests.

## 4. Acceptance Criteria

### 4.1 Per-Component Criteria
- [ ] AC-001: stdin path works.
`
}

func TestSRSSubmitAction_ReadsFromStdin(t *testing.T) {
	application, proj := testAppWithProject(t)

	// Ensure .axiom/ directories exist for SRS file operations.
	for _, sub := range []string{"", "eco"} {
		dir := filepath.Join(application.ProjectRoot, ".axiom", sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	run, err := application.Engine.CreateRun(engine.RunOptions{
		ProjectID:  proj.ID,
		BaseBranch: "main",
		BudgetUSD:  5.0,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.Status != state.RunDraftSRS {
		t.Fatalf("new run status = %q, want %q", run.Status, state.RunDraftSRS)
	}

	content := validSRSForCLITest()
	stdin := strings.NewReader(content)
	buf := new(bytes.Buffer)

	if err := srsSubmitAction(application, run.ID, "-", stdin, buf); err != nil {
		t.Fatalf("srsSubmitAction(stdin): %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "SRS submitted") {
		t.Errorf("expected 'SRS submitted' in output, got: %s", output)
	}
	if !strings.Contains(output, run.ID) {
		t.Errorf("expected run id %q in output, got: %s", run.ID, output)
	}

	// Verify the engine now reports the run as awaiting approval, which
	// confirms SubmitSRS received the stdin-supplied content.
	got, err := application.DB.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunAwaitingSRSApproval {
		t.Errorf("run status = %q, want %q", got.Status, state.RunAwaitingSRSApproval)
	}

	// And the stored draft should match what we fed on stdin.
	draft, err := application.Engine.ReadSRSDraft(run.ID)
	if err != nil {
		t.Fatalf("ReadSRSDraft: %v", err)
	}
	if draft != content {
		t.Errorf("persisted draft does not match stdin content")
	}
}

func TestSRSSubmitAction_ReadsFromFile(t *testing.T) {
	application, proj := testAppWithProject(t)

	for _, sub := range []string{"", "eco"} {
		dir := filepath.Join(application.ProjectRoot, ".axiom", sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	run, err := application.Engine.CreateRun(engine.RunOptions{
		ProjectID:  proj.ID,
		BaseBranch: "main",
		BudgetUSD:  5.0,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	srsPath := filepath.Join(t.TempDir(), "draft.md")
	content := validSRSForCLITest()
	if err := os.WriteFile(srsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	buf := new(bytes.Buffer)
	// Stdin must be ignored when a real file path is supplied.
	stdin := strings.NewReader("this must not be read")
	if err := srsSubmitAction(application, run.ID, srsPath, stdin, buf); err != nil {
		t.Fatalf("srsSubmitAction(file): %v", err)
	}

	got, err := application.DB.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.RunAwaitingSRSApproval {
		t.Errorf("run status = %q, want %q", got.Status, state.RunAwaitingSRSApproval)
	}
}
