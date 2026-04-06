package srs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- SRS Structure Validation Tests ---

func TestValidateStructure_ValidSRS(t *testing.T) {
	content := validSRSContent()
	if err := ValidateStructure(content); err != nil {
		t.Fatalf("expected valid SRS, got error: %v", err)
	}
}

func TestValidateStructure_MissingArchitectureSection(t *testing.T) {
	content := `# SRS: Test Project

## 2. Requirements & Constraints

### 2.1 Functional Requirements
- FR-001: The system SHALL do something.

## 3. Test Strategy

### 3.1 Unit Testing
Unit testing approach.

## 4. Acceptance Criteria

### 4.1 Per-Component Criteria
- [ ] AC-001: It works.
`
	err := ValidateStructure(content)
	if err == nil {
		t.Fatal("expected error for missing Architecture section")
	}
	if !strings.Contains(err.Error(), "Architecture") {
		t.Errorf("error should mention Architecture, got: %v", err)
	}
}

func TestValidateStructure_MissingRequirementsSection(t *testing.T) {
	content := `# SRS: Test Project

## 1. Architecture

### 1.1 System Overview
Overview here.

## 3. Test Strategy

### 3.1 Unit Testing
Unit testing approach.

## 4. Acceptance Criteria

### 4.1 Per-Component Criteria
- [ ] AC-001: It works.
`
	err := ValidateStructure(content)
	if err == nil {
		t.Fatal("expected error for missing Requirements section")
	}
	if !strings.Contains(err.Error(), "Requirements") {
		t.Errorf("error should mention Requirements, got: %v", err)
	}
}

func TestValidateStructure_MissingTestStrategy(t *testing.T) {
	content := `# SRS: Test Project

## 1. Architecture

### 1.1 System Overview
Overview here.

## 2. Requirements & Constraints

### 2.1 Functional Requirements
- FR-001: The system SHALL work.

## 4. Acceptance Criteria

### 4.1 Per-Component Criteria
- [ ] AC-001: It works.
`
	err := ValidateStructure(content)
	if err == nil {
		t.Fatal("expected error for missing Test Strategy section")
	}
	if !strings.Contains(err.Error(), "Test Strategy") {
		t.Errorf("error should mention Test Strategy, got: %v", err)
	}
}

func TestValidateStructure_MissingAcceptanceCriteria(t *testing.T) {
	content := `# SRS: Test Project

## 1. Architecture

### 1.1 System Overview
Overview here.

## 2. Requirements & Constraints

### 2.1 Functional Requirements
- FR-001: The system SHALL work.

## 3. Test Strategy

### 3.1 Unit Testing
Unit testing approach.
`
	err := ValidateStructure(content)
	if err == nil {
		t.Fatal("expected error for missing Acceptance Criteria section")
	}
	if !strings.Contains(err.Error(), "Acceptance Criteria") {
		t.Errorf("error should mention Acceptance Criteria, got: %v", err)
	}
}

func TestValidateStructure_MissingSRSTitle(t *testing.T) {
	content := `# Not an SRS

## 1. Architecture

### 1.1 System Overview
Overview here.

## 2. Requirements & Constraints

### 2.1 Functional Requirements
- FR-001: The system SHALL work.

## 3. Test Strategy

### 3.1 Unit Testing
Unit testing approach.

## 4. Acceptance Criteria

### 4.1 Per-Component Criteria
- [ ] AC-001: It works.
`
	err := ValidateStructure(content)
	if err == nil {
		t.Fatal("expected error for missing SRS title")
	}
	if !strings.Contains(err.Error(), "SRS:") {
		t.Errorf("error should mention SRS title format, got: %v", err)
	}
}

func TestValidateStructure_EmptyContent(t *testing.T) {
	err := ValidateStructure("")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestValidateStructure_AllSectionsPresent(t *testing.T) {
	// Verify that a full valid SRS passes all checks
	content := validSRSContent()
	if err := ValidateStructure(content); err != nil {
		t.Fatalf("valid SRS should pass: %v", err)
	}
}

// --- Bootstrap Context Tests ---

func TestBuildBootstrapContext_Greenfield(t *testing.T) {
	dir := t.TempDir()

	ctx, err := BuildBootstrapContext(dir, true)
	if err != nil {
		t.Fatalf("BuildBootstrapContext: %v", err)
	}

	if !ctx.IsGreenfield {
		t.Error("expected IsGreenfield to be true")
	}
	if ctx.RepoMap != "" {
		t.Error("greenfield context should have empty RepoMap")
	}
	if ctx.ProjectRoot != dir {
		t.Errorf("ProjectRoot = %q, want %q", ctx.ProjectRoot, dir)
	}
}

func TestBuildBootstrapContext_ExistingProject(t *testing.T) {
	dir := t.TempDir()

	// Create some project files to simulate existing project
	if err := os.MkdirAll(filepath.Join(dir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, err := BuildBootstrapContext(dir, false)
	if err != nil {
		t.Fatalf("BuildBootstrapContext: %v", err)
	}

	if ctx.IsGreenfield {
		t.Error("expected IsGreenfield to be false")
	}
	if ctx.RepoMap == "" {
		t.Error("existing project context should have non-empty RepoMap")
	}
	if !strings.Contains(ctx.RepoMap, "cmd/main.go") || !strings.Contains(ctx.RepoMap, "go.mod") {
		t.Errorf("RepoMap should list project files, got: %s", ctx.RepoMap)
	}
}

func TestBuildBootstrapContext_ExcludesAxiomDir(t *testing.T) {
	dir := t.TempDir()

	// Create .axiom/ directory with internal state
	if err := os.MkdirAll(filepath.Join(dir, ".axiom", "containers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".axiom", "config.toml"), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a real project file
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, err := BuildBootstrapContext(dir, false)
	if err != nil {
		t.Fatalf("BuildBootstrapContext: %v", err)
	}

	if strings.Contains(ctx.RepoMap, ".axiom") {
		t.Errorf("RepoMap should exclude .axiom/, got: %s", ctx.RepoMap)
	}
}

// --- SRS Draft Persistence Tests ---

func TestWriteAndReadDraft(t *testing.T) {
	dir := t.TempDir()
	axiomDir := filepath.Join(dir, ".axiom")
	if err := os.MkdirAll(axiomDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "# SRS Draft\n\nThis is a draft."
	runID := "run-123"

	if err := WriteDraft(dir, runID, content); err != nil {
		t.Fatalf("WriteDraft: %v", err)
	}

	got, err := ReadDraft(dir, runID)
	if err != nil {
		t.Fatalf("ReadDraft: %v", err)
	}

	if got != content {
		t.Errorf("ReadDraft = %q, want %q", got, content)
	}
}

func TestReadDraft_NotFound(t *testing.T) {
	dir := t.TempDir()
	axiomDir := filepath.Join(dir, ".axiom")
	if err := os.MkdirAll(axiomDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := ReadDraft(dir, "nonexistent-run")
	if err == nil {
		t.Fatal("expected error for missing draft")
	}
}

func TestWriteDraft_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	axiomDir := filepath.Join(dir, ".axiom")
	if err := os.MkdirAll(axiomDir, 0o755); err != nil {
		t.Fatal(err)
	}

	runID := "run-123"

	if err := WriteDraft(dir, runID, "version 1"); err != nil {
		t.Fatal(err)
	}
	if err := WriteDraft(dir, runID, "version 2"); err != nil {
		t.Fatal(err)
	}

	got, err := ReadDraft(dir, runID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "version 2" {
		t.Errorf("expected overwritten content, got: %q", got)
	}
}

func TestDeleteDraft(t *testing.T) {
	dir := t.TempDir()
	axiomDir := filepath.Join(dir, ".axiom")
	if err := os.MkdirAll(axiomDir, 0o755); err != nil {
		t.Fatal(err)
	}

	runID := "run-123"
	if err := WriteDraft(dir, runID, "some content"); err != nil {
		t.Fatal(err)
	}

	if err := DeleteDraft(dir, runID); err != nil {
		t.Fatalf("DeleteDraft: %v", err)
	}

	_, err := ReadDraft(dir, runID)
	if err == nil {
		t.Fatal("expected error after deleting draft")
	}
}

func TestDeleteDraft_NonexistentIsNoop(t *testing.T) {
	dir := t.TempDir()
	axiomDir := filepath.Join(dir, ".axiom")
	if err := os.MkdirAll(axiomDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Should not error when draft doesn't exist
	if err := DeleteDraft(dir, "nonexistent"); err != nil {
		t.Fatalf("DeleteDraft on nonexistent should not error: %v", err)
	}
}

// --- SRS Hash Computation Tests ---

func TestComputeHash(t *testing.T) {
	content := []byte("test content")
	hash := ComputeHash(content)

	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if len(hash) != 64 {
		t.Errorf("expected 64-char hex SHA-256, got %d chars", len(hash))
	}

	// Deterministic
	hash2 := ComputeHash(content)
	if hash != hash2 {
		t.Error("hash should be deterministic")
	}

	// Different content → different hash
	hash3 := ComputeHash([]byte("different content"))
	if hash == hash3 {
		t.Error("different content should produce different hash")
	}
}

// --- Test Helpers ---

func validSRSContent() string {
	return `# SRS: Test Project

## 1. Architecture

### 1.1 System Overview
A high-level overview of the system.

### 1.2 Component Breakdown
- Component A: handles auth
- Component B: handles data

### 1.3 Technology Decisions
Go for backend, React for frontend.

### 1.4 Data Model
SQLite database with users and items tables.

### 1.5 Directory Structure
cmd/, internal/, web/

## 2. Requirements & Constraints

### 2.1 Functional Requirements
- FR-001: The system SHALL authenticate users.
- FR-002: The system SHALL store items.

### 2.2 Non-Functional Requirements
- NFR-001: The system SHALL respond within 200ms.

### 2.3 Constraints
Must run on Linux.

### 2.4 Assumptions
Users have modern browsers.

## 3. Test Strategy

### 3.1 Unit Testing
Go testing package with table-driven tests.

### 3.2 Integration Testing
Docker-based integration tests.

### 3.3 Security Testing
Static analysis with gosec.

## 4. Acceptance Criteria

### 4.1 Per-Component Criteria

#### Component: Auth
- [ ] AC-001: Users can log in with valid credentials.
- [ ] AC-002: Invalid credentials are rejected.

### 4.2 Integration Criteria
- [ ] IC-001: Auth and data components work together.

### 4.3 Completion Definition
All acceptance criteria pass.
`
}
