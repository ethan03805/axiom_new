package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscover_IgnoresGlobalConfigDirectory(t *testing.T) {
	root := t.TempDir()
	globalDir := filepath.Join(root, ".axiom")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("[budget]\nmax_usd = 10\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(root, "Projects", "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Discover(repoDir); err == nil {
		t.Fatal("expected discovery to ignore the user-global .axiom directory")
	}
}
