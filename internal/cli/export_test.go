package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

func TestExportAction_NoRun(t *testing.T) {
	application, proj := testAppWithProject(t)
	buf := new(bytes.Buffer)

	err := exportAction(application, proj.ID, buf)
	if err != nil {
		t.Fatalf("exportAction: %v", err)
	}

	output := buf.String()

	// Should be valid JSON
	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}

	// Should contain project info
	if result["project_name"] != "test-project" {
		t.Errorf("project_name = %v, want test-project", result["project_name"])
	}
}

func TestExportAction_WithActiveRun(t *testing.T) {
	application, _, run := testAppWithActiveRun(t)
	buf := new(bytes.Buffer)

	err := exportAction(application, "proj-test", buf)
	if err != nil {
		t.Fatalf("exportAction: %v", err)
	}

	output := buf.String()

	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	// Should contain run info
	runData, ok := result["run"].(map[string]any)
	if !ok {
		t.Fatal("expected 'run' field in export")
	}
	if runData["id"] != run.ID {
		t.Errorf("run id = %v, want %v", runData["id"], run.ID)
	}
	if runData["status"] != string(state.RunActive) {
		t.Errorf("run status = %v, want active", runData["status"])
	}
}

func TestExportAction_WithTasks(t *testing.T) {
	application, _, run := testAppWithActiveRun(t)

	// Create a task
	err := application.DB.CreateTask(&state.Task{
		ID:       "task-1",
		RunID:    run.ID,
		Title:    "Implement feature",
		Status:   state.TaskQueued,
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
	})
	if err != nil {
		t.Fatal(err)
	}

	buf := new(bytes.Buffer)
	if err := exportAction(application, "proj-test", buf); err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatal(err)
	}

	tasks, ok := result["tasks"].([]any)
	if !ok {
		t.Fatal("expected 'tasks' array in export")
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}
}

func TestExportAction_OutputIsHumanReadable(t *testing.T) {
	application, proj := testAppWithProject(t)
	buf := new(bytes.Buffer)

	if err := exportAction(application, proj.ID, buf); err != nil {
		t.Fatal(err)
	}

	output := buf.String()

	// Human-readable JSON should be indented
	if !strings.Contains(output, "\n") {
		t.Error("expected indented/pretty JSON output")
	}
}

func TestExportAction_IncludesProjectRoot(t *testing.T) {
	application, proj := testAppWithProject(t)
	buf := new(bytes.Buffer)

	if err := exportAction(application, proj.ID, buf); err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatal(err)
	}

	if result["project_root"] == nil || result["project_root"] == "" {
		t.Error("expected project_root in export")
	}
}

// TestExportAction_IncludesAllRuns verifies the GitHub #1 audit-trail
// fix: `axiom export` must walk every project_runs row, not just the
// active one. A run that was cancelled (or clobbered via a Force
// replace) must still appear in the `runs` array flagged as orphaned.
func TestExportAction_IncludesAllRuns(t *testing.T) {
	application, proj, activeRun := testAppWithActiveRun(t)

	// Cancel the original run and create a second run so the export
	// has one orphaned + one active to list.
	if err := application.Engine.CancelRun(activeRun.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	newRun, err := application.Engine.StartRun(engine.StartRunOptions{
		ProjectID:  proj.ID,
		Prompt:    "second prompt",
		BaseBranch: "main",
		Source:    "cli",
	})
	if err != nil {
		t.Fatalf("StartRun for second run: %v", err)
	}

	buf := new(bytes.Buffer)
	if err := exportAction(application, proj.ID, buf); err != nil {
		t.Fatalf("exportAction: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}

	runsRaw, ok := result["runs"].([]any)
	if !ok {
		t.Fatalf("expected 'runs' array in export; got: %s", buf.String())
	}
	if len(runsRaw) < 2 {
		t.Fatalf("expected at least 2 runs in export, got %d: %s", len(runsRaw), buf.String())
	}

	// Find both runs by ID and assert orphaned flags.
	var foundCancelled, foundActive bool
	for _, r := range runsRaw {
		rm, _ := r.(map[string]any)
		id, _ := rm["id"].(string)
		status, _ := rm["status"].(string)
		orphaned, _ := rm["orphaned"].(bool)
		if id == activeRun.ID {
			foundCancelled = true
			if status != string(state.RunCancelled) {
				t.Errorf("cancelled run status = %q, want cancelled", status)
			}
			if !orphaned {
				t.Error("cancelled run should be flagged orphaned=true")
			}
		}
		if id == newRun.ID {
			foundActive = true
			if orphaned {
				t.Error("active run should not be flagged orphaned")
			}
		}
	}
	if !foundCancelled {
		t.Error("cancelled/orphaned run missing from export runs[]")
	}
	if !foundActive {
		t.Error("active run missing from export runs[]")
	}
}
