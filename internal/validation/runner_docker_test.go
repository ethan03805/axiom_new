package validation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

// scriptedExec is a lightweight ContainerService fake whose Exec method looks
// up scripted ExecResults by the joined command string. It is shared by every
// DockerCheckRunner test in this file.
type scriptedExec struct {
	calls   [][]string
	results map[string]engine.ExecResult
	err     error
}

func (s *scriptedExec) Start(context.Context, engine.ContainerSpec) (string, error) {
	return "sandbox-1", nil
}
func (s *scriptedExec) Stop(context.Context, string) error            { return nil }
func (s *scriptedExec) ListRunning(context.Context) ([]string, error) { return nil, nil }
func (s *scriptedExec) Cleanup(context.Context) error                 { return nil }

func (s *scriptedExec) Exec(_ context.Context, _ string, cmd []string) (engine.ExecResult, error) {
	s.calls = append(s.calls, cmd)
	if s.err != nil {
		return engine.ExecResult{}, s.err
	}
	key := strings.Join(cmd, " ")
	if r, ok := s.results[key]; ok {
		return r, nil
	}
	return engine.ExecResult{ExitCode: 0, Duration: time.Millisecond}, nil
}

func TestDockerCheckRunner_GoProfile_AllPass(t *testing.T) {
	exec := &scriptedExec{results: map[string]engine.ExecResult{}}
	runner := NewDockerCheckRunner(exec, testLogger())

	results := runner.Run(context.Background(), "sandbox-1", []string{"go"}, false)

	if !AllPassed(results) {
		t.Fatalf("expected all pass, got %+v", results)
	}
	// compile + lint + test = 3 results
	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}
	// Check the first command was go build.
	if len(exec.calls) < 1 {
		t.Fatal("expected at least one exec call")
	}
	first := strings.Join(exec.calls[0], " ")
	if !strings.Contains(first, "go build ./...") {
		t.Fatalf("first exec = %q, want it to contain 'go build ./...'", first)
	}
}

func TestDockerCheckRunner_GoProfile_CompileFailBlocks(t *testing.T) {
	exec := &scriptedExec{results: map[string]engine.ExecResult{
		"sh -c go build ./...": {
			ExitCode: 2,
			Stderr:   "main.go:1:1: expected 'package', found EOF",
			Duration: 10 * time.Millisecond,
		},
	}}
	runner := NewDockerCheckRunner(exec, testLogger())

	results := runner.Run(context.Background(), "sandbox-1", []string{"go"}, false)

	if AllPassed(results) {
		t.Fatalf("expected compile failure, got all pass: %+v", results)
	}
	foundCompileFail := false
	for _, r := range results {
		if r.CheckType == state.CheckCompile && r.Status == state.ValidationFail {
			foundCompileFail = true
			if !strings.Contains(r.Output, "expected 'package'") {
				t.Errorf("compile failure output missing stderr: %q", r.Output)
			}
		}
	}
	if !foundCompileFail {
		t.Fatal("expected a failing compile CheckResult")
	}
}

func TestDockerCheckRunner_ExecInfraErrorFailsClosed(t *testing.T) {
	exec := &scriptedExec{err: errors.New("docker daemon down")}
	runner := NewDockerCheckRunner(exec, testLogger())

	results := runner.Run(context.Background(), "sandbox-1", []string{"go"}, false)

	if AllPassed(results) {
		t.Fatal("expected fail-closed result when docker exec errors")
	}
	// At least one result should reference the infra error.
	foundInfra := false
	for _, r := range results {
		if r.Status == state.ValidationFail && strings.Contains(r.Output, "infrastructure error") {
			foundInfra = true
		}
	}
	if !foundInfra {
		t.Fatalf("expected an infrastructure error result, got %+v", results)
	}
}

func TestDockerCheckRunner_SecurityScanOnlyWhenEnabled(t *testing.T) {
	exec := &scriptedExec{results: map[string]engine.ExecResult{}}
	runner := NewDockerCheckRunner(exec, testLogger())

	withoutSec := runner.Run(context.Background(), "sandbox-1", []string{"go"}, false)
	for _, r := range withoutSec {
		if r.CheckType == state.CheckSecurity {
			t.Fatal("security check must be skipped when securityScan=false")
		}
	}
	execCallsBefore := len(exec.calls)

	withSec := runner.Run(context.Background(), "sandbox-1", []string{"go"}, true)
	foundSec := false
	for _, r := range withSec {
		if r.CheckType == state.CheckSecurity {
			foundSec = true
		}
	}
	if !foundSec {
		t.Fatal("security check must be present when securityScan=true")
	}
	if len(exec.calls) == execCallsBefore {
		t.Fatal("security scan should have triggered an additional exec call")
	}
}

func TestDockerCheckRunner_UnknownLanguageProducesSkip(t *testing.T) {
	exec := &scriptedExec{results: map[string]engine.ExecResult{}}
	runner := NewDockerCheckRunner(exec, testLogger())

	results := runner.Run(context.Background(), "sandbox-1", []string{"cobol"}, false)

	if len(results) == 0 {
		t.Fatal("expected at least one skip result for unknown language")
	}
	for _, r := range results {
		if r.Status != state.ValidationSkip {
			t.Errorf("unknown language result = %+v, want skip", r)
		}
	}
}

func TestDockerCheckRunner_NoLanguagesProducesSkip(t *testing.T) {
	exec := &scriptedExec{results: map[string]engine.ExecResult{}}
	runner := NewDockerCheckRunner(exec, testLogger())

	results := runner.Run(context.Background(), "sandbox-1", nil, false)

	if len(results) == 0 {
		t.Fatal("expected a skip result for empty languages")
	}
	for _, r := range results {
		if r.Status != state.ValidationSkip {
			t.Errorf("empty languages result = %+v, want skip", r)
		}
	}
}

func TestDockerCheckRunner_DependencyCacheMissDetected(t *testing.T) {
	exec := &scriptedExec{results: map[string]engine.ExecResult{
		"sh -c go build ./...": {
			ExitCode: 1,
			Stderr:   "go: dependency_cache_miss for lockfile hash abc123",
		},
	}}
	runner := NewDockerCheckRunner(exec, testLogger())

	results := runner.Run(context.Background(), "sandbox-1", []string{"go"}, false)

	foundCacheMiss := false
	for _, r := range results {
		if r.CheckType == state.CheckCompile && r.Status == state.ValidationFail {
			if strings.Contains(r.Output, "dependency_cache_miss") {
				foundCacheMiss = true
			}
		}
	}
	if !foundCacheMiss {
		t.Fatalf("expected dependency_cache_miss to be surfaced in compile result, got %+v", results)
	}
}
