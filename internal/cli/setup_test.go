package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateHome redirects the platform-specific home directory resolution to a
// temporary directory for the duration of the test. On Windows
// os.UserHomeDir reads USERPROFILE; elsewhere it reads HOME.
//
// It also neutralizes PATH so the docker probe deterministically reports
// unavailable. Tests that want docker present should reverse this.
func isolateHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("PATH", dir)
	return dir
}

// runSetup invokes setupAction directly with controlled stdin/stdout. Setup
// writes real files under ~/.axiom so the caller must have already isolated
// HOME via isolateHome.
func runSetup(t *testing.T, input string) string {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(input)
	if err := setupAction(context.Background(), in, &out, false); err != nil {
		t.Fatalf("setupAction returned error: %v", err)
	}
	return out.String()
}

func TestSetTOMLField(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		section string
		key     string
		value   string
		want    string
	}{
		{
			name:    "empty file creates section",
			input:   "",
			section: "inference",
			key:     "openrouter_api_key",
			value:   `"sk-or-abc"`,
			want:    "[inference]\nopenrouter_api_key = \"sk-or-abc\"\n",
		},
		{
			name:    "appends section to existing file",
			input:   "[project]\nname = \"demo\"\n",
			section: "bitnet",
			key:     "enabled",
			value:   "false",
			want:    "[project]\nname = \"demo\"\n\n[bitnet]\nenabled = false\n",
		},
		{
			name:    "updates existing key in place",
			input:   "[inference]\nopenrouter_api_key = \"old\"\nopenrouter_base_url = \"https://x\"\n",
			section: "inference",
			key:     "openrouter_api_key",
			value:   `"new"`,
			want:    "[inference]\nopenrouter_api_key = \"new\"\nopenrouter_base_url = \"https://x\"\n",
		},
		{
			name:    "inserts new key into existing section",
			input:   "[bitnet]\nenabled = true\n\n[docker]\nimage = \"x\"\n",
			section: "bitnet",
			key:     "command",
			value:   `"./srv"`,
			want:    "[bitnet]\nenabled = true\ncommand = \"./srv\"\n\n[docker]\nimage = \"x\"\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := setTOMLField(tc.input, tc.section, tc.key, tc.value)
			if got != tc.want {
				t.Fatalf("setTOMLField mismatch\n---want---\n%q\n---got----\n%q", tc.want, got)
			}
		})
	}
}

func TestSetupKeyAlreadySet(t *testing.T) {
	home := isolateHome(t)
	cfgDir := filepath.Join(home, ".axiom")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(cfgDir, "config.toml")
	original := "[inference]\nopenrouter_api_key = \"sk-or-existingKEY9999\"\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatalf("writing initial config: %v", err)
	}

	// Provide blank answers for docker-build prompt (N) and bitnet (keep).
	// Key step will NOT prompt because key is already set.
	output := runSetup(t, "\n\n")

	if !strings.Contains(output, "OpenRouter key present") {
		t.Fatalf("expected 'OpenRouter key present' in output, got:\n%s", output)
	}
	if strings.Contains(output, "Paste your OpenRouter API key") {
		t.Fatalf("should not have prompted for key when it was already set:\n%s", output)
	}

	// Config file must be byte-identical (no round-trip clobber).
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading config after setup: %v", err)
	}
	if string(after) != original {
		t.Fatalf("config was modified unexpectedly\nbefore: %q\nafter:  %q", original, string(after))
	}
}

func TestSetupWritesKey(t *testing.T) {
	home := isolateHome(t)

	// No pre-existing config. User pastes a key and skips the other prompts.
	input := "sk-or-freshKEY1234\n\n\n"
	output := runSetup(t, input)

	if !strings.Contains(output, "Saved OpenRouter key") {
		t.Fatalf("expected 'Saved OpenRouter key' in output, got:\n%s", output)
	}

	configPath := filepath.Join(home, ".axiom", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("expected config file to be written at %s: %v", configPath, err)
	}
	content := string(data)
	if !strings.Contains(content, "[inference]") {
		t.Fatalf("expected [inference] section, got:\n%s", content)
	}
	if !strings.Contains(content, `openrouter_api_key = "sk-or-freshKEY1234"`) {
		t.Fatalf("expected openrouter_api_key to be written, got:\n%s", content)
	}
}

func TestSetupBitNetManagedMode(t *testing.T) {
	home := isolateHome(t)

	// Seed config with a key already present so step 1 does not prompt.
	cfgDir := filepath.Join(home, ".axiom")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(cfgDir, "config.toml")
	seed := "[inference]\nopenrouter_api_key = \"sk-or-seedKEY0000\"\n"
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("writing seed config: %v", err)
	}

	// isolateHome stripped PATH, so docker is unreachable and no
	// docker-build prompt fires. Inputs are strictly:
	//   1. bitnet choice — "3" (managed)
	//   2. command — "./bitnet-server"
	//   3. working_dir — "/opt/bitnet"
	input := "3\n./bitnet-server\n/opt/bitnet\n"
	output := runSetup(t, input)

	if !strings.Contains(output, "BitNet set to managed mode") {
		t.Fatalf("expected managed-mode confirmation, got:\n%s", output)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[bitnet]") {
		t.Fatalf("expected [bitnet] section, got:\n%s", content)
	}
	if !strings.Contains(content, "enabled = true") {
		t.Fatalf("expected bitnet.enabled = true, got:\n%s", content)
	}
	if !strings.Contains(content, `command = "./bitnet-server"`) {
		t.Fatalf("expected bitnet.command to be written, got:\n%s", content)
	}
	if !strings.Contains(content, `working_dir = "/opt/bitnet"`) {
		t.Fatalf("expected bitnet.working_dir to be written, got:\n%s", content)
	}
	// Key must still be present after the BitNet edits (no clobber).
	if !strings.Contains(content, `openrouter_api_key = "sk-or-seedKEY0000"`) {
		t.Fatalf("seed key was clobbered, got:\n%s", content)
	}
}
