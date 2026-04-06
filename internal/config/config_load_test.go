package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_IgnoresMissingProjectAndGlobalConfig(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	cfg, err := Load(projectRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected default config when layered files are absent")
	}
}

func TestLoad_ReturnsErrorForInvalidGlobalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	globalDir := filepath.Join(home, ".axiom")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("[project\nname = \"oops\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(""); err == nil {
		t.Fatal("expected invalid global config to fail loading")
	}
}
