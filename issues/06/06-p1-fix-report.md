# Issue 06 — Fix Report

**Issue:** `06-p1-git-safety-and-work-branch-isolation-not-enforced.md`
**Resolved:** 2026-04-08
**Branch/commit base:** `main` @ `0d41846`

## Summary

Closed the remaining gaps in the git-safety contract so that Axiom's runtime now matches the architectural promises in §23.1 (Branch Strategy), §23.4 (Project Completion), §28.2 (Git Hygiene), and §22 (State Management & Crash Recovery).

Specifically:

1. `Engine.CancelRun` now executes the full architectural cancel protocol — it stops running containers, reverts uncommitted changes, and returns the repo to the base branch.
2. `axiom cancel` now works from `draft_srs` and `awaiting_srs_approval` so users can abandon a run before the external orchestrator responds.
3. `axiom run --allow-dirty` is a documented recovery-mode escape hatch that bypasses the clean-tree check with a loud `WARN` log.
4. New binary-level acceptance tests exercise the full git lifecycle through the CLI against a real git repo (fixture-based), guarding against silent drift of the git contract.
5. Four stale "not yet wired" notes in the docs have been rewritten to describe the actual runtime flow.

The two Issue 06 recommendations that were already closed by the Issue 01 fix (`ValidateClean` and `SetupWorkBranch` being called from `StartRun`) are now also regression-tested at the binary layer, so any future drift surfaces immediately rather than silently.

## Root Causes Recap

1. **Interface shape** — `engine.GitService` exposed `SetupWorkBranch` but not `CancelCleanup`, so the engine physically could not call the cleanup path even though the primitive existed on `gitops.Manager`.
2. **Lifecycle shape** — `Engine.CancelRun` was a 5-line DB-status flip that predated the full container enumeration and git cleanup helpers; it was never revisited when those helpers landed.
3. **State machine gap** — `validRunTransitions` only allowed cancel from `active` and `paused`, blocking users from abandoning a run while waiting on the external orchestrator.
4. **Missing recovery opt-in** — `ValidateClean` was unconditional, with no CLI or API escape hatch for legitimate crash-recovery scenarios.
5. **Test-coverage gap** — the unit tests used a `noopGitService` that returned `nil` for every method, and no binary-level test ever verified the actual branch on disk.

## Files Changed

| File | Change |
|---|---|
| `internal/engine/interfaces.go` | Added `CancelCleanup` and `SetupWorkBranchAllowDirty` to the `GitService` interface |
| `internal/engine/run.go` | Rewrote `CancelRun` with the full cancel protocol (load → DB barrier → stop containers → git cleanup → event); added `AllowDirty` to `StartRunOptions`; routed `StartRun` through `SetupWorkBranchAllowDirty` when the flag is set |
| `internal/state/models.go` | Expanded `validRunTransitions` to allow `RunDraftSRS → RunCancelled` and `RunAwaitingSRSApproval → RunCancelled` |
| `internal/gitops/gitops.go` | Added the new `SetupWorkBranchAllowDirty` method (shares a private helper with `SetupWorkBranch`); replaced the stale package-comment note with a description of the wired lifecycle |
| `internal/cli/run.go` | Added `--allow-dirty` flag to `RunCmd`, threaded `AllowDirty` through `runAction` into `StartRunOptions` |
| `internal/cli/cli_test.go` | Added `CancelCleanup` and `SetupWorkBranchAllowDirty` stubs to the `noopGitService` fake |
| `internal/engine/engine_test.go` | Added `CancelCleanup` and `SetupWorkBranchAllowDirty` stubs to the `noopGitService` fake |
| `internal/engine/executor_test.go` | Added `CancelCleanup` and `SetupWorkBranchAllowDirty` stubs to the `trackingGitService` fake |
| `internal/engine/cancelrun_test.go` | **New file** — engine unit tests for the cancel protocol: `TestCancelRun_CallsCancelCleanup`, `TestCancelRun_StopsActiveContainers`, `TestCancelRun_ProceedsWhenGitCleanupFails`, `TestCancelRun_FromDraftSRS_WorksEndToEnd`, `TestCancelRun_LoadsRunForBaseBranch`, `TestCancelRun_UnknownRun` |
| `internal/engine/startrun_test.go` | Added `dirtyGitService` fake, `TestStartRun_AllowDirtyBypassesValidateClean`, `TestStartRun_RefusesDirtyTreeByDefault`; extracted a `newTestEngineWithGit` helper |
| `internal/state/runs_test.go` | Extended the valid-transitions table; added `TestUpdateRunStatus_CancelFromDraftSRS` and `TestUpdateRunStatus_CancelFromAwaitingSRSApproval` |
| `internal/cli/run_test.go` | Fixed existing `runAction` call sites for the new `allowDirty` parameter; added `TestRunCmd_AllowDirtyFlagRegistered` and `TestRunCmd_LongDescriptionDocumentsAllowDirty` |
| `cmd/axiom/phase20_integration_test.go` | Added four binary-level acceptance tests: `TestCLIRun_SwitchesToWorkBranch`, `TestCLIRun_RefusesDirtyTree`, `TestCLIRun_AllowDirtyBypass`, `TestCLICancel_CleansUpAndReturnsToBase`. Added helpers `executeCobraExpectError`, `gitCurrentBranch`, `gitBranchExists` |
| `docs/git-operations.md` | Removed four "not yet wired" notes, added a "Cancel Lifecycle" subsection describing the five-step protocol, updated the `GitService` interface listing to include the full current set |
| `docs/cli-reference.md` | Rewrote the `axiom run` section to document `--allow-dirty`, rewrote the `axiom cancel` section to describe the full cleanup protocol and pre-active cancellation support |
| `docs/getting-started.md` | Rewrote "Git Branch Strategy" with the full five-step lifecycle (start → refuse/bypass → task commits → completion → cancel) |

## Implementation Notes

### `Engine.CancelRun` protocol ordering

The new implementation deliberately orders the steps as: **load → DB flip → container stop → git cleanup → event**.

- **Loading first** captures `BaseBranch` for the cleanup call and enforces existence before any state mutation.
- **DB flip second** is the atomic barrier. The scheduler's `findReadyTasks` filters by `project_runs.status`, so flipping to `cancelled` before touching containers prevents any new task dispatch. No race window.
- **Container stop third** runs under a 30-second context deadline. Failures are logged with a warning but do not block the cancel, per §22 (safety over convenience).
- **Git cleanup fourth** is also fail-open; on failure the warning includes an explicit `git reset --hard && git checkout <base>` recovery hint naming the actual base branch.
- **Event emission last** so subscribers see a fully cancelled run (status already flipped, cleanup attempted).

### `SetupWorkBranchAllowDirty` vs. `SetupWorkBranch`

The existing `gitops.Manager.SetupWorkBranch` defensively calls `ValidateClean` internally. That is the right default — it catches programmer errors — but it conflicts with the explicit `AllowDirty` opt-in, because `StartRun` would skip its own `ValidateClean` only to have `SetupWorkBranch` re-check and fail.

Rather than mutate the existing contract (and break `TestSetupWorkBranch_DirtyRepoFails`, which documents the default behavior), I factored the branch-resolution logic into a private `setupWorkBranchBody` helper shared by two public methods:

- `SetupWorkBranch` — keeps the defensive `ValidateClean`, unchanged behavior for every existing caller and test.
- `SetupWorkBranchAllowDirty` — new method, skips the internal check, carries uncommitted state onto the work branch.

`Engine.StartRun` routes through one or the other based on `opts.AllowDirty`. The new method is also exposed on the `engine.GitService` interface so test fakes satisfy the contract.

### State machine expansion

Only two new transitions: `RunDraftSRS → RunCancelled` and `RunAwaitingSRSApproval → RunCancelled`. No safety concern — pre-active runs have no containers and no commits, so the cancel protocol degenerates to a DB-only transition with no-op container/git cleanup steps. Existing `TestUpdateRunStatus_InvalidTransition` and `TestUpdateRunStatus_TerminalStatesReject` continue to protect the invariants.

### `--allow-dirty` safety rails

The flag is guarded by three discouraging signals:

1. CLI help text explicitly says "recovery only".
2. `StartRun` logs a `WARN` level "workspace clean check bypassed via AllowDirty" every time the flag is exercised.
3. The `docs/cli-reference.md` and `docs/getting-started.md` entries describe the use case as crash-recovery-only.

## Verification

### Unit and integration suites

```
go build ./...                    # clean
go vet ./...                      # clean
go test ./... -skip <Windows-IPC-hangs>   # all 33 packages pass
```

Skipped tests are the three pre-existing Windows IPC hangs documented in Issues 04 and 05 (`TestExecuteAttempt_SuccessEnqueuesAndMerges`, `TestExecuteAttempt_ValidationFailureRequeuesTask`, `TestEngineWorkers_SchedulerExecutorMergeQueueFlow`). They reproduce on clean `main` and are unrelated to this fix.

### Targeted acceptance runs

| Command | Result |
|---|---|
| `go test ./internal/state/ -run TestUpdateRunStatus -count=1` | PASS (includes 2 new transition entries + 2 new named tests) |
| `go test ./internal/engine/ -run "TestCancelRun\|TestStartRun_AllowDirty\|TestStartRun_RefusesDirty" -count=1` | PASS (6 cancel tests + 2 allow-dirty tests) |
| `go test ./internal/engine/ -count=1 -skip <Windows-IPC-hangs>` | PASS |
| `go test ./cmd/axiom/ -count=1` | PASS (includes 4 new binary-level tests) |
| `go test ./internal/gitops/ ./internal/cli/ ./internal/state/` | PASS |

### Runtime-caller grep check

```
$ grep -rn "CancelCleanup" --include="*.go" internal/engine/
internal/engine/run.go:260:    if cleanupErr := e.git.CancelCleanup(...
```

`CancelCleanup` now has a real runtime caller in `internal/engine/run.go`, not just test hits.

### Stale-docs grep check

```
$ grep -n "not yet wired\|does not yet invoke\|not yet called\|not yet triggered\|without yet invoking" docs/git-operations.md
(no hits)
```

All four stale notes in `docs/git-operations.md` have been rewritten.

### Binary smoke test (temp git repo)

Against `axiom.exe` built from the fixed tree on 2026-04-08, in a fresh temp git repo initialised on `main` with a baseline commit:

| Step | Expected | Observed |
|---|---|---|
| **1.** `axiom init -n "Smoke Test"` + commit `.axiom/`, then `axiom run "first"` | Run starts, branch switches to `axiom/smoke-test` | `branch=axiom/smoke-test` |
| **2.** Write uncommitted `scratch.txt`, `axiom cancel` | Exit 0, branch returns to `main`, scratch file gone, `axiom/smoke-test` branch still exists | `branch=main`, `scratch exists? no`, `work branch exists? yes` |
| **3.** Write uncommitted `dirty.txt`, `axiom run "refused"` | Exit 1 with actionable error | `error: starting run: workspace not ready: working tree has uncommitted changes; commit or stash before running axiom` |
| **4.** Same dirty tree + `axiom run --allow-dirty "bypass"` | Exit 0 with WARN log about bypass | `level=WARN msg="workspace clean check bypassed via AllowDirty"`, `branch=axiom/smoke-test` |
| **5.** `axiom run "draft cancel"` followed immediately by `axiom cancel` (run still in `draft_srs`) | Both exit 0; run transitions to `cancelled` | `cancel cleanup complete base_branch=main` |

All five smoke-test scenarios match expectations.

### Acceptance criteria (from the issue)

- [x] `go build ./...` compiles cleanly
- [x] `go vet ./...` is clean
- [x] State transition tests pass including the two new entries
- [x] `TestCancelRun|TestStartRun_AllowDirty|TestGitServiceInterfaceIncludesCancelCleanup` targeted suite passes (this fix used a slightly different naming — the intent is covered by `TestCancelRun_*`, `TestStartRun_AllowDirty*`, and the interface itself is compiler-enforced rather than asserted by a dedicated test)
- [x] Full engine suite passes (excluding the three pre-existing Windows-IPC hangs)
- [x] `cmd/axiom` binary suite passes including all four new acceptance tests
- [x] `grep -rn "CancelCleanup" --include="*.go" internal/engine/` returns a runtime call site
- [x] Stale-note grep against `docs/git-operations.md` returns zero hits
- [x] All 5 binary smoke-test scenarios produce the expected outcome

## Non-Goals Respected

- No changes to any existing `gitops.Manager` method signature. `SetupWorkBranchAllowDirty` is a new method, not a modification.
- No new engine methods for granular cancel control (e.g., `CancelRunWithoutGit`) — the single `CancelRun` method executes the whole protocol atomically.
- No automatic conflict resolution on cancel cleanup — on `git clean -fd` failure the engine logs the manual-recovery hint and returns.
- No changes to `PauseRun`; pausing still preserves uncommitted state so the user can resume exactly where they left off.
- No fix for the three pre-existing Windows IPC hangs — they remain excluded via `-skip`, matching the Issues 04/05 precedent.

## How a Future Regression Would Surface

A regression in any of the wiring this fix added would now surface at the binary layer before it could ship:

- Removing `Engine.CancelRun`'s `CancelCleanup` call → `TestCLICancel_CleansUpAndReturnsToBase` fails because the scratch file would survive the cancel.
- Forgetting to refresh the branch after cancel → same test fails at the `branch = main` assertion.
- Re-adding a hard-coded `"main"` in the cleanup path → `TestCancelRun_LoadsRunForBaseBranch` fails against a run with `BaseBranch = "develop"`.
- Skipping the DB status flip → `TestCancelRun_ProceedsWhenGitCleanupFails` fails because the run status assertion runs after cleanup.
- Dropping the `--allow-dirty` flag → `TestRunCmd_AllowDirtyFlagRegistered` and `TestCLIRun_AllowDirtyBypass` both fail.
- Removing the `RunDraftSRS → RunCancelled` transition → `TestUpdateRunStatus_CancelFromDraftSRS`, `TestCancelRun_FromDraftSRS_WorksEndToEnd`, and smoke scenario 5 all fail.

This closes the gap that let Issues 01 and 06 silently drift in the first place.
