# Issue 04 — P0: Merge Queue Commits Without Real Integration Checks

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Stage-2 / Stage-5 validation sandbox actually run language-native build, test, and lint commands inside a hermetic Docker container so that broken staged output can never reach a commit — satisfying Architecture Sections 13.3, 13.5, and 23.3.

**Architecture:** Extend `engine.ContainerService` with an `Exec` primitive, then add a concrete `DockerCheckRunner` in `internal/validation/` that invokes the existing language profiles (`go build ./...`, `go test ./...`, `golangci-lint run ./...`, plus Node/Python/Rust equivalents) via `docker exec`, captures pass/fail/output, and returns `CheckResult` values. Wire the real runner into `app.Open()`; keep `FallbackRunner` as the fail-closed safety net for tests and missing-docker environments. Add a regression test where a scripted failing `go build` blocks the merge.

**Tech Stack:** Go 1.22, Docker CLI, `testing`, existing `internal/container`, `internal/validation`, `internal/mergequeue`, `internal/engine` packages.

---

## 1. Issue Statement (from `issues.md` §4)

> **P0: The merge queue currently commits code without real integration checks**
>
> `internal/engine/mergequeue.go:80-90` uses `mergeQueueValidatorAdapter.RunIntegrationChecks`, which always returns `(true, "", nil)`. … Once the merge path is wired, broken code can be committed even though the architecture promises project-wide validation before integration. This is a direct violation of the architecture's safety story for non-technical users.

## 2. Current State (post-Issue-03 fix)

Re-reproducing the issue against the code that shipped in commits `6c77326` (Issue 03 Plan) and `371af41` (Issue 03 fix):

### 2.1 What already changed

The Issue-03 fix **did** rewrite the merge-queue validator adapter so it is no longer a hardcoded pass-through. Verified in `internal/engine/mergequeue.go:90-116`:

```go
func (a *mergeQueueValidatorAdapter) RunIntegrationChecks(ctx context.Context, projectDir string) (bool, string, error) {
    if a.validation == nil {
        ...
        return true, "", nil  // only hit if validation is nil
    }
    ...
    results, err := a.validation.RunChecks(ctx, ValidationCheckRequest{
        TaskID:     "merge-queue",
        ProjectDir: projectDir,
        Config:     validationCfg,
        Languages:  detectValidationLanguages(projectDir),
    })
    if err != nil {
        return false, "", err
    }
    return validationAllPassed(results), formatValidationResults(results), nil
}
```

The engine composition root (`internal/engine/engine.go:116`) wires this adapter into the merge queue, and the merge queue (`internal/mergequeue/mergequeue.go:266-281`) now calls it during every merge attempt. The Issue-03 fix also introduced `internal/validation/fallback_runner.go`, which fails closed instead of silently passing.

### 2.2 What is still broken — the real defect

The issue was only partially closed. The *adapter* is wired, but the validation service still has **no concrete check runner** that executes anything. The entire chain from merge-queue → validation service → sandbox container stops at a stub:

1. **`app.go:86-90` wires `validation.FallbackRunner{}` as the production runner:**

    ```go
    validationSvc := validation.NewService(validation.ServiceOptions{
        Containers: containerSvc,
        Log:        log,
        Runner:     validation.FallbackRunner{},
    })
    ```

2. **`internal/validation/fallback_runner.go` always returns one failing check:**

    ```go
    func (FallbackRunner) Run(_ context.Context, _ string, _ []string, _ bool) []CheckResult {
        return []CheckResult{{
            CheckType:  state.CheckCompile,
            Status:     state.ValidationFail,
            Output:     "validation runner is not configured",
            DurationMs: 0,
        }}
    }
    ```

3. A codebase-wide search (`Grep "CheckRunner"`) finds **only** the `FallbackRunner` and the test-only `mockCheckRunner` — there is no real implementation. The `Profile` table in `internal/validation/validation.go:272-312` defines the commands (`go build ./...`, `go test ./...`, `golangci-lint run ./...`, `npm test`, `cargo test`, `python -m pytest`, …) but nothing ever invokes them.

4. **`engine.ContainerService`** (`internal/engine/interfaces.go:37-43`) only exposes `Start`, `Stop`, `ListRunning`, `Cleanup` — there is no `Exec` method, so a future `DockerCheckRunner` has no primitive to run `docker exec <container> go build ./...`. The docs in `docs/approval-pipeline.md:180-183` explicitly acknowledge this: *"The default app composition currently uses a fail-closed fallback runner until a concrete in-container check runner is configured."*

### 2.3 Observable behavior

Because the production wire is `FallbackRunner{}`:

- **Stage 2** validation in `internal/engine/executor.go:151-173` always reports a failing compile check.
- Every attempt enters `failAttempt`, feedback is recorded, and the scheduler retries → escalates → eventually marks the task failed.
- **Stage 5** (merge queue) is never reached because stage 2 never passes, so the original headline symptom ("broken code commits") has flipped into "no code can ever commit". Both violate Architecture §23.3 — the promise of real build/test/lint checks is still unfulfilled.
- The existing merge-queue unit tests (`internal/mergequeue/mergequeue_test.go`) inject a `mockValidator` with `pass: true` or `pass: false`, so they pass. The pipeline-level test (`internal/engine/executor_test.go:154-209`) injects a `mockValidationService` that always returns pass. **No test in the repo runs the real validator against real code**, so the bug does not surface in CI.

### 2.4 Root cause

Phase 11 (Architecture §13.5) specified language profiles, and Phase 12 (Architecture §16.4 / §23.3) wired the merge-queue adapter, but **the concrete in-container check runner — the component that actually turns the profile commands into `docker exec` calls and parses their exit codes — was never built.** Issue 03's fix closed the "silent pass" hole by swapping the stub for `FallbackRunner`, but left the deeper gap open: there is no implementation that executes language commands inside the sandbox, because `engine.ContainerService` has no `Exec` primitive and no validation package type knows how to drive one.

---

## 3. Fix Strategy

Build the missing link end-to-end, one bite-sized step at a time:

1. Extend `engine.ContainerService` with `Exec(ctx, id, cmd []string) (ExecResult, error)` and implement it in `internal/container/docker.go` using `docker exec`.
2. Add a `DockerCheckRunner` in `internal/validation/` that uses the new `Exec` primitive, iterates over `GetProfile(lang)` commands, runs compile → lint → test (→ security if enabled), maps each exit code to `state.ValidationPass`/`Fail`, and fails closed on any internal error. Detect `dependency_cache_miss` per Architecture §13.5.
3. Wire `DockerCheckRunner` into `app.Open()` as the default; keep `FallbackRunner` for tests/CI environments without Docker.
4. Add explicit unit tests for `DockerCheckRunner` against a mock `Exec` dispatcher (happy path, compile fail, test fail, lint fail, exec error, timeout, cache miss).
5. Add a merge-queue regression test in `internal/engine/` that wires the real `validation.Service` + `DockerCheckRunner` against a **scripted `Exec` mock** simulating a failing `go build`, and asserts that the merge queue does **not** commit and instead requeues the task with failure feedback. This closes the Architecture §23.3 safety check.
6. Update `docs/approval-pipeline.md`, `docs/getting-started.md`, and `docs/operations-diagnostics.md` to remove the "fail-closed fallback" caveats and describe the real runner.
7. Commit each step separately per the TDD / frequent-commits discipline.

## 4. File Structure

| File | Role | Change type |
|---|---|---|
| `internal/engine/interfaces.go` | Engine service interfaces | **Modify** — add `ExecResult` type and `Exec` method on `ContainerService` |
| `internal/container/docker.go` | Docker CLI wrapper | **Modify** — implement `DockerService.Exec` via `docker exec` |
| `internal/container/docker_test.go` | Docker wrapper tests | **Modify** — cover the new `Exec` method |
| `internal/validation/runner_docker.go` | New concrete check runner | **Create** — `DockerCheckRunner` type using `engine.ContainerService.Exec` |
| `internal/validation/runner_docker_test.go` | Unit tests for the runner | **Create** — profile iteration, pass/fail mapping, cache miss, fail-closed |
| `internal/validation/fallback_runner.go` | Existing fallback | **Keep** — still used by tests and non-Docker environments |
| `internal/validation/validation.go` | Service + profiles | **Modify only if needed** — no interface change expected |
| `internal/engine/executor_test.go` | Scripted container service used by existing tests | **Modify** — add an `Exec` implementation to `scriptedContainerService` and any other fakes so they still satisfy the expanded interface |
| `internal/engine/mergequeue_integration_test.go` | New regression test | **Create** — end-to-end merge queue + real `validation.Service` + scripted `Exec` returning failing `go build`; asserts no commit |
| `internal/app/app.go` | Composition root | **Modify** — wire `DockerCheckRunner` and fall back to `FallbackRunner` only when `cfg.Docker.Image == ""` or an explicit `AXIOM_VALIDATION_DISABLED=1` escape hatch is set |
| `docs/approval-pipeline.md` | Reference docs | **Modify** — remove fallback caveat in stages 2 and 5; document the runner |
| `docs/getting-started.md` | User-facing workflow | **Modify** — note that merges require Docker + the meeseeks image |
| `docs/operations-diagnostics.md` | Ops guide | **Modify** — troubleshooting for `docker exec` failures, cache miss |

Each new file stays focused on one responsibility. `runner_docker.go` owns docker-exec orchestration. The regression test file is separate from the existing executor tests so it can use a different fake topology without polluting their state.

---

## 5. Task Breakdown

### Task 1: Add `Exec` primitive to `engine.ContainerService`

**Files:**
- Modify: `internal/engine/interfaces.go:25-43`

- [ ] **Step 1: Write the failing compile-time check**

  Add a throwaway assertion in a new file `internal/engine/interfaces_exec_test.go` to confirm the interface is extended:

  ```go
  package engine

  import (
      "context"
      "testing"
  )

  type containerServiceWithExec struct{}

  func (containerServiceWithExec) Start(context.Context, ContainerSpec) (string, error)   { return "", nil }
  func (containerServiceWithExec) Stop(context.Context, string) error                     { return nil }
  func (containerServiceWithExec) ListRunning(context.Context) ([]string, error)          { return nil, nil }
  func (containerServiceWithExec) Cleanup(context.Context) error                          { return nil }
  func (containerServiceWithExec) Exec(context.Context, string, []string) (ExecResult, error) {
      return ExecResult{}, nil
  }

  func TestContainerServiceInterfaceIncludesExec(t *testing.T) {
      var _ ContainerService = containerServiceWithExec{}
  }
  ```

- [ ] **Step 2: Run the test to verify it fails**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestContainerServiceInterfaceIncludesExec -count=1`
  Expected: compile failure — `ExecResult` not defined / `Exec` missing from `ContainerService`.

- [ ] **Step 3: Add the `ExecResult` type and `Exec` method**

  Edit `internal/engine/interfaces.go`:

  ```go
  // ExecResult holds the outcome of running a command inside a container via docker exec.
  type ExecResult struct {
      ExitCode int
      Stdout   string
      Stderr   string
      Duration time.Duration
  }

  // ContainerService abstracts Docker container lifecycle for testability.
  type ContainerService interface {
      Start(ctx context.Context, spec ContainerSpec) (string, error)
      Stop(ctx context.Context, id string) error
      ListRunning(ctx context.Context) ([]string, error)
      Cleanup(ctx context.Context) error
      // Exec runs a command inside a running container started via Start.
      // Returns the exit code plus captured stdout/stderr. A non-zero exit code
      // is NOT an error — it is a normal result. err is non-nil only on
      // infrastructure failures (container not found, docker daemon down).
      Exec(ctx context.Context, containerID string, cmd []string) (ExecResult, error)
  }
  ```

  Add the `time` import if it is not already present.

- [ ] **Step 4: Run the test to verify it passes**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestContainerServiceInterfaceIncludesExec -count=1`
  Expected: `ok` — interface assertion compiles and passes.

- [ ] **Step 5: Fix every existing implementation of `ContainerService`**

  Expect the rest of `./...` to fail to compile because existing mocks and the real `DockerService` no longer satisfy the interface. Search for them:

  Run: `cd C:/Users/ethan/Projects/axiom_new && go build ./...`
  Expected: a list of compile errors in files that embed or implement `ContainerService`. Known offenders:
  - `internal/container/docker.go` (real implementation)
  - `internal/engine/executor_test.go` (`scriptedContainerService`)
  - `internal/validation/validation_test.go` (`mockContainerService`)
  - `internal/container/docker_test.go` (`mockExecutor` is separate; the `DockerService` test suite will need coverage)

  For each, add a minimal stub `Exec` that returns `ExecResult{}, nil`. Real behavior for `DockerService` lands in Task 2; tests land in Task 4.

- [ ] **Step 6: Run the full build**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go build ./...`
  Expected: exit code 0, no errors.

- [ ] **Step 7: Commit**

  ```bash
  git add internal/engine/interfaces.go internal/engine/interfaces_exec_test.go \
          internal/container/docker.go internal/engine/executor_test.go \
          internal/validation/validation_test.go
  git commit -m "feat(engine): add Exec primitive to ContainerService interface"
  ```

---

### Task 2: Implement `DockerService.Exec`

**Files:**
- Modify: `internal/container/docker.go`
- Modify: `internal/container/docker_test.go`

- [ ] **Step 1: Write the failing test**

  Append to `internal/container/docker_test.go` (use an existing `mockExecutor` pattern):

  ```go
  func TestDockerService_Exec_CapturesStdoutAndExitCode(t *testing.T) {
      exec := newMockExecutor()
      // Scripted docker exec response: exit code 0 with "hello" on stdout.
      exec.responses = append(exec.responses, mockResponse{
          stdout: "hello\n",
          err:    nil,
      })
      svc, _ := testService(t, exec)

      result, err := svc.Exec(context.Background(), "axiom-test-123", []string{"go", "version"})
      if err != nil {
          t.Fatalf("Exec: %v", err)
      }
      if result.ExitCode != 0 {
          t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
      }
      if !strings.Contains(result.Stdout, "hello") {
          t.Fatalf("Stdout = %q, want to contain hello", result.Stdout)
      }

      if len(exec.calls) != 1 {
          t.Fatalf("docker invocations = %d, want 1", len(exec.calls))
      }
      got := exec.calls[0]
      if got[0] != "exec" || got[1] != "axiom-test-123" {
          t.Fatalf("docker args = %v, want [exec axiom-test-123 ...]", got)
      }
  }

  func TestDockerService_Exec_NonZeroExitIsResultNotError(t *testing.T) {
      exec := newMockExecutor()
      exec.responses = append(exec.responses, mockResponse{
          stdout: "FAIL compile\n",
          // Simulate docker exec exiting with a non-zero code by returning
          // a *exec.ExitError — see CLIExecutor.Run error wrapping below.
          err: &fakeExitError{code: 2, stderr: "compile error line 42"},
      })
      svc, _ := testService(t, exec)

      result, err := svc.Exec(context.Background(), "axiom-test-123", []string{"go", "build", "./..."})
      if err != nil {
          t.Fatalf("Exec returned error for non-zero exit; want result: %v", err)
      }
      if result.ExitCode != 2 {
          t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
      }
      if !strings.Contains(result.Stderr, "compile error") {
          t.Fatalf("Stderr = %q, want compile error", result.Stderr)
      }
  }
  ```

  `fakeExitError` is a new test helper struct that implements `ExitCode() int` and `error` — define it at the bottom of the test file.

  Adjust the existing `mockExecutor` if needed so it can return per-call responses (current implementation may use a single pre-canned response).

- [ ] **Step 2: Run the tests to verify they fail**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/container/ -run TestDockerService_Exec -count=1 -timeout 60s`
  Expected: FAIL — `DockerService.Exec` is not defined.

- [ ] **Step 3: Implement `DockerService.Exec` and update `CLIExecutor`**

  Add a sibling method to `internal/container/executor.go` that also exposes exit codes:

  ```go
  // RunWithExit behaves like Run but returns the exit code and stdout/stderr
  // separately. A non-zero exit code is returned without an error.
  func (CLIExecutor) RunWithExit(ctx context.Context, args ...string) (stdout, stderr string, exitCode int, err error) {
      cmd := exec.CommandContext(ctx, "docker", args...)
      var outBuf, errBuf bytes.Buffer
      cmd.Stdout = &outBuf
      cmd.Stderr = &errBuf
      runErr := cmd.Run()
      if runErr != nil {
          var ee *exec.ExitError
          if errors.As(runErr, &ee) {
              return outBuf.String(), errBuf.String(), ee.ExitCode(), nil
          }
          return outBuf.String(), errBuf.String(), -1, runErr
      }
      return outBuf.String(), errBuf.String(), 0, nil
  }
  ```

  Update the `CommandExecutor` interface in `docker.go` to include `RunWithExit`. In `docker.go`, implement `Exec` on `DockerService`:

  ```go
  func (d *DockerService) Exec(ctx context.Context, containerID string, cmd []string) (engine.ExecResult, error) {
      if len(cmd) == 0 {
          return engine.ExecResult{}, fmt.Errorf("empty command")
      }
      args := append([]string{"exec", containerID}, cmd...)
      start := time.Now()
      stdout, stderr, code, err := d.exec.RunWithExit(ctx, args...)
      duration := time.Since(start)
      if err != nil {
          return engine.ExecResult{}, fmt.Errorf("docker exec %s: %w", containerID, err)
      }
      return engine.ExecResult{
          ExitCode: code,
          Stdout:   stdout,
          Stderr:   stderr,
          Duration: duration,
      }, nil
  }
  ```

- [ ] **Step 4: Run the tests to verify they pass**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/container/ -run TestDockerService_Exec -count=1 -timeout 60s`
  Expected: PASS.

- [ ] **Step 5: Run the whole `internal/container` suite to catch regressions**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/container/ -count=1 -timeout 120s`
  Expected: PASS.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/container/docker.go internal/container/executor.go internal/container/docker_test.go
  git commit -m "feat(container): implement DockerService.Exec via docker exec"
  ```

---

### Task 3: Create `DockerCheckRunner`

**Files:**
- Create: `internal/validation/runner_docker.go`
- Create: `internal/validation/runner_docker_test.go`

- [ ] **Step 1: Write the failing tests first**

  Create `internal/validation/runner_docker_test.go` with the behaviors we need:

  ```go
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

  type scriptedExec struct {
      calls    [][]string
      results  map[string]engine.ExecResult // keyed by joined command
      err      error
  }

  func (s *scriptedExec) Start(context.Context, engine.ContainerSpec) (string, error) {
      return "sandbox-1", nil
  }
  func (s *scriptedExec) Stop(context.Context, string) error               { return nil }
  func (s *scriptedExec) ListRunning(context.Context) ([]string, error)    { return nil, nil }
  func (s *scriptedExec) Cleanup(context.Context) error                    { return nil }

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
      // Check commands fired in order
      wantFirst := "sh -c go build ./..."
      if len(exec.calls) < 1 || strings.Join(exec.calls[0], " ") != wantFirst {
          t.Fatalf("first exec = %v, want %q", exec.calls[0], wantFirst)
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
      failed := 0
      for _, r := range results {
          if r.Status == state.ValidationFail {
              failed++
              if r.CheckType != state.CheckCompile {
                  continue
              }
              if !strings.Contains(r.Output, "expected 'package'") {
                  t.Errorf("compile failure output missing stderr: %q", r.Output)
              }
          }
      }
      if failed == 0 {
          t.Fatal("expected at least one failing result")
      }
  }

  func TestDockerCheckRunner_ExecInfraErrorFailsClosed(t *testing.T) {
      exec := &scriptedExec{err: errors.New("docker daemon down")}
      runner := NewDockerCheckRunner(exec, testLogger())

      results := runner.Run(context.Background(), "sandbox-1", []string{"go"}, false)

      if AllPassed(results) {
          t.Fatal("expected fail-closed result when docker exec errors")
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
  ```

- [ ] **Step 2: Run the tests to verify they fail**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/validation/ -run TestDockerCheckRunner -count=1 -timeout 60s`
  Expected: compile failure — `NewDockerCheckRunner` not defined.

- [ ] **Step 3: Implement `DockerCheckRunner`**

  Create `internal/validation/runner_docker.go`:

  ```go
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
  // Per Architecture Section 13.5, the order is: compile → lint → test → security.
  // On any infrastructure error (docker daemon down, container missing), Run
  // emits a failing CheckResult rather than a silent pass — the merge queue
  // must never commit on infra failures.
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
      // relative paths) run in a shell inside the sandbox.
      cmd := []string{"sh", "-c", commandLine}
      start := time.Now()
      execResult, err := r.containers.Exec(ctx, containerID, cmd)
      duration := time.Since(start).Milliseconds()
      if err != nil {
          r.log.Warn("docker exec failed during validation", "check", checkType, "error", err)
          return CheckResult{
              CheckType:  checkType,
              Status:     state.ValidationFail,
              Output:     fmt.Sprintf("infrastructure error running %s: %v", checkType, err),
              DurationMs: duration,
          }
      }

      // Dependency cache miss detection per Architecture Section 13.5.
      combined := execResult.Stderr + "\n" + execResult.Stdout
      if strings.Contains(strings.ToLower(combined), "dependency_cache_miss") {
          return CheckResult{
              CheckType:  checkType,
              Status:     state.ValidationFail,
              Output:     "dependency_cache_miss: " + strings.TrimSpace(combined),
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
          Output:     strings.TrimSpace(combined),
          DurationMs: duration,
      }
  }
  ```

- [ ] **Step 4: Run the tests to verify they pass**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/validation/ -run TestDockerCheckRunner -count=1 -timeout 60s`
  Expected: PASS (all 5 tests).

- [ ] **Step 5: Run the whole validation suite**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/validation/ -count=1 -timeout 120s`
  Expected: PASS — existing tests should not break because the new type is additive.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/validation/runner_docker.go internal/validation/runner_docker_test.go
  git commit -m "feat(validation): add DockerCheckRunner that runs profile commands via docker exec"
  ```

---

### Task 4: Wire `DockerCheckRunner` into the composition root

**Files:**
- Modify: `internal/app/app.go:86-90`

- [ ] **Step 1: Write the failing wiring test**

  Create `internal/app/app_wiring_test.go`:

  ```go
  package app

  import (
      "reflect"
      "testing"

      "github.com/openaxiom/axiom/internal/validation"
  )

  func TestApp_DefaultRunnerIsDockerCheckRunner(t *testing.T) {
      // Reflectively verify that the default composition root wires the real
      // runner, not the fallback. Protects against regressions where somebody
      // swaps FallbackRunner back in.
      wantType := reflect.TypeOf(&validation.DockerCheckRunner{})
      gotType := defaultValidationRunnerType()
      if gotType != wantType {
          t.Fatalf("default validation runner type = %v, want %v", gotType, wantType)
      }
  }
  ```

  Add a small exported-for-test helper in `internal/app/app.go` (or a `app_internal_test.go` file in the same package) named `defaultValidationRunnerType()` that returns `reflect.TypeOf(...)` of the concrete runner chosen inside `Open()`. Refactor `Open()` so the runner selection lives in a private function `buildValidationRunner(cfg, containerSvc, log)` that both `Open()` and the test helper call.

- [ ] **Step 2: Run the test to verify it fails**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/app/ -run TestApp_DefaultRunnerIsDockerCheckRunner -count=1 -timeout 60s`
  Expected: FAIL — either `defaultValidationRunnerType` is undefined, or it returns the `FallbackRunner` type.

- [ ] **Step 3: Update `app.Open()` to wire the real runner**

  Edit `internal/app/app.go`:

  ```go
  func buildValidationRunner(cfg *config.Config, containerSvc engine.ContainerService, log *slog.Logger) validation.CheckRunner {
      // Allow tests and docker-less environments to explicitly opt out. Any
      // production wiring without the opt-out gets the real runner.
      if os.Getenv("AXIOM_VALIDATION_DISABLED") == "1" {
          log.Warn("validation runner disabled via AXIOM_VALIDATION_DISABLED; merges will fail closed")
          return validation.FallbackRunner{}
      }
      if cfg == nil || cfg.Docker.Image == "" {
          log.Warn("no validation image configured; using fail-closed fallback runner")
          return validation.FallbackRunner{}
      }
      return validation.NewDockerCheckRunner(containerSvc, log)
  }

  func defaultValidationRunnerType() reflect.Type {
      // Build a throwaway runner using a minimal config so the test can assert
      // the production code path without requiring a real Docker daemon.
      cfg := &config.Config{Docker: config.DockerConfig{Image: "axiom-meeseeks-multi:latest"}}
      return reflect.TypeOf(buildValidationRunner(cfg, nil, slog.Default()))
  }
  ```

  Replace the existing `validation.NewService(...)` call in `Open()` to use `buildValidationRunner(cfg, containerSvc, log)`.

- [ ] **Step 4: Run the test to verify it passes**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/app/ -run TestApp_DefaultRunnerIsDockerCheckRunner -count=1 -timeout 60s`
  Expected: PASS.

- [ ] **Step 5: Run the full app package tests**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/app/ -count=1 -timeout 120s`
  Expected: PASS.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/app/app.go internal/app/app_wiring_test.go
  git commit -m "feat(app): wire DockerCheckRunner as the default validation runner"
  ```

---

### Task 5: End-to-end regression test — broken build MUST NOT commit

**Files:**
- Create: `internal/engine/mergequeue_integration_test.go`

This is the single test that proves Architecture §23.3 is enforced — it is the acceptance gate for the issue.

- [ ] **Step 1: Write the failing regression test**

  Create `internal/engine/mergequeue_integration_test.go`:

  ```go
  package engine

  import (
      "context"
      "os"
      "path/filepath"
      "strings"
      "testing"

      "github.com/openaxiom/axiom/internal/state"
      "github.com/openaxiom/axiom/internal/validation"
  )

  // TestMergeQueue_RealValidator_BlocksBrokenGoBuild is the acceptance test
  // for Issue 04: if the validation sandbox's `go build ./...` returns a
  // non-zero exit code, the merge queue MUST NOT commit the staged output.
  //
  // It exercises the full chain:
  //   engine merge queue adapter -> validation.Service (real) -> DockerCheckRunner
  //   -> scripted Exec returning a failing `go build`.
  //
  // The scripted container service asserts that no `git commit` is performed
  // and that the task is requeued with failure feedback.
  func TestMergeQueue_RealValidator_BlocksBrokenGoBuild(t *testing.T) {
      engineUnderTest, scripted, gitSvc := newRealValidatorEngine(t, map[string]scriptedExecResult{
          "sh -c go build ./...": {exitCode: 2, stderr: "pkg/foo.go:1:1: expected 'package', found 'bad'"},
      })
      taskRecord, attemptRecord := seedDispatchedAttempt(t, engineUnderTest, "run-broken", "task-broken", state.TaskInProgress)

      // Seed a go.mod so detectValidationLanguages returns ["go"].
      if err := os.WriteFile(filepath.Join(engineUnderTest.rootDir, "go.mod"), []byte("module broken\n"), 0o644); err != nil {
          t.Fatalf("seed go.mod: %v", err)
      }

      engineUnderTest.executeAttempt(context.Background(), *taskRecord, *attemptRecord)

      // Stage 2 must fail *before* the merge queue gets a turn.
      if engineUnderTest.MergeQueueLen() != 0 {
          t.Fatalf("merge queue length = %d; broken build should not reach the queue", engineUnderTest.MergeQueueLen())
      }

      // No commits whatsoever.
      if len(gitSvc.commits) != 0 {
          t.Fatalf("commits = %d; want 0 because go build failed", len(gitSvc.commits))
      }

      // Task must be requeued.
      taskAfter, err := engineUnderTest.db.GetTask(taskRecord.ID)
      if err != nil {
          t.Fatalf("GetTask: %v", err)
      }
      if taskAfter.Status != state.TaskQueued {
          t.Fatalf("task status = %q, want %q", taskAfter.Status, state.TaskQueued)
      }

      attemptAfter, err := engineUnderTest.db.GetAttempt(attemptRecord.ID)
      if err != nil {
          t.Fatalf("GetAttempt: %v", err)
      }
      if attemptAfter.Status != state.AttemptFailed {
          t.Fatalf("attempt status = %q, want %q", attemptAfter.Status, state.AttemptFailed)
      }
      if attemptAfter.Feedback == nil || !strings.Contains(*attemptAfter.Feedback, "expected 'package'") {
          t.Fatalf("attempt feedback = %v, want go compile error", attemptAfter.Feedback)
      }

      // Confirm the scripted runner was actually invoked — this guards against
      // regressions where the adapter silently bypasses the real runner.
      if scripted.execCalls == 0 {
          t.Fatal("expected the real validator to call docker exec at least once")
      }
  }
  ```

  The test needs two helpers:

  - `newRealValidatorEngine(t, responses)` — similar to `newExecutorEngine` in `executor_test.go`, but instead of `mockValidationService` it wires the real `validation.Service` with `validation.NewEngineAdapter` and a `DockerCheckRunner` backed by a scripted container service that supports both Meeseeks start + `Exec`.
  - `scriptedExecResult { exitCode int; stdout string; stderr string }` — shared struct for the map of expected commands.

  Place both helpers in the same file for locality.

- [ ] **Step 2: Run the test to verify it fails**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestMergeQueue_RealValidator_BlocksBrokenGoBuild -count=1 -timeout 120s`
  Expected: FAIL (missing helpers / wiring).

- [ ] **Step 3: Implement the helpers**

  - Extend `scriptedContainerService` in `executor_test.go` (or inline a new type in the new file) to expose a scriptable `Exec` method that looks up commands in a response map and counts calls.
  - Write `newRealValidatorEngine(t, responses)` that:
    1. Reuses `testDB(t)`, `testConfig()`, `trackingGitService{}`, and `mockTaskService`.
    2. Creates a scripted container service that produces a valid manifest for stage 2 (reusing `scriptedContainerService`'s goroutine pattern).
    3. Constructs `validation.NewService(...)` with `validation.NewDockerCheckRunner(scripted, testLogger())` as the runner.
    4. Wraps it in `validation.NewEngineAdapter(...)` and passes it as the `Validation` option to `engine.New`.

- [ ] **Step 4: Run the test to verify it passes**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -run TestMergeQueue_RealValidator_BlocksBrokenGoBuild -count=1 -timeout 120s`
  Expected: PASS.

- [ ] **Step 5: Run the whole engine package to make sure existing tests still pass**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/engine/ -count=1 -timeout 300s`
  Expected: PASS.

  Note for the executor: while investigating this plan, `TestExecuteAttempt_SuccessEnqueuesAndMerges` was observed hanging in `monitorTaskIPC` on Windows in at least one run. If it reproduces here, stop and open a separate bug — do **not** paper over it as part of this issue. The hang is a flaky-test / IPC-dir mismatch concern in the Issue-03 executor, not part of the merge-queue integration-check scope.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/engine/mergequeue_integration_test.go internal/engine/executor_test.go
  git commit -m "test(engine): add regression test ensuring broken go build blocks the merge queue"
  ```

---

### Task 6: Handle the "same command twice" issue at stage 2 vs stage 5

**Files:**
- Modify: `internal/engine/mergequeue.go:90-116` (engine adapter)
- Modify: `internal/validation/runner_docker.go` (read workspace dir from environment env var)

Stage 2 runs with `StagingDir` set and `ProjectDir:/workspace/project:ro` + `StagingDir:/workspace/staging:rw`. Stage 5 runs with `StagingDir == ""` because the merge queue has already applied the files into the live project directory, so only `/workspace/project` is mounted. The `DockerCheckRunner` simply `sh -c`'s into the container, so the working directory matters.

- [ ] **Step 1: Write the failing test**

  Add to `internal/validation/runner_docker_test.go`:

  ```go
  func TestDockerCheckRunner_UsesWorkspaceProjectAsCwd(t *testing.T) {
      exec := &scriptedExec{results: map[string]engine.ExecResult{}}
      runner := NewDockerCheckRunner(exec, testLogger())

      _ = runner.Run(context.Background(), "sandbox-1", []string{"go"}, false)

      if len(exec.calls) == 0 {
          t.Fatal("expected at least one exec call")
      }
      // sh -c must cd into /workspace/project (read-only mount) before running
      // the profile command — otherwise go build runs against an empty /.
      cmd := strings.Join(exec.calls[0], " ")
      if !strings.Contains(cmd, "cd /workspace/project") {
          t.Fatalf("first exec = %q, want it to cd into /workspace/project", cmd)
      }
  }
  ```

- [ ] **Step 2: Run the test to verify it fails**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/validation/ -run TestDockerCheckRunner_UsesWorkspaceProjectAsCwd -count=1`
  Expected: FAIL — current implementation runs `sh -c go build ./...` without `cd`.

- [ ] **Step 3: Prefix commands with `cd /workspace/project &&`**

  Update `DockerCheckRunner.runOne`:

  ```go
  cmd := []string{"sh", "-c", "cd /workspace/project && " + commandLine}
  ```

  Update `TestDockerCheckRunner_GoProfile_AllPass` and any other existing test whose assertion embeds the command string — they should now expect the `cd` prefix.

- [ ] **Step 4: Run the validation tests**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./internal/validation/ -count=1 -timeout 120s`
  Expected: PASS.

- [ ] **Step 5: Commit**

  ```bash
  git add internal/validation/runner_docker.go internal/validation/runner_docker_test.go
  git commit -m "fix(validation): run profile commands with cd /workspace/project"
  ```

---

### Task 7: Update docs

**Files:**
- Modify: `docs/approval-pipeline.md` (§ Stage 2, § Stage 5)
- Modify: `docs/getting-started.md` (Docker prerequisite section)
- Modify: `docs/operations-diagnostics.md` (new troubleshooting entries)

- [ ] **Step 1: Remove fallback caveats from `approval-pipeline.md`**

  Replace the Stage 2 note *"The default app composition currently uses a fail-closed fallback runner until a concrete in-container check runner is configured."* with:

  > The default app composition wires `validation.DockerCheckRunner`, which runs the language-specific profile commands (`go build ./...`, `golangci-lint run ./...`, `go test ./...`, and the Node/Python/Rust equivalents) inside the sandbox container via `docker exec`. A fail-closed `FallbackRunner` is still used when Docker is unavailable (e.g. test environments without a daemon, or when `AXIOM_VALIDATION_DISABLED=1` is set explicitly).

  Remove the analogous Stage 5 note.

- [ ] **Step 2: Add a prerequisites bullet in `getting-started.md`**

  Add to the "Before you start" section:

  > Axiom requires a working local Docker installation. Merges are blocked until the validation sandbox can run `docker exec` successfully — this is how the engine guarantees every commit has passed a real build, test, and lint.

- [ ] **Step 3: Add a troubleshooting entry in `operations-diagnostics.md`**

  New section "Validation runner failures":
  - `infrastructure error running compile: docker exec ...` → Docker daemon unreachable; run `docker ps`.
  - `dependency_cache_miss: ...` → Prepared dependency cache absent for current lockfile; re-run `axiom preflight`.
  - All tasks stuck in `failed` with `validation runner is not configured` → You are running with `AXIOM_VALIDATION_DISABLED=1` or no `docker.image` configured. Remove the env var or set the image in `.axiom/config.toml`.

- [ ] **Step 4: Commit**

  ```bash
  git add docs/approval-pipeline.md docs/getting-started.md docs/operations-diagnostics.md
  git commit -m "docs: describe real validation runner and merge-queue integration checks"
  ```

---

### Task 8: Final verification

- [ ] **Step 1: Run the full test suite**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go test ./... -count=1 -timeout 10m`
  Expected: all packages PASS. If `TestExecuteAttempt_SuccessEnqueuesAndMerges` hangs, see the note in Task 5 Step 5 — escalate separately rather than patching it under this issue.

- [ ] **Step 2: Build the CLI binary**

  Run: `cd C:/Users/ethan/Projects/axiom_new && go build ./cmd/axiom`
  Expected: exit code 0, `axiom.exe` produced.

- [ ] **Step 3: Write the fix report**

  Create `issues/04/04-fix-report.md` modeled on `issues/03/03-fix-report.md`:
  - Summary (what changed, why it closes Issue 04)
  - What Changed (bullet list with file references)
  - Validation (commands run, tests added)
  - Known limitations (requires Docker; `DockerCheckRunner` does not implement warm-pool reuse — that's a Phase 13+ concern per §13.8)

- [ ] **Step 4: Commit the report**

  ```bash
  git add issues/04/04-fix-report.md
  git commit -m "docs(issues): fix report for Issue 04"
  ```

---

## 6. Notes & Design Decisions

- **Why `cd /workspace/project` instead of `WorkingDir`.** The `engine.ContainerSpec` has no `WorkingDir` field and the sandbox container's default cwd is whatever the Meeseeks base image sets. A `sh -c "cd … && …"` wrapper avoids touching the ContainerSpec struct (which is referenced widely) and keeps the runner's contract self-contained.
- **Fail-closed default.** `DockerCheckRunner` emits `ValidationFail` on every infra error. This is the correct behavior given the safety story for non-technical users — the wrong failure mode here is a silent pass, not an over-eager requeue.
- **Why keep `FallbackRunner`.** Tests and developers without Docker still need a path that does not require standing up a meeseeks image. Keeping the type around, gated behind `AXIOM_VALIDATION_DISABLED=1` or missing `docker.image`, preserves that path while making the production default strict.
- **Dependency cache miss.** Architecture §13.5 mandates the `dependency_cache_miss` result shape. We detect it by substring in combined stdout/stderr; the Meeseeks image is responsible for emitting that token when the validation container cannot reach its prepared cache. The runner does not itself know how caches are prepared.
- **Out of scope.** Warm sandbox pools (§13.8), integration sandbox opt-in (§13.6), and any dependency-cache preparation tooling are deferred. This issue closes *only* the "no real integration checks at merge time" hole.
- **Flaky test observation (not fixed here).** During investigation, `go test ./internal/engine/ -run TestExecuteAttempt_SuccessEnqueuesAndMerges -count=1 -timeout 60s` hung inside `monitorTaskIPC` on Windows. It appears to be a pre-existing Issue-03 concern (scripted container service writes IPC messages to a path that may not match `req.Dirs.Output` on Windows). Do not try to fix this under Issue 04 — if it reproduces, open a new issue and surface it so the team can investigate the IPC-dir mismatch in isolation.

## 7. Spec Coverage Self-Check

| `issues.md` §4 requirement | Task(s) that cover it |
|---|---|
| "Replace the stub adapter with a real validation service that runs build/test/lint in the validation sandbox" | Tasks 1, 2, 3, 4 |
| "Fail closed on validation runner errors instead of auto-passing" | Task 3 (`TestDockerCheckRunner_ExecInfraErrorFailsClosed`) and Task 4 (`buildValidationRunner` default path) |
| "Add a regression test where intentionally broken staged output must not commit" | Task 5 (`TestMergeQueue_RealValidator_BlocksBrokenGoBuild`) |
| Architecture §23.3 "Full build / test suite / linting" | Task 3 (profile iteration) + Task 6 (cwd fix) |
| Docs affected: `docs/approval-pipeline.md`, `docs/getting-started.md` | Task 7 |

No gaps.
