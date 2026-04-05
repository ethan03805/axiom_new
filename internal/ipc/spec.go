package ipc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TaskSpec represents a self-contained spec delivered to a Meeseeks.
// Per Architecture Section 10.3.
type TaskSpec struct {
	TaskID             string
	BaseSnapshot       string
	Objective          string
	Context            string
	InterfaceContract  string
	Constraints        TaskConstraints
	AcceptanceCriteria []string
}

// TaskConstraints holds the constraint fields for a TaskSpec.
type TaskConstraints struct {
	Language      string
	Style         string
	Dependencies  string
	MaxFileLength int
}

// ReviewSpec represents the spec delivered to a reviewer.
// Per Architecture Section 11.7.
type ReviewSpec struct {
	TaskID                string
	OriginalTaskSpec      string
	MeeseeksOutput        string
	AutomatedCheckResults string
	ReviewInstructions    string
}

// WriteTaskSpec writes a TaskSpec as spec.md in the given directory.
// The format follows Architecture Section 10.3.
func WriteTaskSpec(specDir string, spec TaskSpec) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# TaskSpec: %s\n\n", spec.TaskID)

	fmt.Fprintf(&b, "## Base Snapshot\ngit_sha: %s\n\n", spec.BaseSnapshot)

	fmt.Fprintf(&b, "## Objective\n%s\n\n", spec.Objective)

	if spec.Context != "" {
		fmt.Fprintf(&b, "## Context\n%s\n\n", spec.Context)
	} else {
		fmt.Fprintf(&b, "## Context\n<No additional context required for this task.>\n\n")
	}

	if spec.InterfaceContract != "" {
		fmt.Fprintf(&b, "## Interface Contract\n%s\n\n", spec.InterfaceContract)
	} else {
		fmt.Fprintf(&b, "## Interface Contract\n<No specific interface contract.>\n\n")
	}

	fmt.Fprintf(&b, "## Constraints\n")
	if spec.Constraints.Language != "" {
		fmt.Fprintf(&b, "- Language: %s\n", spec.Constraints.Language)
	}
	if spec.Constraints.Style != "" {
		fmt.Fprintf(&b, "- Style: %s\n", spec.Constraints.Style)
	}
	if spec.Constraints.Dependencies != "" {
		fmt.Fprintf(&b, "- Dependencies: %s\n", spec.Constraints.Dependencies)
	}
	if spec.Constraints.MaxFileLength > 0 {
		fmt.Fprintf(&b, "- Max file length: %d lines\n", spec.Constraints.MaxFileLength)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## Acceptance Criteria\n")
	if len(spec.AcceptanceCriteria) > 0 {
		for _, ac := range spec.AcceptanceCriteria {
			fmt.Fprintf(&b, "- [ ] %s\n", ac)
		}
	} else {
		b.WriteString("<No specific acceptance criteria defined.>\n")
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## Output Format\nWrite all output files to /workspace/staging/\nInclude a manifest.json describing all file operations.\n")

	path := filepath.Join(specDir, "spec.md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("writing task spec: %w", err)
	}
	return nil
}

// WriteReviewSpec writes a ReviewSpec as spec.md in the given directory.
// The format follows Architecture Section 11.7.
func WriteReviewSpec(specDir string, spec ReviewSpec) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# ReviewSpec: %s\n\n", spec.TaskID)

	fmt.Fprintf(&b, "## Original TaskSpec\n%s\n\n", spec.OriginalTaskSpec)

	fmt.Fprintf(&b, "## Meeseeks Output\n%s\n\n", spec.MeeseeksOutput)

	fmt.Fprintf(&b, "## Automated Check Results\n%s\n\n", spec.AutomatedCheckResults)

	fmt.Fprintf(&b, "## Review Instructions\n")
	if spec.ReviewInstructions != "" {
		fmt.Fprintf(&b, "%s\n\n", spec.ReviewInstructions)
	} else {
		b.WriteString("Evaluate the Meeseeks' output against the original TaskSpec.\n\n")
	}

	b.WriteString(`Check for:
- Correctness against acceptance criteria
- Interface contract compliance
- Obvious bugs, edge cases, or security issues
- Code quality and style compliance

Respond in the following format:

### Verdict: APPROVE | REJECT

### Criterion Evaluation
- [ ] AC-001: PASS | FAIL — <explanation>
- [ ] AC-002: PASS | FAIL — <explanation>

### Feedback (if REJECT)
<Specific, actionable feedback for the Meeseeks to address.
 Reference exact line numbers and code sections.>
`)

	path := filepath.Join(specDir, "spec.md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("writing review spec: %w", err)
	}
	return nil
}
