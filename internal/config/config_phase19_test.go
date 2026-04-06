package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MergesObservabilityAndBitNetProcessOverrides(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := os.MkdirAll(filepath.Join(home, ".axiom"), 0o755); err != nil {
		t.Fatal(err)
	}
	globalConfig := `[observability]
log_prompts = true

[bitnet]
command = "python"
working_dir = "C:/bitnet"
startup_timeout_seconds = 45
`
	if err := os.WriteFile(filepath.Join(home, ".axiom", "config.toml"), []byte(globalConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(projectRoot, ".axiom"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectConfig := `[project]
name = "phase19"
slug = "phase19"
`
	if err := os.WriteFile(filepath.Join(projectRoot, ".axiom", "config.toml"), []byte(projectConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(projectRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.Observability.LogPrompts {
		t.Fatal("expected observability.log_prompts override from global config")
	}
	if cfg.BitNet.Command != "python" {
		t.Fatalf("bitnet.command = %q, want %q", cfg.BitNet.Command, "python")
	}
	if cfg.BitNet.WorkingDir != "C:/bitnet" {
		t.Fatalf("bitnet.working_dir = %q, want %q", cfg.BitNet.WorkingDir, "C:/bitnet")
	}
	if cfg.BitNet.StartupTimeoutSeconds != 45 {
		t.Fatalf("bitnet.startup_timeout_seconds = %d, want 45", cfg.BitNet.StartupTimeoutSeconds)
	}
	if cfg.BitNet.Port != 3002 {
		t.Fatalf("bitnet.port = %d, want default 3002", cfg.BitNet.Port)
	}
}
