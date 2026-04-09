package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/dockerassets"
	"github.com/spf13/cobra"
)

// SetupCmd creates the `axiom setup` command, a plain interactive CLI that
// walks the user through first-run prerequisites: OpenRouter credential,
// Docker availability, default image readiness, and BitNet mode selection.
//
// Unlike `axiom run` and `axiom tui`, this command does not call openApp() —
// its entire purpose is to unblock users whose environment is not yet
// complete enough for the app to compose.
func SetupCmd(verbose *bool) *cobra.Command {
	var nonInteractive bool

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Guided first-run setup for OpenRouter, Docker, and BitNet",
		Long: "Walks through the first-run prerequisites Axiom needs before it can\n" +
			"run projects: OpenRouter API key, Docker daemon, the default worker\n" +
			"image, and your BitNet operating mode. Writes changes to\n" +
			"~/.axiom/config.toml without clobbering unrelated fields.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return setupAction(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), nonInteractive)
		},
	}

	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "do not prompt; only report what would change")
	return cmd
}

// setupAction is the command's business logic, factored out for testing.
func setupAction(ctx context.Context, in io.Reader, out io.Writer, nonInteractive bool) error {
	if ctx == nil {
		ctx = context.Background()
	}

	scanner := bufio.NewScanner(in)
	// Allow long lines (API keys / paths) without scanner errors.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	globalPath, err := globalConfigPath()
	if err != nil {
		return fmt.Errorf("resolving global config path: %w", err)
	}

	fmt.Fprintln(out, "Axiom first-run setup")
	fmt.Fprintln(out, "=====================")
	fmt.Fprintf(out, "Global config: %s\n\n", globalPath)

	var changes []string

	// Step 1 — OpenRouter key
	fmt.Fprintln(out, "Step 1: OpenRouter API key")
	fmt.Fprintln(out, "--------------------------")
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}
	if existing := strings.TrimSpace(cfg.Inference.OpenRouterAPIKey); existing != "" {
		fmt.Fprintf(out, "OpenRouter key present (%s)\n\n", maskKey(existing))
	} else if nonInteractive {
		fmt.Fprintln(out, "OpenRouter key is missing. Re-run without --non-interactive to set it.")
		fmt.Fprintln(out)
	} else {
		fmt.Fprint(out, "Paste your OpenRouter API key (or press Enter to skip): ")
		key := readLine(scanner)
		key = strings.TrimSpace(key)
		if key != "" {
			if err := writeTOMLField(globalPath, "inference", "openrouter_api_key", quoteTOML(key)); err != nil {
				return fmt.Errorf("writing OpenRouter key: %w", err)
			}
			fmt.Fprintf(out, "Saved OpenRouter key to %s\n\n", globalPath)
			changes = append(changes, "wrote inference.openrouter_api_key")
		} else {
			fmt.Fprintln(out, "Skipped. You can re-run `axiom setup` later.")
			fmt.Fprintln(out)
		}
	}

	// Step 2 — Docker daemon
	fmt.Fprintln(out, "Step 2: Docker daemon")
	fmt.Fprintln(out, "---------------------")
	dockerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	dockerAvailable := dockerDaemonAvailable(dockerCtx)
	cancel()
	if dockerAvailable {
		fmt.Fprintln(out, "Docker daemon is reachable.")
		fmt.Fprintln(out)
	} else {
		fmt.Fprintln(out, "Docker is not reachable.")
		fmt.Fprintln(out, dockerRemediationHint())
		fmt.Fprintln(out, "Continuing setup without Docker. You can start Docker and re-run `axiom doctor` later.")
		fmt.Fprintln(out)
	}

	// Step 3 — Default image readiness
	fmt.Fprintln(out, "Step 3: Default worker image")
	fmt.Fprintln(out, "----------------------------")
	imageTag := dockerassets.DefaultImage
	buildCmd := dockerassets.DefaultBuildCommand
	if !dockerAvailable {
		fmt.Fprintln(out, "Skipping image check because Docker is not reachable.")
		fmt.Fprintln(out)
	} else {
		imgCtx, imgCancel := context.WithTimeout(ctx, 15*time.Second)
		imgErr := dockerImagePresent(imgCtx, imageTag)
		imgCancel()
		if imgErr == nil {
			fmt.Fprintf(out, "Image %s is present.\n\n", imageTag)
		} else {
			fmt.Fprintf(out, "Image %s is not present locally.\n", imageTag)
			fmt.Fprintf(out, "Build command: %s\n", buildCmd)
			if nonInteractive {
				fmt.Fprintln(out, "Re-run without --non-interactive to build now.")
				fmt.Fprintln(out)
			} else {
				fmt.Fprint(out, "Run this now? (y/N): ")
				answer := strings.ToLower(strings.TrimSpace(readLine(scanner)))
				if answer == "y" || answer == "yes" {
					if err := runDockerBuild(ctx, out); err != nil {
						fmt.Fprintf(out, "Image build failed: %v\n", err)
						fmt.Fprintln(out, "You can re-run `axiom setup` or run the build command manually.")
					} else {
						fmt.Fprintln(out, "Image built successfully.")
						changes = append(changes, "built docker image "+imageTag)
					}
				} else {
					fmt.Fprintln(out, "Skipped. Run the command above when you are ready.")
				}
				fmt.Fprintln(out)
			}
		}
	}

	// Step 4 — BitNet mode
	fmt.Fprintln(out, "Step 4: BitNet mode")
	fmt.Fprintln(out, "-------------------")
	fmt.Fprintln(out, "BitNet gives you a local fallback model. You have three options:")
	fmt.Fprintln(out, "  (1) disabled       - no BitNet fallback")
	fmt.Fprintln(out, "  (2) manual         - you start the BitNet server yourself")
	fmt.Fprintln(out, "  (3) managed        - Axiom launches and manages the BitNet server")
	currentMode := describeBitNetMode(cfg.BitNet)
	fmt.Fprintf(out, "Current: %s\n", currentMode)
	if nonInteractive {
		fmt.Fprintln(out, "Re-run without --non-interactive to change the BitNet mode.")
		fmt.Fprintln(out)
	} else {
		fmt.Fprint(out, "Choose [1/2/3, Enter=keep current]: ")
		choice := strings.TrimSpace(readLine(scanner))
		switch choice {
		case "":
			fmt.Fprintln(out, "Keeping current BitNet mode.")
		case "1":
			if err := writeTOMLField(globalPath, "bitnet", "enabled", "false"); err != nil {
				return fmt.Errorf("writing bitnet.enabled: %w", err)
			}
			fmt.Fprintln(out, "BitNet disabled.")
			changes = append(changes, "set bitnet.enabled = false")
		case "2":
			if err := writeTOMLField(globalPath, "bitnet", "enabled", "true"); err != nil {
				return fmt.Errorf("writing bitnet.enabled: %w", err)
			}
			if err := writeTOMLField(globalPath, "bitnet", "command", `""`); err != nil {
				return fmt.Errorf("writing bitnet.command: %w", err)
			}
			fmt.Fprintln(out, "BitNet set to manual mode. Start the server yourself.")
			changes = append(changes, "set bitnet to manual mode")
		case "3":
			fmt.Fprint(out, "Command to launch BitNet server (e.g. ./bitnet-server): ")
			command := strings.TrimSpace(readLine(scanner))
			fmt.Fprint(out, "Working directory (absolute path, blank for process cwd): ")
			workingDir := strings.TrimSpace(readLine(scanner))
			if command == "" {
				fmt.Fprintln(out, "Empty command; leaving BitNet mode unchanged.")
			} else {
				if err := writeTOMLField(globalPath, "bitnet", "enabled", "true"); err != nil {
					return fmt.Errorf("writing bitnet.enabled: %w", err)
				}
				if err := writeTOMLField(globalPath, "bitnet", "command", quoteTOML(command)); err != nil {
					return fmt.Errorf("writing bitnet.command: %w", err)
				}
				if err := writeTOMLField(globalPath, "bitnet", "working_dir", quoteTOML(workingDir)); err != nil {
					return fmt.Errorf("writing bitnet.working_dir: %w", err)
				}
				fmt.Fprintln(out, "BitNet set to managed mode.")
				changes = append(changes, "set bitnet to managed mode")
			}
		default:
			fmt.Fprintf(out, "Unrecognized choice %q; leaving BitNet mode unchanged.\n", choice)
		}
		fmt.Fprintln(out)
	}

	// Summary
	fmt.Fprintln(out, "Summary")
	fmt.Fprintln(out, "-------")
	if len(changes) == 0 {
		fmt.Fprintln(out, "No configuration changes were made.")
	} else {
		fmt.Fprintln(out, "Changes written:")
		for _, c := range changes {
			fmt.Fprintf(out, "  - %s\n", c)
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintln(out, "  1. axiom doctor            - verify everything looks healthy")
	fmt.Fprintln(out, "  2. cd your-project && axiom init")
	fmt.Fprintln(out, "  3. axiom run \"describe what you want to build\"")

	return nil
}

// globalConfigPath returns the path to ~/.axiom/config.toml without caring
// whether the file currently exists. It uses os.UserHomeDir, which on Windows
// honors USERPROFILE.
func globalConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".axiom", "config.toml"), nil
}

func readLine(scanner *bufio.Scanner) string {
	if !scanner.Scan() {
		return ""
	}
	return scanner.Text()
}

// maskKey returns a redacted display form of an API key.
func maskKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 4 {
		return "****"
	}
	return "sk-or-..." + key[len(key)-4:]
}

func describeBitNetMode(b config.BitNetConfig) string {
	if !b.Enabled {
		return "disabled"
	}
	if strings.TrimSpace(b.Command) == "" {
		return "manual"
	}
	return "managed (command=" + b.Command + ")"
}

// dockerDaemonAvailable probes the docker daemon using the same approach
// doctor uses internally. Keeping this local avoids a dependency on doctor's
// unexported dockerCLI helper.
func dockerDaemonAvailable(ctx context.Context) bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func dockerImagePresent(ctx context.Context, image string) error {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", image)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("image inspect failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func runDockerBuild(ctx context.Context, w io.Writer) error {
	// Split dockerassets.DefaultBuildCommand into fields so we can stream
	// stdout/stderr through the cobra output writer.
	parts := strings.Fields(dockerassets.DefaultBuildCommand)
	if len(parts) == 0 {
		return fmt.Errorf("empty build command")
	}
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}

func dockerRemediationHint() string {
	switch runtime.GOOS {
	case "windows":
		return "  Install Docker Desktop: https://www.docker.com/products/docker-desktop/\n  Make sure Docker Desktop is running before re-running `axiom setup`."
	case "darwin":
		return "  Install Docker Desktop: https://www.docker.com/products/docker-desktop/\n  Start Docker Desktop and re-run `axiom setup`."
	default:
		return "  Install Docker using your package manager (e.g. `sudo apt install docker.io`\n  or `sudo dnf install docker`), then start the service and add your user\n  to the docker group. Re-run `axiom setup` afterwards."
	}
}

// -----------------------------------------------------------------------------
// Narrow, targeted TOML writer
// -----------------------------------------------------------------------------
//
// We intentionally do NOT round-trip the full config through pelletier's
// marshaller. Doing so would emit every field in the Config struct, including
// empty ones, which caused the exact bug tracked by issues 9 and 10 where an
// empty project-level field shadowed the global value.
//
// Instead, writeTOMLField performs a minimal surgical edit against the
// existing file text. It ensures a `[section]` header exists and then either
// updates the existing `key = value` line inside that section or appends a
// new one. It never touches unrelated sections or fields.
//
// The keys we write from setup are a small, closed set (openrouter_api_key,
// enabled, command, working_dir), all of which use simple scalar TOML
// syntax, so a line-based editor is sufficient and avoids pulling in a
// round-trip-preserving TOML library.

// writeTOMLField creates the file (and parent directory) if necessary, then
// writes a single `key = value` pair under `[section]`.
func writeTOMLField(path, section, key, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	var original string
	if data, err := os.ReadFile(path); err == nil {
		original = string(data)
	} else if !os.IsNotExist(err) {
		return err
	}

	updated := setTOMLField(original, section, key, value)
	return os.WriteFile(path, []byte(updated), 0o600)
}

// setTOMLField is the pure-string half of writeTOMLField, exposed for tests.
// It treats the TOML file as a sequence of lines plus section headers.
func setTOMLField(original, section, key, value string) string {
	lines := splitLinesPreserving(original)

	headerLine := "[" + section + "]"
	assignment := key + " = " + value

	// Locate the target section.
	sectionStart := -1
	sectionEnd := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == headerLine {
			sectionStart = i
			// Find where this section ends — the next header or EOF.
			for j := i + 1; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
					sectionEnd = j
					break
				}
			}
			break
		}
	}

	if sectionStart == -1 {
		// Section missing: append it.
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, headerLine, assignment, "")
		return strings.Join(lines, "\n")
	}

	// Section present: look for an existing assignment for `key`.
	for i := sectionStart + 1; i < sectionEnd; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if eq := strings.Index(trimmed, "="); eq >= 0 {
			existingKey := strings.TrimSpace(trimmed[:eq])
			if existingKey == key {
				lines[i] = assignment
				return strings.Join(lines, "\n")
			}
		}
	}

	// Key not present: insert before any trailing blank lines of the section.
	insertAt := sectionEnd
	for insertAt > sectionStart+1 && strings.TrimSpace(lines[insertAt-1]) == "" {
		insertAt--
	}
	newLines := make([]string, 0, len(lines)+1)
	newLines = append(newLines, lines[:insertAt]...)
	newLines = append(newLines, assignment)
	newLines = append(newLines, lines[insertAt:]...)
	return strings.Join(newLines, "\n")
}

// splitLinesPreserving splits on '\n' without dropping the trailing empty
// element, so Join produces byte-identical output when no edits happen.
func splitLinesPreserving(s string) []string {
	if s == "" {
		return nil
	}
	// Normalize CRLF -> LF for editing, then split.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

// quoteTOML emits a TOML basic string literal for a scalar value. It escapes
// backslashes and double quotes but is not a general-purpose TOML encoder —
// it only needs to handle the narrow set of values the setup flow writes.
func quoteTOML(s string) string {
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}
