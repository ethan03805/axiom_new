// Package validation implements the hermetic validation sandbox per
// Architecture Sections 13 and 14.2 (Stage 2). It orchestrates running
// automated checks (compile, lint, test, security) against untrusted
// Meeseeks output in isolated Docker containers.
package validation

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

// CheckResult holds the outcome of a single validation check.
type CheckResult struct {
	CheckType  state.ValidationCheckType
	Status     state.ValidationStatus
	Output     string
	DurationMs int64
}

// CheckRequest describes what to validate.
type CheckRequest struct {
	TaskID     string
	RunID      string
	Image      string
	StagingDir string
	ProjectDir string
	Config     *config.ValidationConfig
	Languages  []string
}

// SandboxParams holds the parameters for building a validation sandbox container spec.
type SandboxParams struct {
	TaskID     string
	RunID      string
	Image      string
	StagingDir string
	ProjectDir string
	Config     *config.ValidationConfig
}

// CheckRunner abstracts the execution of validation checks inside a container.
// This allows tests to inject mock runners.
type CheckRunner interface {
	Run(ctx context.Context, containerID string, languages []string, securityScan bool) []CheckResult
}

// ServiceOptions configures a new validation Service.
type ServiceOptions struct {
	Containers engine.ContainerService
	Log        *slog.Logger
	Runner     CheckRunner
}

// Service orchestrates validation sandbox lifecycle and check execution.
type Service struct {
	containers engine.ContainerService
	log        *slog.Logger
	runner     CheckRunner
}

// NewService creates a new validation Service.
func NewService(opts ServiceOptions) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		containers: opts.Containers,
		log:        log,
		runner:     opts.Runner,
	}
}

// RunChecks orchestrates the full validation pipeline:
// 1. Build and start a validation sandbox container
// 2. Run checks (compile, lint, test, optional security)
// 3. Collect results
// 4. Destroy the sandbox container
//
// Per Architecture Section 13.3, the sandbox has:
//   - Read-only snapshot of project at HEAD
//   - Writable overlay with Meeseeks output applied
//   - No network, no secrets, resource-limited
func (s *Service) RunChecks(ctx context.Context, req CheckRequest) ([]CheckResult, error) {
	// Build container spec
	spec := BuildSandboxSpec(SandboxParams{
		TaskID:     req.TaskID,
		RunID:      req.RunID,
		Image:      req.Image,
		StagingDir: req.StagingDir,
		ProjectDir: req.ProjectDir,
		Config:     req.Config,
	})

	// Start sandbox container
	containerID, err := s.containers.Start(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("starting validation sandbox: %w", err)
	}

	// Always clean up the container
	defer func() {
		if stopErr := s.containers.Stop(ctx, containerID); stopErr != nil {
			s.log.Warn("failed to stop validation sandbox",
				"container", containerID, "error", stopErr)
		}
	}()

	s.log.Info("validation sandbox started",
		"container", containerID,
		"task", req.TaskID,
		"languages", req.Languages,
	)

	// Run checks
	securityScan := req.Config != nil && req.Config.SecurityScan
	results := s.runner.Run(ctx, containerID, req.Languages, securityScan)

	return results, nil
}

// BuildSandboxSpec constructs a container spec for the validation sandbox.
// Per Architecture Section 13.3:
//   - Network MUST be "none"
//   - Project is mounted read-only
//   - Staging overlay is mounted read-write
//   - No secrets are injected
func BuildSandboxSpec(params SandboxParams) engine.ContainerSpec {
	name := fmt.Sprintf("axiom-validator-%s", params.TaskID)

	cpuLimit := 1.0
	memLimit := "4g"
	timeoutMs := int64(600000) // 10 minutes default

	if params.Config != nil {
		if params.Config.CPULimit > 0 {
			cpuLimit = params.Config.CPULimit
		}
		if params.Config.MemLimit != "" {
			memLimit = params.Config.MemLimit
		}
		if params.Config.TimeoutMinutes > 0 {
			timeoutMs = int64(params.Config.TimeoutMinutes) * 60 * 1000
		}
	}

	// Mounts: project read-only, staging read-write
	// Per Section 13.3: read-only snapshot of project at HEAD
	var mounts []string
	if params.ProjectDir != "" {
		mounts = append(mounts, params.ProjectDir+":/workspace/project:ro")
	}
	if params.StagingDir != "" {
		mounts = append(mounts, params.StagingDir+":/workspace/staging:rw")
	}

	return engine.ContainerSpec{
		Name:      name,
		Image:     params.Image,
		CPULimit:  cpuLimit,
		MemLimit:  memLimit,
		Network:   "none", // MUST be none per Section 13.3
		Mounts:    mounts,
		TimeoutMs: timeoutMs,
		Env: map[string]string{
			"AXIOM_CONTAINER_TYPE": string(state.ContainerValidator),
			"AXIOM_TASK_ID":        params.TaskID,
			"AXIOM_RUN_ID":         params.RunID,
		},
	}
}

// AllPassed returns true if all check results passed or were skipped.
func AllPassed(results []CheckResult) bool {
	for _, r := range results {
		if r.Status == state.ValidationFail {
			return false
		}
	}
	return true
}

// FormatResults produces a human-readable summary of validation results.
// This is included in the ReviewSpec per Section 13.9.
func FormatResults(results []CheckResult) string {
	var b strings.Builder
	for _, r := range results {
		var icon string
		switch r.Status {
		case state.ValidationPass:
			icon = "PASS"
		case state.ValidationFail:
			icon = "FAIL"
		case state.ValidationSkip:
			icon = "SKIP"
		}
		fmt.Fprintf(&b, "%s: %s (%dms)\n", r.CheckType, icon, r.DurationMs)
		if r.Output != "" {
			fmt.Fprintf(&b, "  Output: %s\n", r.Output)
		}
	}
	return b.String()
}

// --- Language detection ---

// DetectLanguages inspects a project directory for language markers.
// Per Architecture Section 13.5, the engine detects project languages
// from configuration and applies appropriate validation profiles.
func DetectLanguages(projectDir string) []string {
	var langs []string

	markers := map[string]string{
		"go.mod":           "go",
		"package.json":     "node",
		"requirements.txt": "python",
		"pyproject.toml":   "python",
		"setup.py":         "python",
		"Cargo.toml":       "rust",
	}

	for file, lang := range markers {
		path := filepath.Join(projectDir, file)
		if _, err := os.Stat(path); err == nil {
			// Avoid duplicate languages
			found := false
			for _, l := range langs {
				if l == lang {
					found = true
					break
				}
			}
			if !found {
				langs = append(langs, lang)
			}
		}
	}

	sort.Strings(langs)
	return langs
}

// --- Language-specific validation profiles ---

// Profile defines the commands used for each validation check
// for a given language ecosystem per Architecture Section 13.5.
type Profile struct {
	Language   string
	CompileCmd string
	LintCmd    string
	TestCmd    string
	SecurityCmd string
	DepInstall string // dependency install command for hermetic builds
}

// GetProfile returns the validation profile for a language.
// Per Architecture Section 13.5:
//   - Go: vendored modules or read-only GOMODCACHE
//   - Node: npm ci --ignore-scripts --offline
//   - Python: pip install --no-index --find-links
//   - Rust: cargo with pre-populated registry
func GetProfile(lang string) Profile {
	switch lang {
	case "go":
		return Profile{
			Language:   "go",
			CompileCmd: "go build ./...",
			LintCmd:    "golangci-lint run ./...",
			TestCmd:    "go test ./...",
			SecurityCmd: "gosec ./...",
			DepInstall: "", // Go uses vendored modules or GOMODCACHE
		}
	case "node":
		return Profile{
			Language:   "node",
			CompileCmd: "npx tsc --noEmit",
			LintCmd:    "npx eslint .",
			TestCmd:    "npm test",
			SecurityCmd: "npx audit-ci",
			DepInstall: "npm ci --ignore-scripts --offline",
		}
	case "python":
		return Profile{
			Language:   "python",
			CompileCmd: "python -m py_compile",
			LintCmd:    "ruff check .",
			TestCmd:    "python -m pytest",
			SecurityCmd: "bandit -r .",
			DepInstall: "pip install --no-index --find-links /workspace/deps",
		}
	case "rust":
		return Profile{
			Language:   "rust",
			CompileCmd: "cargo build",
			LintCmd:    "cargo clippy -- -D warnings",
			TestCmd:    "cargo test",
			SecurityCmd: "cargo audit",
			DepInstall: "", // Uses pre-populated cargo registry
		}
	default:
		return Profile{}
	}
}
