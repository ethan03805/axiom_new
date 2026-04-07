package engine_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/validation"
)

// scriptedExecContainers is a lightweight engine.ContainerService fake whose
// Exec method looks up scripted ExecResults by the joined command string.
// It is used by the end-to-end merge-queue integration test below to prove
// that a failing `go build` prevents the merge queue from committing.
type scriptedExecContainers struct {
	execResults map[string]engine.ExecResult
	execCalls   atomic.Int64
}

func (s *scriptedExecContainers) Start(context.Context, engine.ContainerSpec) (string, error) {
	return "sandbox-integration", nil
}
func (s *scriptedExecContainers) Stop(context.Context, string) error            { return nil }
func (s *scriptedExecContainers) ListRunning(context.Context) ([]string, error) { return nil, nil }
func (s *scriptedExecContainers) Cleanup(context.Context) error                 { return nil }
func (s *scriptedExecContainers) Exec(_ context.Context, _ string, cmd []string) (engine.ExecResult, error) {
	s.execCalls.Add(1)
	key := strings.Join(cmd, " ")
	if r, ok := s.execResults[key]; ok {
		return r, nil
	}
	// Unknown commands succeed silently so the lint/test passes don't mask
	// the compile failure we're asserting on.
	return engine.ExecResult{ExitCode: 0, Duration: time.Millisecond}, nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestMergeQueue_RealValidator_BlocksBrokenGoBuild is the acceptance test
// for Issue 04. It wires the real validation.Service + DockerCheckRunner
// behind engine's internal merge-queue validator adapter with a scripted
// `docker exec` that returns a failing `go build`. It asserts:
//
//   - RunIntegrationChecks returns (false, feedback, nil) so the merge
//     queue will NOT commit.
//   - The feedback contains the simulated compile error.
//   - The scripted Exec was actually invoked — this guards against
//     regressions where the adapter silently bypasses the real runner.
//
// Per Architecture Section 23.3, this is the safety guarantee the merge
// queue must honor.
func TestMergeQueue_RealValidator_BlocksBrokenGoBuild(t *testing.T) {
	projectDir := t.TempDir()
	// Seed a go.mod so detectValidationLanguages returns ["go"].
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module broken\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}

	scripted := &scriptedExecContainers{
		execResults: map[string]engine.ExecResult{
			"sh -c go build ./...": {
				ExitCode: 2,
				Stderr:   "pkg/foo.go:1:1: expected 'package', found 'bad'",
				Duration: 5 * time.Millisecond,
			},
		},
	}

	runner := validation.NewDockerCheckRunner(scripted, quietLogger())
	svc := validation.NewService(validation.ServiceOptions{
		Containers: scripted,
		Log:        quietLogger(),
		Runner:     runner,
	})
	engineAdapter := validation.NewEngineAdapter(svc)

	cfg := &config.Config{
		Docker:     config.DockerConfig{Image: "axiom-meeseeks-multi:latest"},
		Validation: config.ValidationConfig{Network: "none", TimeoutMinutes: 10},
	}

	passed, feedback, err := engine.RunMergeQueueIntegrationChecksForTest(
		context.Background(), engineAdapter, cfg, projectDir,
	)
	if err != nil {
		t.Fatalf("RunMergeQueueIntegrationChecksForTest: unexpected error %v", err)
	}
	if passed {
		t.Fatal("expected integration checks to FAIL on broken go build; merge queue would have committed")
	}
	if !strings.Contains(feedback, "expected 'package'") {
		t.Fatalf("feedback missing simulated compile error: %q", feedback)
	}

	if scripted.execCalls.Load() == 0 {
		t.Fatal("expected the real validator to call docker exec at least once — adapter must not bypass the runner")
	}
}

// TestMergeQueue_RealValidator_PassesCleanGoBuild complements the broken
// case above: if docker exec succeeds for every profile command, the
// adapter returns (true, "...", nil) so the merge queue is free to commit.
// This guards against regressions where the real runner accidentally
// fails closed even on a clean build.
func TestMergeQueue_RealValidator_PassesCleanGoBuild(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module clean\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}

	scripted := &scriptedExecContainers{execResults: map[string]engine.ExecResult{}}

	runner := validation.NewDockerCheckRunner(scripted, quietLogger())
	svc := validation.NewService(validation.ServiceOptions{
		Containers: scripted,
		Log:        quietLogger(),
		Runner:     runner,
	})
	engineAdapter := validation.NewEngineAdapter(svc)

	cfg := &config.Config{
		Docker:     config.DockerConfig{Image: "axiom-meeseeks-multi:latest"},
		Validation: config.ValidationConfig{Network: "none", TimeoutMinutes: 10},
	}

	passed, _, err := engine.RunMergeQueueIntegrationChecksForTest(
		context.Background(), engineAdapter, cfg, projectDir,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passed {
		t.Fatal("expected integration checks to pass on a clean build")
	}
	if scripted.execCalls.Load() == 0 {
		t.Fatal("expected the real runner to call docker exec on the clean path too")
	}
}

// TestMergeQueue_RealValidator_InfraErrorFailsClosed proves the merge
// queue fails closed on docker infra errors: an Exec failure MUST NOT be
// turned into a silent pass. Uses a container service whose Exec always
// errors.
func TestMergeQueue_RealValidator_InfraErrorFailsClosed(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module broken\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}

	scripted := &erroringExecContainers{}
	runner := validation.NewDockerCheckRunner(scripted, quietLogger())
	svc := validation.NewService(validation.ServiceOptions{
		Containers: scripted,
		Log:        quietLogger(),
		Runner:     runner,
	})
	engineAdapter := validation.NewEngineAdapter(svc)

	cfg := &config.Config{
		Docker:     config.DockerConfig{Image: "axiom-meeseeks-multi:latest"},
		Validation: config.ValidationConfig{Network: "none", TimeoutMinutes: 10},
	}

	passed, feedback, err := engine.RunMergeQueueIntegrationChecksForTest(
		context.Background(), engineAdapter, cfg, projectDir,
	)
	if err != nil {
		t.Fatalf("RunIntegrationChecks returned error (should surface as failed check, not error): %v", err)
	}
	if passed {
		t.Fatalf("expected fail-closed on infra error, got pass; feedback=%q", feedback)
	}
	if !strings.Contains(feedback, "infrastructure error") {
		t.Fatalf("feedback should mention infrastructure error, got %q", feedback)
	}
}

// erroringExecContainers is a ContainerService whose Exec always returns an
// infrastructure-style error (e.g. docker daemon down).
type erroringExecContainers struct{}

func (erroringExecContainers) Start(context.Context, engine.ContainerSpec) (string, error) {
	return "sandbox-integration", nil
}
func (erroringExecContainers) Stop(context.Context, string) error            { return nil }
func (erroringExecContainers) ListRunning(context.Context) ([]string, error) { return nil, nil }
func (erroringExecContainers) Cleanup(context.Context) error                 { return nil }
func (erroringExecContainers) Exec(context.Context, string, []string) (engine.ExecResult, error) {
	return engine.ExecResult{}, errFakeDockerDown
}

// errFakeDockerDown is a sentinel for the infra-error test.
var errFakeDockerDown = &dockerDownError{}

type dockerDownError struct{}

func (*dockerDownError) Error() string { return "docker daemon not reachable" }
