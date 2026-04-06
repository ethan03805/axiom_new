package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// --- ParseManifest ---

func TestParseManifest_Valid(t *testing.T) {
	data := []byte(`{
		"task_id": "task-042",
		"base_snapshot": "abc123def",
		"files": {
			"added": [
				{"path": "src/handlers/auth.go", "binary": false},
				{"path": "public/logo.png", "binary": true, "size_bytes": 24576}
			],
			"modified": [
				{"path": "src/routes/api.go", "binary": false}
			],
			"deleted": ["src/handlers/old_auth.go"],
			"renamed": [
				{"from": "src/utils/hash.go", "to": "src/crypto/hash.go"}
			]
		}
	}`)

	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}

	if m.TaskID != "task-042" {
		t.Errorf("TaskID = %q, want %q", m.TaskID, "task-042")
	}
	if m.BaseSnapshot != "abc123def" {
		t.Errorf("BaseSnapshot = %q, want %q", m.BaseSnapshot, "abc123def")
	}
	if len(m.Files.Added) != 2 {
		t.Errorf("len(Added) = %d, want 2", len(m.Files.Added))
	}
	if m.Files.Added[0].Path != "src/handlers/auth.go" {
		t.Errorf("Added[0].Path = %q, want %q", m.Files.Added[0].Path, "src/handlers/auth.go")
	}
	if m.Files.Added[0].Binary {
		t.Error("Added[0].Binary = true, want false")
	}
	if m.Files.Added[1].Binary != true {
		t.Error("Added[1].Binary = false, want true")
	}
	if m.Files.Added[1].SizeBytes != 24576 {
		t.Errorf("Added[1].SizeBytes = %d, want 24576", m.Files.Added[1].SizeBytes)
	}
	if len(m.Files.Modified) != 1 {
		t.Errorf("len(Modified) = %d, want 1", len(m.Files.Modified))
	}
	if len(m.Files.Deleted) != 1 {
		t.Errorf("len(Deleted) = %d, want 1", len(m.Files.Deleted))
	}
	if m.Files.Deleted[0] != "src/handlers/old_auth.go" {
		t.Errorf("Deleted[0] = %q, want %q", m.Files.Deleted[0], "src/handlers/old_auth.go")
	}
	if len(m.Files.Renamed) != 1 {
		t.Errorf("len(Renamed) = %d, want 1", len(m.Files.Renamed))
	}
	if m.Files.Renamed[0].From != "src/utils/hash.go" {
		t.Errorf("Renamed[0].From = %q, want %q", m.Files.Renamed[0].From, "src/utils/hash.go")
	}
	if m.Files.Renamed[0].To != "src/crypto/hash.go" {
		t.Errorf("Renamed[0].To = %q, want %q", m.Files.Renamed[0].To, "src/crypto/hash.go")
	}
}

func TestParseManifest_EmptyFiles(t *testing.T) {
	data := []byte(`{
		"task_id": "task-001",
		"base_snapshot": "deadbeef",
		"files": {}
	}`)

	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.TaskID != "task-001" {
		t.Errorf("TaskID = %q, want %q", m.TaskID, "task-001")
	}
}

func TestParseManifest_InvalidJSON(t *testing.T) {
	_, err := ParseManifest([]byte(`{invalid`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseManifest_MissingTaskID(t *testing.T) {
	data := []byte(`{
		"base_snapshot": "abc123",
		"files": {}
	}`)
	_, err := ParseManifest(data)
	if err == nil {
		t.Error("expected error for missing task_id")
	}
}

func TestParseManifest_MissingBaseSnapshot(t *testing.T) {
	data := []byte(`{
		"task_id": "task-001",
		"files": {}
	}`)
	_, err := ParseManifest(data)
	if err == nil {
		t.Error("expected error for missing base_snapshot")
	}
}

// --- ValidateManifest ---

func setupStagingDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create a file in the staging directory
	if err := os.MkdirAll(filepath.Join(dir, "src", "handlers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "handlers", "auth.go"), []byte("package handlers"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestValidateManifest_ValidSimple(t *testing.T) {
	staging := setupStagingDir(t)

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Added: []FileEntry{{Path: "src/handlers/auth.go"}},
		},
	}

	errs := ValidateManifest(m, staging, nil, DefaultValidationConfig())
	if len(errs) > 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateManifest_FileNotInStaging(t *testing.T) {
	staging := t.TempDir() // empty

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Added: []FileEntry{{Path: "src/missing.go"}},
		},
	}

	errs := ValidateManifest(m, staging, nil, DefaultValidationConfig())
	if len(errs) == 0 {
		t.Error("expected error for file not found in staging")
	}
}

func TestValidateManifest_UnlistedFileInStaging(t *testing.T) {
	staging := setupStagingDir(t)

	// Manifest declares no files, but staging has auth.go
	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files:        ManifestFiles{},
	}

	errs := ValidateManifest(m, staging, nil, DefaultValidationConfig())
	if len(errs) == 0 {
		t.Error("expected error for unlisted file in staging")
	}
}

func TestValidateManifest_PathTraversal(t *testing.T) {
	staging := t.TempDir()

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Added: []FileEntry{{Path: "../../../etc/passwd"}},
		},
	}

	errs := ValidateManifest(m, staging, nil, DefaultValidationConfig())
	if len(errs) == 0 {
		t.Error("expected error for path traversal")
	}

	hasTraversal := false
	for _, e := range errs {
		if containsAny(e.Error(), "traversal", "escapes") {
			hasTraversal = true
		}
	}
	if !hasTraversal {
		t.Errorf("expected traversal error, got %v", errs)
	}
}

func TestValidateManifest_AbsolutePath(t *testing.T) {
	staging := t.TempDir()

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Added: []FileEntry{{Path: "/etc/passwd"}},
		},
	}

	errs := ValidateManifest(m, staging, nil, DefaultValidationConfig())
	if len(errs) == 0 {
		t.Error("expected error for absolute path")
	}
}

func TestValidateManifest_OversizedFile(t *testing.T) {
	staging := t.TempDir()

	// Create a file that exceeds size limit
	bigContent := make([]byte, 11*1024*1024) // 11 MB
	if err := os.WriteFile(filepath.Join(staging, "big.bin"), bigContent, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultValidationConfig()
	cfg.MaxFileSizeBytes = 10 * 1024 * 1024 // 10 MB

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Added: []FileEntry{{Path: "big.bin", Binary: true, SizeBytes: 11 * 1024 * 1024}},
		},
	}

	errs := ValidateManifest(m, staging, nil, cfg)
	if len(errs) == 0 {
		t.Error("expected error for oversized file")
	}
}

func TestValidateManifest_ScopeEnforcement(t *testing.T) {
	staging := t.TempDir()

	if err := os.MkdirAll(filepath.Join(staging, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "src", "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Only allow writes to "internal/" scope — "src/main.go" should be rejected
	allowedScope := []string{"internal/"}

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Added: []FileEntry{{Path: "src/main.go"}},
		},
	}

	errs := ValidateManifest(m, staging, allowedScope, DefaultValidationConfig())
	if len(errs) == 0 {
		t.Error("expected error for out-of-scope file")
	}
}

func TestValidateManifest_ScopeEnforcement_NilMeansUnrestricted(t *testing.T) {
	staging := setupStagingDir(t)

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Added: []FileEntry{{Path: "src/handlers/auth.go"}},
		},
	}

	// nil scope means unrestricted
	errs := ValidateManifest(m, staging, nil, DefaultValidationConfig())
	if len(errs) > 0 {
		t.Errorf("expected no errors with nil scope, got %v", errs)
	}
}

func TestValidateManifest_DuplicatePaths(t *testing.T) {
	staging := setupStagingDir(t)

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Added: []FileEntry{
				{Path: "src/handlers/auth.go"},
				{Path: "src/handlers/auth.go"},
			},
		},
	}

	errs := ValidateManifest(m, staging, nil, DefaultValidationConfig())
	if len(errs) == 0 {
		t.Error("expected error for duplicate paths")
	}
}

func TestValidateManifest_RenameFromAndToTracked(t *testing.T) {
	staging := t.TempDir()

	if err := os.MkdirAll(filepath.Join(staging, "src", "crypto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "src", "crypto", "hash.go"), []byte("package crypto"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Renamed: []RenameEntry{
				{From: "src/utils/hash.go", To: "src/crypto/hash.go"},
			},
		},
	}

	// The "to" file should exist in staging
	errs := ValidateManifest(m, staging, nil, DefaultValidationConfig())
	if len(errs) > 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateManifest_DeletedPathTraversal(t *testing.T) {
	staging := t.TempDir()

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Deleted: []string{"../../../etc/passwd"},
		},
	}

	errs := ValidateManifest(m, staging, nil, DefaultValidationConfig())
	if len(errs) == 0 {
		t.Error("expected error for deleted path traversal")
	}
}

func TestValidateManifest_RenamePathTraversal(t *testing.T) {
	staging := t.TempDir()

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Renamed: []RenameEntry{
				{From: "../secret.key", To: "src/secret.key"},
			},
		},
	}

	errs := ValidateManifest(m, staging, nil, DefaultValidationConfig())
	if len(errs) == 0 {
		t.Error("expected error for rename path traversal")
	}
}

// --- ComputeArtifacts ---

func TestComputeArtifacts_Added(t *testing.T) {
	staging := t.TempDir()
	content := []byte("package main\n\nfunc main() {}\n")
	if err := os.WriteFile(filepath.Join(staging, "main.go"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Added: []FileEntry{{Path: "main.go"}},
		},
	}

	arts, err := ComputeArtifacts(m, staging, 1)
	if err != nil {
		t.Fatalf("ComputeArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("len(artifacts) = %d, want 1", len(arts))
	}

	a := arts[0]
	if a.Operation != "add" {
		t.Errorf("Operation = %q, want %q", a.Operation, "add")
	}
	if a.PathTo == nil || *a.PathTo != "main.go" {
		t.Errorf("PathTo = %v, want %q", a.PathTo, "main.go")
	}
	if a.PathFrom != nil {
		t.Errorf("PathFrom = %v, want nil", a.PathFrom)
	}

	// Verify SHA256
	h := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(h[:])
	if a.SHA256After == nil || *a.SHA256After != expectedHash {
		t.Errorf("SHA256After = %v, want %q", a.SHA256After, expectedHash)
	}

	size := int64(len(content))
	if a.SizeAfter == nil || *a.SizeAfter != size {
		t.Errorf("SizeAfter = %v, want %d", a.SizeAfter, size)
	}
}

func TestComputeArtifacts_Modified(t *testing.T) {
	staging := t.TempDir()
	content := []byte("package main // modified\n")
	if err := os.WriteFile(filepath.Join(staging, "main.go"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Modified: []FileEntry{{Path: "main.go"}},
		},
	}

	arts, err := ComputeArtifacts(m, staging, 1)
	if err != nil {
		t.Fatalf("ComputeArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("len(artifacts) = %d, want 1", len(arts))
	}
	if arts[0].Operation != "modify" {
		t.Errorf("Operation = %q, want %q", arts[0].Operation, "modify")
	}
	if arts[0].PathTo == nil || *arts[0].PathTo != "main.go" {
		t.Errorf("PathTo = %v, want %q", arts[0].PathTo, "main.go")
	}
}

func TestComputeArtifacts_Deleted(t *testing.T) {
	staging := t.TempDir()

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Deleted: []string{"old_file.go"},
		},
	}

	arts, err := ComputeArtifacts(m, staging, 1)
	if err != nil {
		t.Fatalf("ComputeArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("len(artifacts) = %d, want 1", len(arts))
	}
	if arts[0].Operation != "delete" {
		t.Errorf("Operation = %q, want %q", arts[0].Operation, "delete")
	}
	if arts[0].PathFrom == nil || *arts[0].PathFrom != "old_file.go" {
		t.Errorf("PathFrom = %v, want %q", arts[0].PathFrom, "old_file.go")
	}
}

func TestComputeArtifacts_Renamed(t *testing.T) {
	staging := t.TempDir()
	content := []byte("package crypto\n")
	if err := os.MkdirAll(filepath.Join(staging, "src", "crypto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "src", "crypto", "hash.go"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Renamed: []RenameEntry{
				{From: "src/utils/hash.go", To: "src/crypto/hash.go"},
			},
		},
	}

	arts, err := ComputeArtifacts(m, staging, 1)
	if err != nil {
		t.Fatalf("ComputeArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("len(artifacts) = %d, want 1", len(arts))
	}

	a := arts[0]
	if a.Operation != "rename" {
		t.Errorf("Operation = %q, want %q", a.Operation, "rename")
	}
	if a.PathFrom == nil || *a.PathFrom != "src/utils/hash.go" {
		t.Errorf("PathFrom = %v, want %q", a.PathFrom, "src/utils/hash.go")
	}
	if a.PathTo == nil || *a.PathTo != "src/crypto/hash.go" {
		t.Errorf("PathTo = %v, want %q", a.PathTo, "src/crypto/hash.go")
	}

	// SHA256 should be computed from the "to" file in staging
	h := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(h[:])
	if a.SHA256After == nil || *a.SHA256After != expectedHash {
		t.Errorf("SHA256After = %v, want %q", a.SHA256After, expectedHash)
	}
}

// --- AllPaths ---

func TestAllPaths(t *testing.T) {
	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Added:    []FileEntry{{Path: "a.go"}, {Path: "b.go"}},
			Modified: []FileEntry{{Path: "c.go"}},
			Deleted:  []string{"d.go"},
			Renamed:  []RenameEntry{{From: "e.go", To: "f.go"}},
		},
	}

	paths := m.AllOutputPaths()
	expected := map[string]bool{
		"a.go": true,
		"b.go": true,
		"c.go": true,
		"f.go": true, // rename "to" path
	}

	if len(paths) != len(expected) {
		t.Fatalf("len(AllOutputPaths) = %d, want %d", len(paths), len(expected))
	}
	for _, p := range paths {
		if !expected[p] {
			t.Errorf("unexpected path %q in AllOutputPaths", p)
		}
	}
}

func TestAllReferencedPaths(t *testing.T) {
	m := &Manifest{
		TaskID:       "task-001",
		BaseSnapshot: "abc123",
		Files: ManifestFiles{
			Added:    []FileEntry{{Path: "a.go"}},
			Modified: []FileEntry{{Path: "c.go"}},
			Deleted:  []string{"d.go"},
			Renamed:  []RenameEntry{{From: "e.go", To: "f.go"}},
		},
	}

	paths := m.AllReferencedPaths()
	expected := map[string]bool{
		"a.go": true,
		"c.go": true,
		"d.go": true,
		"e.go": true,
		"f.go": true,
	}

	if len(paths) != len(expected) {
		t.Fatalf("len(AllReferencedPaths) = %d, want %d", len(paths), len(expected))
	}
	for _, p := range paths {
		if !expected[p] {
			t.Errorf("unexpected path %q in AllReferencedPaths", p)
		}
	}
}

// --- helper ---

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
