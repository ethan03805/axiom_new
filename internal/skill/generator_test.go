package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/config"
)

func TestGeneratorGenerateWritesExpectedArtifacts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		runtime     Runtime
		wantFiles   []string
		primaryFile string
	}{
		{
			name:        "claw",
			runtime:     RuntimeClaw,
			wantFiles:   []string{"axiom-skill.md"},
			primaryFile: "axiom-skill.md",
		},
		{
			name:        "claude-code",
			runtime:     RuntimeClaudeCode,
			wantFiles:   []string{filepath.Join(".claude", "CLAUDE.md"), filepath.Join(".claude", "settings.json")},
			primaryFile: filepath.Join(".claude", "CLAUDE.md"),
		},
		{
			name:        "codex",
			runtime:     RuntimeCodex,
			wantFiles:   []string{"AGENTS.md", "codex-instructions.md"},
			primaryFile: "AGENTS.md",
		},
		{
			name:        "opencode",
			runtime:     RuntimeOpenCode,
			wantFiles:   []string{"AGENTS.md", "opencode-instructions.md", "opencode.json"},
			primaryFile: "AGENTS.md",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			cfg := config.Default("Axiom Demo", "axiom-demo")
			cfg.API.Port = 4123
			cfg.Budget.MaxUSD = 42.50
			cfg.Budget.WarnAtPercent = 65
			cfg.Git.BranchPrefix = "axiom"

			gen := NewGenerator(root, &cfg)
			artifacts, err := gen.Generate(tc.runtime)
			if err != nil {
				t.Fatalf("Generate(%q) error: %v", tc.runtime, err)
			}
			if len(artifacts) == 0 {
				t.Fatalf("Generate(%q) returned no artifacts", tc.runtime)
			}

			for _, rel := range tc.wantFiles {
				path := filepath.Join(root, rel)
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("expected generated file %s: %v", rel, err)
				}
			}

			content := readFile(t, filepath.Join(root, tc.primaryFile))
			assertContainsAll(t, content,
				"prompt -> SRS -> approval -> execution",
				"Trusted Engine",
				"Untrusted Agent Plane",
				"TaskSpec",
				"ReviewSpec",
				"submit_srs",
				"spawn_meeseeks",
				"budget",
				"ECO",
				"test authorship separation",
				"Do not implement the user's requested software directly outside Axiom.",
				"http://localhost:4123",
				"$42.50",
				"warn threshold: 65%",
			)
		})
	}
}

func TestGeneratorRegeneratesWhenConfigChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Default("Axiom Demo", "axiom-demo")
	cfg.API.Port = 3000
	cfg.Budget.MaxUSD = 10.00
	cfg.Budget.WarnAtPercent = 80
	cfg.Git.BranchPrefix = "axiom"

	gen := NewGenerator(root, &cfg)
	if _, err := gen.Generate(RuntimeOpenCode); err != nil {
		t.Fatalf("initial generate error: %v", err)
	}

	before := readFile(t, filepath.Join(root, "opencode-instructions.md"))

	cfg.API.Port = 4555
	cfg.Budget.MaxUSD = 99.95
	cfg.Budget.WarnAtPercent = 50
	cfg.Git.BranchPrefix = "work"

	if _, err := gen.Generate(RuntimeOpenCode); err != nil {
		t.Fatalf("regenerate error: %v", err)
	}

	after := readFile(t, filepath.Join(root, "opencode-instructions.md"))
	if before == after {
		t.Fatal("expected generated instructions to change after config update")
	}

	assertContainsAll(t, after,
		"http://localhost:4555",
		"$99.95",
		"warn threshold: 50%",
		"`work/<project-slug>`",
	)
}

func TestGeneratorWritesRuntimeNativeGuardrails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Default("Axiom Demo", "axiom-demo")
	gen := NewGenerator(root, &cfg)

	if _, err := gen.Generate(RuntimeClaudeCode); err != nil {
		t.Fatalf("Generate(claude-code) error: %v", err)
	}
	if _, err := gen.Generate(RuntimeOpenCode); err != nil {
		t.Fatalf("Generate(opencode) error: %v", err)
	}

	var claudeSettings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	settingsBytes := []byte(readFile(t, filepath.Join(root, ".claude", "settings.json")))
	if err := json.Unmarshal(settingsBytes, &claudeSettings); err != nil {
		t.Fatalf("parsing Claude settings: %v", err)
	}
	if len(claudeSettings.Hooks["PreToolUse"]) == 0 {
		t.Fatal("expected Claude settings to define PreToolUse hooks")
	}

	opencodeConfig := readFile(t, filepath.Join(root, "opencode.json"))
	assertContainsAll(t, opencodeConfig,
		"\"instructions\"",
		"\"opencode-instructions.md\"",
		"\"permission\"",
		"\"edit\": \"ask\"",
		"\"bash\": \"ask\"",
	)
}

func TestGeneratorRejectsUnsupportedRuntime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Default("Axiom Demo", "axiom-demo")
	gen := NewGenerator(root, &cfg)

	if _, err := gen.Generate(Runtime("invalid-runtime")); err == nil {
		t.Fatal("expected invalid runtime error")
	} else {
		assertContainsAll(t, err.Error(), "invalid runtime", "claw", "claude-code", "codex", "opencode")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func assertContainsAll(t *testing.T, content string, want ...string) {
	t.Helper()
	for _, needle := range want {
		if !strings.Contains(content, needle) {
			t.Fatalf("expected content to contain %q\ncontent:\n%s", needle, content)
		}
	}
}
