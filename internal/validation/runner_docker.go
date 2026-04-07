package validation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

// DockerCheckRunner executes language-specific build, test, and lint commands
// inside a running validation sandbox container via docker exec.
// Implements the CheckRunner interface used by validation.Service.
//
// Per Architecture Sections 13.3 and 13.5, this is the component that turns
// profile commands (go build ./..., golangci-lint run ./..., go test ./...
// and the Node/Python/Rust equivalents) into CheckResult records so the
// merge queue (Stage 5) and Stage 2 validator both run real integration
// checks against staged Meeseeks output.
type DockerCheckRunner struct {
	containers engine.ContainerService
	log        *slog.Logger
}

// NewDockerCheckRunner constructs a runner that runs checks inside a
// validation sandbox started by validation.Service.
func NewDockerCheckRunner(containers engine.ContainerService, log *slog.Logger) *DockerCheckRunner {
	if log == nil {
		log = slog.Default()
	}
	return &DockerCheckRunner{containers: containers, log: log}
}

// Run executes the language profiles for each language in the request inside
// the running sandbox container. It always returns at least one result per
// (language, check type) pair so upstream reporting is consistent.
//
// Per Architecture Section 13.5, the order is: compile → lint → test →
// security. On any infrastructure error (docker daemon down, container
// missing), Run emits a failing CheckResult rather than a silent pass —
// the merge queue must never commit on infra failures.
func (r *DockerCheckRunner) Run(ctx context.Context, containerID string, languages []string, securityScan bool) []CheckResult {
	if len(languages) == 0 {
		return []CheckResult{{
			CheckType: state.CheckCompile,
			Status:    state.ValidationSkip,
			Output:    "no languages detected in project",
		}}
	}

	var results []CheckResult
	for _, lang := range languages {
		profile := GetProfile(lang)
		if profile.Language == "" {
			results = append(results, CheckResult{
				CheckType: state.CheckCompile,
				Status:    state.ValidationSkip,
				Output:    fmt.Sprintf("unsupported language: %s", lang),
			})
			continue
		}

		results = append(results, r.runOne(ctx, containerID, state.CheckCompile, profile.CompileCmd))
		results = append(results, r.runOne(ctx, containerID, state.CheckLint, profile.LintCmd))
		results = append(results, r.runOne(ctx, containerID, state.CheckTest, profile.TestCmd))
		if securityScan && profile.SecurityCmd != "" {
			results = append(results, r.runOne(ctx, containerID, state.CheckSecurity, profile.SecurityCmd))
		}
	}
	return results
}

func (r *DockerCheckRunner) runOne(ctx context.Context, containerID string, checkType state.ValidationCheckType, commandLine string) CheckResult {
	if commandLine == "" {
		return CheckResult{CheckType: checkType, Status: state.ValidationSkip}
	}
	// Wrap in `sh -c` so the profile commands (which may use pipes or
	// relative paths) run in a shell inside the sandbox. Always cd into
	// /workspace/project first — validation.BuildSandboxSpec mounts the
	// project there, and the base image's default cwd is not guaranteed
	// to match. This fixes the stage 5 path where StagingDir is empty
	// and only /workspace/project is mounted.
	cmd := []string{"sh", "-c", "cd /workspace/project && " + commandLine}
	start := time.Now()
	execResult, err := r.containers.Exec(ctx, containerID, cmd)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		r.log.Warn("docker exec failed during validation",
			"check", checkType, "error", err)
		return CheckResult{
			CheckType:  checkType,
			Status:     state.ValidationFail,
			Output:     fmt.Sprintf("infrastructure error running %s: %v", checkType, err),
			DurationMs: duration,
		}
	}

	// Dependency cache miss detection per Architecture Section 13.5.
	combined := strings.TrimSpace(execResult.Stderr + "\n" + execResult.Stdout)
	if strings.Contains(strings.ToLower(combined), "dependency_cache_miss") {
		return CheckResult{
			CheckType:  checkType,
			Status:     state.ValidationFail,
			Output:     "dependency_cache_miss: " + combined,
			DurationMs: duration,
		}
	}

	if execResult.ExitCode == 0 {
		return CheckResult{
			CheckType:  checkType,
			Status:     state.ValidationPass,
			Output:     "",
			DurationMs: duration,
		}
	}

	return CheckResult{
		CheckType:  checkType,
		Status:     state.ValidationFail,
		Output:     combined,
		DurationMs: duration,
	}
}
