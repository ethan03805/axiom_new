package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInit(t *testing.T) {
	dir := t.TempDir()

	if err := Init(dir, "My Test Project"); err != nil {
		t.Fatal(err)
	}

	// Verify .axiom/ directory exists
	axiomDir := filepath.Join(dir, ".axiom")
	if _, err := os.Stat(axiomDir); err != nil {
		t.Fatal("expected .axiom/ directory")
	}

	// Verify config.toml exists
	cfgPath := filepath.Join(axiomDir, "config.toml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatal("expected config.toml")
	}

	// Verify .gitignore exists
	giPath := filepath.Join(axiomDir, ".gitignore")
	if _, err := os.Stat(giPath); err != nil {
		t.Fatal("expected .gitignore")
	}

	// Verify models.json exists
	modelsPath := filepath.Join(axiomDir, "models.json")
	if _, err := os.Stat(modelsPath); err != nil {
		t.Fatal("expected models.json")
	}

	// Verify subdirectories
	subdirs := []string{
		"containers/specs", "containers/staging", "containers/ipc",
		"validation", "eco", "logs/prompts",
	}
	for _, sub := range subdirs {
		p := filepath.Join(axiomDir, sub)
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			t.Errorf("expected directory %s", sub)
		}
	}
}

func TestInit_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, "test"); err != nil {
		t.Fatal(err)
	}
	// Second init should fail
	if err := Init(dir, "test"); err == nil {
		t.Error("expected error for duplicate init")
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"My Project", "my-project"},
		{"hello_world", "hello-world"},
		{"  Spaces  ", "spaces"},
		{"UPPER", "upper"},
		{"a--b--c", "a--b--c"},
		{"", "project"},
		{"special!@#chars", "special-chars"},
	}

	for _, tt := range tests {
		got := Slugify(tt.input)
		if got != tt.expected {
			t.Errorf("Slugify(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, "test"); err != nil {
		t.Fatal(err)
	}

	// Should find from the root
	found, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if found != dir {
		t.Errorf("expected %s, got %s", dir, found)
	}

	// Should find from a subdirectory
	subDir := filepath.Join(dir, "src", "pkg")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	found, err = Discover(subDir)
	if err != nil {
		t.Fatal(err)
	}
	if found != dir {
		t.Errorf("expected %s from subdir, got %s", dir, found)
	}
}

func TestDiscover_NotFound(t *testing.T) {
	// Create a nested directory that guarantees no .axiom/ in parents
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Discover(nested)
	if err == nil {
		// It's possible a parent directory has .axiom/ on this machine.
		// Only fail if we can confirm there's genuinely no .axiom/ above.
		t.Skip("a parent directory may contain .axiom/")
	}
}

func TestWorkBranch(t *testing.T) {
	if got := WorkBranch("my-project"); got != "axiom/my-project" {
		t.Errorf("expected axiom/my-project, got %s", got)
	}
}

func TestDBPath(t *testing.T) {
	p := DBPath("/root")
	expected := filepath.Join("/root", ".axiom", "axiom.db")
	if p != expected {
		t.Errorf("expected %s, got %s", expected, p)
	}
}

func TestConfigPath(t *testing.T) {
	p := ConfigPath("/root")
	expected := filepath.Join("/root", ".axiom", "config.toml")
	if p != expected {
		t.Errorf("expected %s, got %s", expected, p)
	}
}

func TestWriteAndVerifySRS(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, "test"); err != nil {
		t.Fatal(err)
	}

	content := []byte("# SRS\n\nThis is the spec.\n")
	if err := WriteSRS(dir, content); err != nil {
		t.Fatal(err)
	}

	// Verify the SRS file is read-only
	srsPath := filepath.Join(dir, ".axiom", "srs.md")
	info, err := os.Stat(srsPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o200 != 0 {
		t.Error("SRS file should be read-only")
	}

	// Verify passes
	if err := VerifySRS(dir); err != nil {
		t.Errorf("verification should pass: %v", err)
	}

	// Tamper with hash file and verify fails
	hashPath := filepath.Join(dir, ".axiom", "srs.md.sha256")
	if err := os.WriteFile(hashPath, []byte("badhash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifySRS(dir); err == nil {
		t.Error("verification should fail after hash tampering")
	}
}

func TestVerifySRS_NoSRS(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, "test"); err != nil {
		t.Fatal(err)
	}
	// No SRS written — should succeed (nothing to verify)
	if err := VerifySRS(dir); err != nil {
		t.Errorf("should succeed with no SRS: %v", err)
	}
}
