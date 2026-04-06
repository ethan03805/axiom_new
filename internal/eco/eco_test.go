package eco

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ECO Category Validation Tests ---

func TestValidCategory_AllValidCodes(t *testing.T) {
	validCodes := []string{"ECO-DEP", "ECO-API", "ECO-SEC", "ECO-PLT", "ECO-LIC", "ECO-PRV"}
	for _, code := range validCodes {
		if !ValidCategory(code) {
			t.Errorf("expected %q to be valid", code)
		}
	}
}

func TestValidCategory_InvalidCodes(t *testing.T) {
	invalidCodes := []string{"ECO-NEW", "ECO-FEATURE", "eco-dep", "", "DEP", "ECO-"}
	for _, code := range invalidCodes {
		if ValidCategory(code) {
			t.Errorf("expected %q to be invalid", code)
		}
	}
}

func TestCategoryDescription(t *testing.T) {
	desc := CategoryDescription("ECO-DEP")
	if desc == "" {
		t.Error("expected non-empty description for ECO-DEP")
	}

	desc = CategoryDescription("ECO-INVALID")
	if desc != "" {
		t.Errorf("expected empty description for invalid code, got: %q", desc)
	}
}

// --- ECO Proposal Validation Tests ---

func TestValidateProposal_Valid(t *testing.T) {
	p := Proposal{
		Category:       "ECO-DEP",
		AffectedRefs:   "FR-001, AC-002",
		Description:    "Library X was deprecated.",
		ProposedChange: "Replace with Library Y.",
	}

	if err := ValidateProposal(p); err != nil {
		t.Fatalf("expected valid proposal, got: %v", err)
	}
}

func TestValidateProposal_InvalidCategory(t *testing.T) {
	p := Proposal{
		Category:       "ECO-NEW",
		AffectedRefs:   "FR-001",
		Description:    "Some issue.",
		ProposedChange: "Some fix.",
	}

	err := ValidateProposal(p)
	if err == nil {
		t.Fatal("expected error for invalid category")
	}
	if !strings.Contains(err.Error(), "category") {
		t.Errorf("error should mention category, got: %v", err)
	}
}

func TestValidateProposal_MissingDescription(t *testing.T) {
	p := Proposal{
		Category:       "ECO-DEP",
		AffectedRefs:   "FR-001",
		Description:    "",
		ProposedChange: "Replace library.",
	}

	err := ValidateProposal(p)
	if err == nil {
		t.Fatal("expected error for missing description")
	}
	if !strings.Contains(err.Error(), "description") {
		t.Errorf("error should mention description, got: %v", err)
	}
}

func TestValidateProposal_MissingAffectedRefs(t *testing.T) {
	p := Proposal{
		Category:       "ECO-DEP",
		AffectedRefs:   "",
		Description:    "Library was deprecated.",
		ProposedChange: "Replace library.",
	}

	err := ValidateProposal(p)
	if err == nil {
		t.Fatal("expected error for missing affected refs")
	}
	if !strings.Contains(err.Error(), "affected") {
		t.Errorf("error should mention affected refs, got: %v", err)
	}
}

func TestValidateProposal_MissingProposedChange(t *testing.T) {
	p := Proposal{
		Category:       "ECO-DEP",
		AffectedRefs:   "FR-001",
		Description:    "Library was deprecated.",
		ProposedChange: "",
	}

	err := ValidateProposal(p)
	if err == nil {
		t.Fatal("expected error for missing proposed change")
	}
	if !strings.Contains(err.Error(), "proposed") {
		t.Errorf("error should mention proposed change, got: %v", err)
	}
}

// --- ECO File Persistence Tests ---

func TestWriteAndReadECOFile(t *testing.T) {
	dir := t.TempDir()
	ecoDir := filepath.Join(dir, ".axiom", "eco")
	if err := os.MkdirAll(ecoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	record := Record{
		ECOCode:        "ECO-001",
		Category:       "ECO-DEP",
		Status:         "Approved",
		AffectedRefs:   "1.3, FR-003, AC-005",
		Description:    "Library passport-oauth2 is deprecated.",
		ProposedChange: "Replace with arctic v2.1.",
	}

	if err := WriteECOFile(dir, record); err != nil {
		t.Fatalf("WriteECOFile: %v", err)
	}

	// Verify file exists
	files, err := ListECOFiles(dir)
	if err != nil {
		t.Fatalf("ListECOFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 ECO file, got %d", len(files))
	}
	if !strings.Contains(files[0], "ECO-001") {
		t.Errorf("file name should contain ECO code, got: %s", files[0])
	}
}

func TestWriteECOFile_ContentFormat(t *testing.T) {
	dir := t.TempDir()
	ecoDir := filepath.Join(dir, ".axiom", "eco")
	if err := os.MkdirAll(ecoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	record := Record{
		ECOCode:        "ECO-002",
		Category:       "ECO-API",
		Status:         "Approved",
		AffectedRefs:   "FR-010",
		Description:    "API endpoint changed.",
		ProposedChange: "Update endpoint URL.",
	}

	if err := WriteECOFile(dir, record); err != nil {
		t.Fatal(err)
	}

	// Read the file and check format matches architecture Section 7.4
	files, _ := ListECOFiles(dir)
	content, err := os.ReadFile(filepath.Join(dir, ".axiom", "eco", files[0]))
	if err != nil {
		t.Fatal(err)
	}

	s := string(content)
	if !strings.Contains(s, "ECO-002") {
		t.Error("content should contain ECO code")
	}
	if !strings.Contains(s, "[ECO-API]") {
		t.Error("content should contain category code in brackets")
	}
	if !strings.Contains(s, "Environmental Issue") {
		t.Error("content should contain Environmental Issue section")
	}
	if !strings.Contains(s, "Proposed Substitute") {
		t.Error("content should contain Proposed Substitute section")
	}
	if !strings.Contains(s, "Impact Assessment") {
		t.Error("content should contain Impact Assessment section")
	}
}

func TestWriteECOFile_AppendOnly(t *testing.T) {
	dir := t.TempDir()
	ecoDir := filepath.Join(dir, ".axiom", "eco")
	if err := os.MkdirAll(ecoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	r1 := Record{
		ECOCode: "ECO-001", Category: "ECO-DEP", Status: "Approved",
		AffectedRefs: "FR-001", Description: "First issue.", ProposedChange: "Fix 1.",
	}
	r2 := Record{
		ECOCode: "ECO-002", Category: "ECO-API", Status: "Approved",
		AffectedRefs: "FR-002", Description: "Second issue.", ProposedChange: "Fix 2.",
	}

	if err := WriteECOFile(dir, r1); err != nil {
		t.Fatal(err)
	}
	if err := WriteECOFile(dir, r2); err != nil {
		t.Fatal(err)
	}

	files, err := ListECOFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 ECO files, got %d", len(files))
	}
}

func TestListECOFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	ecoDir := filepath.Join(dir, ".axiom", "eco")
	if err := os.MkdirAll(ecoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	files, err := ListECOFiles(dir)
	if err != nil {
		t.Fatalf("ListECOFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestListECOFiles_SortedByName(t *testing.T) {
	dir := t.TempDir()
	ecoDir := filepath.Join(dir, ".axiom", "eco")
	if err := os.MkdirAll(ecoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, code := range []string{"ECO-003", "ECO-001", "ECO-002"} {
		r := Record{
			ECOCode: code, Category: "ECO-DEP", Status: "Approved",
			AffectedRefs: "FR-001", Description: "Issue.", ProposedChange: "Fix.",
		}
		if err := WriteECOFile(dir, r); err != nil {
			t.Fatal(err)
		}
	}

	files, err := ListECOFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}

	// Should be sorted
	for i := 1; i < len(files); i++ {
		if files[i] < files[i-1] {
			t.Errorf("files not sorted: %v", files)
			break
		}
	}
}
