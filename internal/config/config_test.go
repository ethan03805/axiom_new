package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default("my-project", "my-project")

	if cfg.Project.Name != "my-project" {
		t.Errorf("expected name my-project, got %s", cfg.Project.Name)
	}
	if cfg.Budget.MaxUSD != 10.0 {
		t.Errorf("expected budget 10.0, got %f", cfg.Budget.MaxUSD)
	}
	if cfg.Docker.NetworkMode != "none" {
		t.Errorf("expected network_mode none, got %s", cfg.Docker.NetworkMode)
	}
	if cfg.Validation.Network != "none" {
		t.Errorf("expected validation network none, got %s", cfg.Validation.Network)
	}
	if cfg.Security.ForceLocalForSecretBearing != true {
		t.Error("expected force_local_for_secret_bearing true")
	}
}

func TestValidate_Valid(t *testing.T) {
	cfg := Default("test", "test")
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should be valid: %v", err)
	}
}

func TestValidate_MissingName(t *testing.T) {
	cfg := Default("", "test")
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestValidate_InvalidRuntime(t *testing.T) {
	cfg := Default("test", "test")
	cfg.Orchestrator.Runtime = "invalid"
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for invalid runtime")
	}
}

func TestValidate_InvalidNetworkMode(t *testing.T) {
	cfg := Default("test", "test")
	cfg.Docker.NetworkMode = "bridge"
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for network_mode != none")
	}
}

func TestValidate_NegativeBudget(t *testing.T) {
	cfg := Default("test", "test")
	cfg.Budget.MaxUSD = -5
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for negative budget")
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `[project]
name = "loaded-project"
slug = "loaded-project"

[budget]
max_usd = 25.0
warn_at_percent = 90
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project.Name != "loaded-project" {
		t.Errorf("expected loaded-project, got %s", cfg.Project.Name)
	}
	if cfg.Budget.MaxUSD != 25.0 {
		t.Errorf("expected 25.0, got %f", cfg.Budget.MaxUSD)
	}
}

func TestLoadFile_NotFound(t *testing.T) {
	_, err := LoadFile("/nonexistent/config.toml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadFile_InvalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("this is not valid toml [[["), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path)
	if err == nil {
		t.Error("expected error for invalid TOML")
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	cfg := Default("roundtrip", "roundtrip")
	data, err := Marshal(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Project.Name != "roundtrip" {
		t.Errorf("expected roundtrip, got %s", loaded.Project.Name)
	}
	if loaded.Budget.MaxUSD != cfg.Budget.MaxUSD {
		t.Errorf("budget mismatch: %f vs %f", loaded.Budget.MaxUSD, cfg.Budget.MaxUSD)
	}
}
