package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestModelsRefreshAction(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := modelsRefreshAction(application, buf)
	if err != nil {
		t.Fatalf("modelsRefreshAction: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "refreshed") {
		t.Errorf("expected output to contain 'refreshed', got: %s", output)
	}
}

func TestModelsListAction_ListsAll(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := modelsListAction(application, "", "", buf)
	if err != nil {
		t.Fatalf("modelsListAction: %v", err)
	}

	output := buf.String()
	// Shipped models were loaded in testApp, so there should be models listed
	if !strings.Contains(output, "ID") {
		t.Errorf("expected table header with 'ID', got: %s", output)
	}
	// Should list at least one model
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		t.Errorf("expected at least header + 1 model, got %d lines", len(lines))
	}
}

func TestModelsListAction_FilterByTier(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := modelsListAction(application, "premium", "", buf)
	if err != nil {
		t.Fatalf("modelsListAction with tier: %v", err)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	// Skip header (line 0) and separator (line 1); all data lines should be premium
	for _, line := range lines[2:] {
		if line == "" {
			continue
		}
		if !strings.Contains(line, "premium") {
			t.Errorf("expected all models to be premium tier, got line: %s", line)
		}
	}
}

func TestModelsListAction_FilterByFamily(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := modelsListAction(application, "", "claude", buf)
	if err != nil {
		t.Fatalf("modelsListAction with family: %v", err)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		if !strings.Contains(strings.ToLower(line), "claude") {
			t.Errorf("expected all models to be claude family, got line: %s", line)
		}
	}
}

func TestModelsListAction_NoResults(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := modelsListAction(application, "nonexistent-tier", "", buf)
	if err != nil {
		t.Fatalf("modelsListAction: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No models") {
		t.Errorf("expected 'No models' message for empty result, got: %s", output)
	}
}

func TestModelsInfoAction_ShowsModel(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	// Use a model ID that exists in shipped models
	// The shipped models should include some known IDs
	models, err := application.Registry.List("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) == 0 {
		t.Skip("no shipped models available")
	}

	err = modelsInfoAction(application, models[0].ID, buf)
	if err != nil {
		t.Fatalf("modelsInfoAction: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, models[0].ID) {
		t.Errorf("expected output to contain model ID %q, got: %s", models[0].ID, output)
	}
	if !strings.Contains(output, "Tier") {
		t.Errorf("expected output to contain 'Tier' field, got: %s", output)
	}
}

func TestModelsInfoAction_NotFound(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := modelsInfoAction(application, "nonexistent-model-id", buf)
	if err == nil {
		t.Fatal("expected error for nonexistent model")
	}
}
