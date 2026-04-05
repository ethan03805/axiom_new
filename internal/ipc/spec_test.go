package ipc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTaskSpec(t *testing.T) {
	root := t.TempDir()
	dirs, err := CreateTaskDirs(root, "task-042")
	if err != nil {
		t.Fatal(err)
	}

	spec := TaskSpec{
		TaskID:       "task-042",
		BaseSnapshot: "abc123def",
		Objective:    "Implement user authentication handler",
		Context:      "### Symbol Context (tier: symbol)\nfunc Authenticate(token string) (*User, error)",
		InterfaceContract: "func Authenticate(token string) (*User, error)",
		Constraints: TaskConstraints{
			Language:      "Go 1.25",
			Style:         "standard library conventions",
			Dependencies:  "stdlib only",
			MaxFileLength: 500,
		},
		AcceptanceCriteria: []string{
			"Handler validates JWT tokens",
			"Returns 401 for invalid tokens",
			"Extracts user ID from claims",
		},
	}

	if err := WriteTaskSpec(dirs.Spec, spec); err != nil {
		t.Fatalf("WriteTaskSpec: %v", err)
	}

	// Verify file exists
	specPath := filepath.Join(dirs.Spec, "spec.md")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("reading spec: %v", err)
	}

	content := string(data)

	// Verify required sections from Architecture Section 10.3
	requiredSections := []string{
		"# TaskSpec: task-042",
		"## Base Snapshot",
		"abc123def",
		"## Objective",
		"Implement user authentication handler",
		"## Context",
		"## Interface Contract",
		"## Constraints",
		"Go 1.25",
		"## Acceptance Criteria",
		"Handler validates JWT tokens",
		"## Output Format",
		"/workspace/staging/",
		"manifest.json",
	}

	for _, s := range requiredSections {
		if !strings.Contains(content, s) {
			t.Errorf("spec missing required content: %q", s)
		}
	}
}

func TestWriteTaskSpecMinimal(t *testing.T) {
	root := t.TempDir()
	dirs, err := CreateTaskDirs(root, "task-min")
	if err != nil {
		t.Fatal(err)
	}

	spec := TaskSpec{
		TaskID:       "task-min",
		BaseSnapshot: "deadbeef",
		Objective:    "Rename variable",
	}

	if err := WriteTaskSpec(dirs.Spec, spec); err != nil {
		t.Fatalf("WriteTaskSpec minimal: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dirs.Spec, "spec.md"))
	content := string(data)

	if !strings.Contains(content, "# TaskSpec: task-min") {
		t.Error("missing task header")
	}
	if !strings.Contains(content, "deadbeef") {
		t.Error("missing base snapshot")
	}
}

func TestWriteReviewSpec(t *testing.T) {
	root := t.TempDir()
	dirs, err := CreateTaskDirs(root, "task-042")
	if err != nil {
		t.Fatal(err)
	}

	spec := ReviewSpec{
		TaskID:           "task-042",
		OriginalTaskSpec: "# TaskSpec: task-042\n## Objective\nBuild auth handler",
		MeeseeksOutput:   "```go\npackage auth\n\nfunc Handle() {}\n```",
		AutomatedCheckResults: "✅ Compilation: PASS\n✅ Linting: PASS\n✅ Unit Tests: PASS (12/12)",
		ReviewInstructions: "Evaluate the Meeseeks' output against the original TaskSpec.",
	}

	if err := WriteReviewSpec(dirs.Spec, spec); err != nil {
		t.Fatalf("WriteReviewSpec: %v", err)
	}

	specPath := filepath.Join(dirs.Spec, "spec.md")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("reading review spec: %v", err)
	}

	content := string(data)

	// Verify required sections from Architecture Section 11.7
	requiredSections := []string{
		"# ReviewSpec: task-042",
		"## Original TaskSpec",
		"Build auth handler",
		"## Meeseeks Output",
		"## Automated Check Results",
		"Compilation: PASS",
		"## Review Instructions",
		"### Verdict: APPROVE | REJECT",
		"### Criterion Evaluation",
		"### Feedback (if REJECT)",
	}

	for _, s := range requiredSections {
		if !strings.Contains(content, s) {
			t.Errorf("review spec missing required content: %q", s)
		}
	}
}

func TestWriteTaskSpecOverwrites(t *testing.T) {
	root := t.TempDir()
	dirs, err := CreateTaskDirs(root, "task-001")
	if err != nil {
		t.Fatal(err)
	}

	spec1 := TaskSpec{TaskID: "task-001", BaseSnapshot: "aaa", Objective: "First version"}
	spec2 := TaskSpec{TaskID: "task-001", BaseSnapshot: "bbb", Objective: "Second version"}

	if err := WriteTaskSpec(dirs.Spec, spec1); err != nil {
		t.Fatal(err)
	}
	if err := WriteTaskSpec(dirs.Spec, spec2); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dirs.Spec, "spec.md"))
	content := string(data)

	if strings.Contains(content, "First version") {
		t.Error("old spec content should be overwritten")
	}
	if !strings.Contains(content, "Second version") {
		t.Error("new spec content should be present")
	}
}

func TestTaskSpecOutputFormatInstructions(t *testing.T) {
	root := t.TempDir()
	dirs, err := CreateTaskDirs(root, "task-out")
	if err != nil {
		t.Fatal(err)
	}

	spec := TaskSpec{
		TaskID:       "task-out",
		BaseSnapshot: "abc",
		Objective:    "Test output format",
	}

	if err := WriteTaskSpec(dirs.Spec, spec); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dirs.Spec, "spec.md"))
	content := string(data)

	// Per Architecture Section 10.3, output format section is mandatory
	if !strings.Contains(content, "Write all output files to /workspace/staging/") {
		t.Error("missing output directory instruction")
	}
	if !strings.Contains(content, "manifest.json") {
		t.Error("missing manifest.json instruction")
	}
}
