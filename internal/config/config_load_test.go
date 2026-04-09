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

func TestLoad_PreservesGlobalOpenRouterKeyWhenProjectConfigIsSparse(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	globalDir := filepath.Join(home, ".axiom")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalConfig := `[inference]
openrouter_api_key = "sk-global-key"
openrouter_base_url = "http://127.0.0.1:1"
timeout_seconds = 1
`
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(globalConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	projectDir := filepath.Join(projectRoot, ".axiom")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectConfig, err := MarshalProjectTemplate("phase9", "phase9")
	if err != nil {
		t.Fatalf("MarshalProjectTemplate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "config.toml"), projectConfig, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(projectRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Inference.OpenRouterAPIKey != "sk-global-key" {
		t.Fatalf("openrouter_api_key = %q, want global key", cfg.Inference.OpenRouterAPIKey)
	}
	if cfg.Project.Name != "phase9" || cfg.Project.Slug != "phase9" {
		t.Fatalf("project = %#v, want name/slug phase9", cfg.Project)
	}
}

func TestLoad_PreservesGlobalBitNetDisableWhenProjectConfigIsSparse(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	globalDir := filepath.Join(home, ".axiom")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalConfig := `[bitnet]
enabled = false
`
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(globalConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	projectDir := filepath.Join(projectRoot, ".axiom")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectConfig, err := MarshalProjectTemplate("phase10", "phase10")
	if err != nil {
		t.Fatalf("MarshalProjectTemplate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "config.toml"), projectConfig, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(projectRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BitNet.Enabled {
		t.Fatal("bitnet.enabled should inherit global false for a sparse project config")
	}
}

func TestLoad_ProjectOpenRouterOverrideStillWins(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	globalDir := filepath.Join(home, ".axiom")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalConfig := `[inference]
openrouter_api_key = "sk-global-key"
`
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(globalConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	projectDir := filepath.Join(projectRoot, ".axiom")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectConfig := `[project]
name = "phase9"
slug = "phase9"

[inference]
openrouter_api_key = "sk-project-key"
`
	if err := os.WriteFile(filepath.Join(projectDir, "config.toml"), []byte(projectConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(projectRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Inference.OpenRouterAPIKey != "sk-project-key" {
		t.Fatalf("openrouter_api_key = %q, want project override", cfg.Inference.OpenRouterAPIKey)
	}
}

func TestLoad_ProjectBitNetEnableOverrideStillWins(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	globalDir := filepath.Join(home, ".axiom")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalConfig := `[bitnet]
enabled = false
`
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(globalConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	projectDir := filepath.Join(projectRoot, ".axiom")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectConfig := `[project]
name = "phase10"
slug = "phase10"

[bitnet]
enabled = true
`
	if err := os.WriteFile(filepath.Join(projectDir, "config.toml"), []byte(projectConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(projectRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.BitNet.Enabled {
		t.Fatal("bitnet.enabled should honor an explicit project override to true")
	}
}

func TestLoad_ProjectBitNetDisableOverrideStillWins(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	globalDir := filepath.Join(home, ".axiom")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalConfig := `[bitnet]
enabled = true
`
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(globalConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	projectDir := filepath.Join(projectRoot, ".axiom")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectConfig := `[project]
name = "phase10"
slug = "phase10"

[bitnet]
enabled = false
`
	if err := os.WriteFile(filepath.Join(projectDir, "config.toml"), []byte(projectConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(projectRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BitNet.Enabled {
		t.Fatal("bitnet.enabled should honor an explicit project override to false")
	}
}
