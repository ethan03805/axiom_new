# Issue 04 Fix Report

## Summary

Issue 04 was resolved by building the missing link between the merge queue and the validation sandbox. The engine now has a concrete `DockerCheckRunner` that executes language-specific build/test/lint commands inside the sandbox container via `docker exec`, and the composition root wires it as the default validation runner. The merge-queue adapter that was introduced during Issue 03 is now backed by a runner that actually runs commands — broken code cannot pass the integration checks at stage 2 or stage 5, closing the safety promise of Architecture Sections 13.3, 13.5, and 23.3.

A dedicated engine-level regression test drives the real `validation.Service` + `DockerCheckRunner` against a scripted `docker exec` returning a failing `go build` and asserts that the merge queue refuses to commit. A complementary test proves the clean-build path still works, and a third test proves the runner fails closed on docker infra errors.

## What Changed

### Engine interface

- Extended `engine.ContainerService` (`internal/engine/interfaces.go:37-58`) with a new `ExecResult` type and `Exec(ctx, containerID, cmd) (ExecResult, error)` method. A non-zero exit code is a normal result; `err` is non-nil only on infrastructure failures.
- Stubbed `Exec` on every existing `ContainerService` implementer so the broader codebase kept compiling while new behavior was added:
  - `*container.DockerService` (`internal/container/docker.go`) — real implementation (see next bullet).
  - Test doubles in `internal/engine/engine_test.go`, `internal/engine/executor_test.go`, `internal/engine/recovery_phase19_test.go`, `internal/validation/validation_test.go`, `internal/review/review_test.go`.
- Added `internal/engine/interfaces_exec_test.go` as a compile-time interface assertion.

### Docker CLI wrapper

- Added `RunWithExit` to `container.CommandExecutor` and its concrete `CLIExecutor` (`internal/container/executor.go`) so callers can capture stdout, stderr, and the raw exit code separately without a non-zero exit being reported as an error.
- Implemented `DockerService.Exec` (`internal/container/docker.go`) on top of `RunWithExit` with empty-command validation, duration tracking, and stdout/stderr capture.
- Added unit tests for `DockerService.Exec`: stdout/exit-code capture, non-zero-exit-as-result, infra error propagation, and empty-command rejection (`internal/container/docker_test.go`).

### Docker check runner

- Added `internal/validation/runner_docker.go` with `DockerCheckRunner`. It iterates the existing `GetProfile(lang)` table for every detected language and runs compile → lint → test (→ security if `securityScan` is set) by dispatching `sh -c "cd /workspace/project && <profile command>"` through `engine.ContainerService.Exec`. It maps exit codes to `state.ValidationPass`/`Fail`, detects the `dependency_cache_miss` sentinel per Architecture Section 13.5, and fails closed (`ValidationFail` plus an `infrastructure error running <check>` output) on any docker infra error.
- Added `internal/validation/runner_docker_test.go` with coverage for:
  - Go profile all-pass, compile failure with surfaced stderr, infra-error fail-closed, security scan gating, unknown-language skip, empty-language skip, dependency cache miss detection, and the `cd /workspace/project` cwd requirement.

### Composition root wiring

- Refactored `internal/app/app.go` so runner selection lives in `buildValidationRunner(cfg, containerSvc, log)`. It returns `validation.NewDockerCheckRunner(...)` by default and falls back to `validation.FallbackRunner{}` only when `AXIOM_VALIDATION_DISABLED=1` or `cfg.Docker.Image` is empty.
- Added `defaultValidationRunnerType()` (test-only) and `internal/app/app_wiring_test.go` asserting:
  - Default production wiring resolves to `*validation.DockerCheckRunner`.
  - The escape hatch returns `validation.FallbackRunner{}`.
  - A missing `docker.image` returns `validation.FallbackRunner{}`.

### Regression test — broken build must not commit

- Added `internal/engine/export_test.go` exposing `RunMergeQueueIntegrationChecksForTest(ctx, validation, cfg, projectDir)` which drives the internal `mergeQueueValidatorAdapter` directly. The helper exists only in test builds so the integration test can live in `package engine_test` and import `internal/validation` without an import cycle.
- Added `internal/engine/mergequeue_integration_test.go` (`package engine_test`) with three acceptance tests:
  - `TestMergeQueue_RealValidator_BlocksBrokenGoBuild` — wires a real `validation.Service` + `DockerCheckRunner` behind the merge-queue adapter with a scripted `docker exec` that returns a failing `go build`, and asserts `RunIntegrationChecks` returns `(false, feedback, nil)` with the simulated compile error in the feedback and the scripted Exec actually invoked.
  - `TestMergeQueue_RealValidator_PassesCleanGoBuild` — the clean-build counterpart, proving the runner does not accidentally fail closed on a healthy project.
  - `TestMergeQueue_RealValidator_InfraErrorFailsClosed` — uses a container service whose `Exec` always errors and asserts the adapter reports failure with an `infrastructure error` feedback string.

### Documentation

- `docs/approval-pipeline.md` — replaced the "fail-closed fallback runner until a concrete in-container check runner is configured" note with a description of `validation.DockerCheckRunner`, the `cd /workspace/project` wrapper, and the conditions under which `FallbackRunner` is still used. Updated the Stage 5 merge-queue section to state the Section 23.3 safety contract explicitly.
- `docs/getting-started.md` — expanded the Docker prerequisite bullet to note that merges are blocked until the validation sandbox can run `docker exec` successfully.
- `docs/operations-diagnostics.md` — added a new "Validation Runner Failures" troubleshooting table covering the infra-error, `dependency_cache_miss`, `validation runner is not configured` (`FallbackRunner`), and genuine compile-error cases.

## Validation

### Commands run

```bash
# Type/package build
go build ./...
go vet ./...
go build -o axiom.exe ./cmd/axiom      # binary produced successfully

# Targeted tests for the new code
go test ./internal/engine/ -run "TestContainerServiceInterfaceIncludesExec|TestMergeQueue_RealValidator" -count=1
go test ./internal/container/ -run "TestDockerService_Exec" -count=1
go test ./internal/validation/ -run "TestDockerCheckRunner" -count=1
go test ./internal/app/ -run "TestApp_" -count=1

# Full package runs for every package touched (and their reverse dependencies)
go test ./internal/container/ ./internal/validation/ ./internal/app/ -count=1
go test ./internal/engine/ -count=1 -timeout 120s \
  -skip "TestExecuteAttempt_SuccessEnqueuesAndMerges|TestExecuteAttempt_ValidationFailureRequeuesTask|TestEngineWorkers_SchedulerExecutorMergeQueueFlow"

# Whole repository except the engine package (which has pre-existing Windows-only flakes — see below)
go test $(go list ./... | grep -v /internal/engine$) -count=1 -timeout 300s
```

All of the above commands returned `ok` for every package.

### New tests added

| Test | Purpose |
|---|---|
| `TestContainerServiceInterfaceIncludesExec` (`internal/engine/interfaces_exec_test.go`) | Compile-time assertion that `ContainerService` includes `Exec`. |
| `TestDockerService_Exec_CapturesStdoutAndExitCode` (`internal/container/docker_test.go`) | Verifies stdout capture and exit-code passthrough. |
| `TestDockerService_Exec_NonZeroExitIsResultNotError` | Non-zero exit is a normal result, not an error. |
| `TestDockerService_Exec_InfraErrorPropagates` | Infrastructure errors surface as errors, not silent passes. |
| `TestDockerService_Exec_EmptyCmdIsError` | Defensive guard against empty command dispatch. |
| `TestDockerCheckRunner_GoProfile_AllPass` | Clean-build path returns three pass results for Go. |
| `TestDockerCheckRunner_GoProfile_CompileFailBlocks` | Compile failure surfaces stderr and blocks AllPassed. |
| `TestDockerCheckRunner_ExecInfraErrorFailsClosed` | Infra error → failing CheckResult with `infrastructure error` output. |
| `TestDockerCheckRunner_SecurityScanOnlyWhenEnabled` | Security check gated by `securityScan` flag. |
| `TestDockerCheckRunner_UnknownLanguageProducesSkip` | Unknown language → `ValidationSkip` result. |
| `TestDockerCheckRunner_NoLanguagesProducesSkip` | Empty languages slice → skip. |
| `TestDockerCheckRunner_DependencyCacheMissDetected` | `dependency_cache_miss` sentinel surfaces in the result. |
| `TestDockerCheckRunner_UsesWorkspaceProjectAsCwd` | Commands are wrapped in `cd /workspace/project && ...`. |
| `TestApp_DefaultRunnerIsDockerCheckRunner` (`internal/app/app_wiring_test.go`) | Default composition root wires the real runner. |
| `TestApp_EscapeHatchFallsBackToFallbackRunner` | `AXIOM_VALIDATION_DISABLED=1` returns the fail-closed runner. |
| `TestApp_NoDockerImageFallsBackToFallbackRunner` | Missing `docker.image` returns the fail-closed runner. |
| `TestMergeQueue_RealValidator_BlocksBrokenGoBuild` (`internal/engine/mergequeue_integration_test.go`) | **Acceptance test.** End-to-end regression proving a failing `go build` blocks the merge queue. |
| `TestMergeQueue_RealValidator_PassesCleanGoBuild` | Clean build path still passes end-to-end. |
| `TestMergeQueue_RealValidator_InfraErrorFailsClosed` | Docker infra error end-to-end results in a blocked merge with infra-error feedback. |

## Known limitations

- **Requires Docker for real validation.** When `docker.image` is unset in `.axiom/config.toml` or the operator sets `AXIOM_VALIDATION_DISABLED=1`, `app.Open()` still falls back to `validation.FallbackRunner`, which fails closed. This is intentional — it preserves a path for tests and docker-less CI environments while keeping production strict. Documented in `docs/approval-pipeline.md` and `docs/operations-diagnostics.md`.
- **No warm sandbox pool.** `DockerCheckRunner` reuses the single sandbox container started by `validation.Service.RunChecks` for the life of one check run but does not implement the warm-pool reuse described in Architecture Section 13.8. That is a Phase 13+ concern and is out of scope for this issue.
- **Dependency cache preparation not implemented here.** The runner detects the `dependency_cache_miss` sentinel and fails closed, but it does not itself populate the cache. The Meeseeks base image is responsible for emitting the sentinel when its prepared cache does not cover the current lockfile — consistent with Architecture Section 13.5.
- **Integration sandbox opt-in (§13.6).** Still deferred; the merge-queue check path is validator-based only, no optional in-repo integration sandbox has been added.
- **Pre-existing Windows IPC flake in executor tests.** The plan's Task 5 Step 5 note explicitly warned that `TestExecuteAttempt_SuccessEnqueuesAndMerges` (and, as observed during this work, `TestExecuteAttempt_ValidationFailureRequeuesTask` and `TestEngineWorkers_SchedulerExecutorMergeQueueFlow`) hang on Windows inside `monitorTaskIPC` due to an IPC directory mismatch in `scriptedContainerService`. This was reproduced on the pre-Issue-04 commit `371af41` (`Issue 03 fix`), confirming it predates this work. The Issue 04 changes to `scriptedContainerService` are purely additive (a no-op `Exec` stub to satisfy the extended interface). Per the plan's explicit instruction, this flake was not patched under Issue 04 and should be triaged as a separate Issue-03-scope concern. The verification commands above use `go test -skip` to exclude those tests while still running every other engine test.

## Spec Coverage

| `issues.md` §4 requirement | Implementation |
|---|---|
| Replace the stub adapter with a real validation service that runs build/test/lint in the validation sandbox | `DockerCheckRunner` in `internal/validation/runner_docker.go` wired via `app.buildValidationRunner`. |
| Fail closed on validation runner errors instead of auto-passing | `DockerCheckRunner.runOne` infra-error branch + `TestDockerCheckRunner_ExecInfraErrorFailsClosed` + `TestMergeQueue_RealValidator_InfraErrorFailsClosed`. |
| Add a regression test where intentionally broken staged output must not commit | `TestMergeQueue_RealValidator_BlocksBrokenGoBuild` in `internal/engine/mergequeue_integration_test.go`. |
| Architecture §23.3 — full build / test suite / linting at merge time | Profile iteration in `DockerCheckRunner.Run` + the `cd /workspace/project` wrapper + `sandbox → engine → merge-queue` wiring in `app.Open`. |
| Docs affected: `docs/approval-pipeline.md`, `docs/getting-started.md` | Both updated, plus a bonus troubleshooting table in `docs/operations-diagnostics.md`. |
