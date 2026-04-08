# Issue 06 — P1: Git safety and work-branch isolation are defined, but not enforced

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the remaining gaps in the git safety contract so that (a) `axiom cancel` actually reverts uncommitted changes on the work branch and returns the user to the base branch, (b) `axiom cancel` stops containers that belong to the cancelled run, (c) a user can cancel a run that is still sitting in `draft_srs` or `awaiting_srs_approval` (today the state machine refuses), (d) there is an explicit `--allow-dirty` escape hatch for recovery scenarios, and (e) there is an end-to-end regression test at the binary layer that asserts the work branch is actually created on disk, dirty trees are refused, and cancel cleans up. Satisfies Architecture §23.1 (Branch Strategy), §23.4 (Project Completion), §28.2 (Git Hygiene), and §22 (State Management & Crash Recovery).

**Architecture:** The `gitops.Manager` already implements every primitive this fix needs — `ValidateClean`, `SetupWorkBranch`, and `CancelCleanup`. Two of those primitives were wired into the engine by the Issue 01 fix (commit `2c5a73f`), which introduced `Engine.StartRun` and the external-orchestrator handoff. The third — `CancelCleanup` — has zero runtime callers and is not even exposed on the `engine.GitService` interface, so the engine physically cannot call it. The cancel cleanup flow is the last missing link, plus three smaller gaps: the run-state machine forbids cancelling a pre-active run, there is no `--allow-dirty` escape hatch despite the architecture recommending one, and there is no end-to-end regression test that exercises the branch creation / dirty refusal / cancel flow against a real git repo from the binary layer. This is pure composition-root + adapter-layer wiring plus one state-transition addition plus one CLI flag; no new domain logic is required.

**Tech Stack:** Go 1.22, `internal/gitops`, `internal/engine/{run,interfaces,engine}.go`, `internal/state/{models,containers}.go`, `internal/cli/run.go`, `internal/testfixtures`, `cmd/axiom`, `testing`.

---

## 1. Issue Statement (from `issues.md` §6)

> **P1: Git safety and work-branch isolation are defined, but not enforced**
>
> `internal/gitops/gitops.go:116-126` implements dirty-tree rejection via `ValidateClean`.
> `internal/gitops/gitops.go:237-266` implements `SetupWorkBranch`.
> `internal/gitops/gitops.go:270-287` implements `CancelCleanup`.
> A non-test search found no runtime callers for those methods.
> `internal/engine/run.go:44-55` only records `WorkBranch` in state.
> Smoke tests showed:
>
> - `axiom run` did not switch the repo to `axiom/<slug>`.
> - `axiom run` succeeded on a dirty working tree.
>
> **Why this matters:** A non-technical user is told Axiom uses isolated work branches and safe git hygiene, but the actual runtime works directly in the current checkout. Dirty-tree acceptance makes it easy to mix pre-existing user edits with Axiom-managed lifecycle state.

## 2. Reproduction and Verification (2026-04-08)

The issue was verified by a combination of (a) binary-level smoke tests against a fresh temp git repo using the pre-built `axiom.exe`, (b) `grep` for runtime callers, and (c) a review of the state machine and the `engine.GitService` interface. The result is that **two of the four recommendations from issue 6 were resolved as an incidental side-effect of the Issue 01 fix**, but the other two — plus related gaps — remain open.

### 2.1 What the Issue 01 fix already resolved

The Issue 01 fix (commit `2c5a73f`, "Issue 01 Fix") introduced the high-level `Engine.StartRun` entrypoint and wired the CLI to it. `StartRun` calls `ValidateClean` and `SetupWorkBranch` before persisting run state:

- `internal/engine/run.go:48` — `if err := e.git.ValidateClean(e.rootDir); err != nil { ... }`
- `internal/engine/run.go:71` — `if err := e.git.SetupWorkBranch(e.rootDir, run.BaseBranch, run.WorkBranch); err != nil { ... }`

**Binary smoke test on 2026-04-08** against `axiom.exe` at `0d41846` in a clean temp git repo confirms both:

- **Dirty-tree refusal works:** with an uncommitted `.axiom/` directory present, `axiom run "Build a REST API"` exits with `error: starting run: workspace not ready: working tree has uncommitted changes; commit or stash before running axiom` and returns exit code 1.
- **Work branch creation works:** after committing a `.gitignore` to clean the tree, `axiom run "Build a REST API"` succeeds, logs `created work branch branch=axiom/axiom-issue6-test`, and `git branch --show-current` returns `axiom/axiom-issue6-test`.

So recommendations 1 and 2 from issue 6 ("call `SetupWorkBranch` during run creation" and "refuse to create a run when the working tree is dirty") are **already resolved**. The issues.md entry for §6 was filed against a pre-Issue-01 version of the code and has not been updated since.

### 2.2 What is still open

#### 2.2.1 `CancelCleanup` has zero runtime callers

Grep across `*.go` files for `CancelCleanup`:

```
$ grep -rn "CancelCleanup" --include="*.go"
internal/gitops/gitops.go:266:// CancelCleanup reverts all uncommitted changes on the current branch and
internal/gitops/gitops.go:270:func (m *Manager) CancelCleanup(dir, baseBranch string) error {
internal/gitops/gitops_test.go:719,722,733,735,752,766,768,787,794,796:   (package tests only)
```

Every non-test hit is a definition or a test. No engine, CLI, or API code calls it.

`Engine.CancelRun` at `internal/engine/run.go:184-197` is a five-line method that only updates the DB status and emits an event:

```go
func (e *Engine) CancelRun(runID string) error {
    if err := e.db.UpdateRunStatus(runID, state.RunCancelled); err != nil {
        return fmt.Errorf("cancelling run: %w", err)
    }
    e.emitEvent(events.EngineEvent{
        Type:  events.RunCancelled,
        RunID: runID,
    })
    e.log.Info("run cancelled", "run_id", runID)
    return nil
}
```

It does not call `git.CancelCleanup`, does not stop running containers, and does not load the run to discover the base branch. The CLI command description at `internal/cli/run.go:140` (`"Cancel execution, kill containers, revert uncommitted changes"`) therefore lies — none of the three promised actions actually happen.

#### 2.2.2 `engine.GitService` does not expose `CancelCleanup`

`internal/engine/interfaces.go:13-23`:

```go
type GitService interface {
    CurrentBranch(dir string) (string, error)
    CreateBranch(dir, name string) error
    CurrentHEAD(dir string) (string, error)
    IsDirty(dir string) (bool, error)
    ValidateClean(dir string) error
    SetupWorkBranch(dir, baseBranch, workBranch string) error
    AddFiles(dir string, files []string) error
    Commit(dir string, message string) (string, error)
    ChangedFilesSince(dir, sinceRef string) ([]string, error)
}
```

`CancelCleanup` is **not in the interface.** Even if we patched `CancelRun` to call it, the engine's `e.git` field would not compile against it. This is the root blocker for the cleanup flow and must be fixed first.

#### 2.2.3 The state machine refuses to cancel a pre-active run

`internal/state/models.go:34-39`:

```go
var validRunTransitions = map[RunStatus][]RunStatus{
    RunDraftSRS:            {RunAwaitingSRSApproval},
    RunAwaitingSRSApproval: {RunActive, RunDraftSRS},
    RunActive:              {RunPaused, RunCancelled, RunCompleted, RunError},
    RunPaused:              {RunActive, RunCancelled},
}
```

Only `active` and `paused` runs can be cancelled. A run created by `axiom run "..."` immediately sits in `draft_srs` waiting for the external orchestrator to submit an SRS draft. Binary smoke test on 2026-04-08: running `axiom cancel` against such a run returns:

```
error: cancelling run: cancelling run: invalid status transition: draft_srs → cancelled
```

A user who starts a run, realises they typed the wrong prompt, and wants to cancel it *before* the SRS draft arrives has no supported way to do so. The only workaround is to manually edit the SQLite database. This is a usability regression that was never filed as its own issue but is directly implicated by issue 6's recommendation "Call `CancelCleanup` … from `CancelRun`" — the recommendation is meaningless unless the user can actually reach `CancelRun` from the states they will be in.

#### 2.2.4 No `--allow-dirty` escape hatch

Issue 6 explicitly recommends: *"Refuse to create a run when the working tree is dirty unless the user explicitly opts into a recovery mode."*

`Engine.StartRun` has no such opt-in. `ValidateClean` is called unconditionally, and `StartRunOptions` has no `AllowDirty bool` field. There is no CLI flag either. This is a small but architecturally-important gap: crash recovery scenarios (where Axiom needs to resume work on a branch that has legitimate uncommitted state) are impossible today.

#### 2.2.5 No end-to-end regression test for git safety

`cmd/axiom/phase20_integration_test.go` has two binary-level tests (`TestCLIInitRunStatusFlow_ExistingProjectFixture` and `TestCLIInitDefaultsNameFromGreenfieldDirectory`), both of which use `testfixtures.Materialize` to stand up a real git repo. Neither asserts that the git branch actually switches to `axiom/<slug>` after `axiom run`, neither tests the dirty-tree refusal path, and neither exercises `axiom cancel`. Any regression in git wiring — like the one Issue 01 fixed — would therefore not be caught at the binary layer.

The unit-level tests in `internal/engine/run_test.go` use a `noopGitService` that returns `nil` for everything, so they also do not verify that the right git calls are made.

#### 2.2.6 Stale documentation

Several docs still claim git safety is not wired:

- `internal/gitops/gitops.go:11` — package comment still says "the git package is not yet wired in"
- `docs/git-operations.md:11` — *"but `engine.CreateRun` currently records the intended work branch in state without yet invoking `SetupWorkBranch`"*
- `docs/git-operations.md:57` — *"the live `axiom run` path does not yet call it automatically"*
- `docs/git-operations.md:80` — *"but the current `axiom run` command does not yet invoke this path"*
- `docs/git-operations.md:202` — *"Work-branch creation and cancellation cleanup are implemented in the git package but not yet triggered by `CreateRun` / `CancelRun`."*

The Issue 01 fix updated half of the code but none of these doc notes. All of them must be corrected as part of issue 6 (the creation half is already wired; the cancel half will be wired by this fix).

## 3. Root Cause

Two separate root causes stacked on top of each other, plus three secondary gaps:

**Primary root cause — interface shape:** The `engine.GitService` interface was defined early in Phase 3 based on the first few git operations the engine needed (`CurrentHEAD`, `AddFiles`, `Commit`, `ChangedFilesSince`). When Phase 4 added `SetupWorkBranch` and `CancelCleanup` to the `gitops.Manager`, only `SetupWorkBranch` was ever added to the interface (by the Issue 01 fix). `CancelCleanup` was left out, which silently made the cancel cleanup flow impossible to wire even though everyone assumed it existed.

**Primary root cause — lifecycle shape:** `Engine.CancelRun` was built during Phase 3 as a minimal DB-status-transition method, before `gitops.CancelCleanup` existed and before the container service had a per-run enumeration API (`state.DB.ListActiveContainers(runID)`). Both of those helpers later landed but `CancelRun` was never revisited to consume them.

**Secondary: state machine.** The run state machine was authored around the assumption that cancellation only applies to runs that have already begun active work. In a world with an external-orchestrator handoff, that assumption is wrong: a user can sit in `draft_srs` for an arbitrarily long time while waiting on their orchestrator, and they must be able to abandon the run.

**Secondary: no recovery opt-in.** Dirty-tree refusal was built before the recovery-mode use case was articulated. The `allow_dirty` escape hatch is a trivial addition that was simply never implemented.

**Secondary: test coverage gap.** The `noopGitService` test fake returns `nil` for every method, so unit tests can never notice when a production code path forgets to call the git layer. The binary-level tests exist but don't cover the git-lifecycle assertions.

## 4. Fix Strategy

Keep the surface area small and follow the issue-05 playbook: pure composition-root / adapter-layer wiring plus narrowly scoped state-machine and CLI additions. No changes to `gitops.Manager` itself — its API is the contract.

1. **Extend the `engine.GitService` interface** with `CancelCleanup(dir, baseBranch string) error`. Add the stub to every test fake that implements the interface (four files today: `internal/cli/cli_test.go`, `internal/engine/engine_test.go`, `internal/engine/executor_test.go`, plus `trackingGitService`).
2. **Rewrite `Engine.CancelRun`** to execute the full architectural cancel protocol in a well-defined order:
   1. Load the run record via `e.db.GetRun(runID)` to capture `BaseBranch` (needed for the git cleanup call) and to enforce "run must exist".
   2. Transition the DB status first via `e.db.UpdateRunStatus(runID, state.RunCancelled)`. This is the single atomic barrier that blocks the scheduler from dispatching any new work for this run (the scheduler already filters by run status in `findReadyTasks`).
   3. Enumerate and stop any containers still running for this run via `e.db.ListActiveContainers(runID)` followed by `e.container.Stop(ctx, cs.ID)` for each. Failures here are logged but do not block the cancel — per the architecture's "safety over convenience" doctrine, the user's intent to cancel is absolute and container leaks are recoverable via the next session's recovery pass (§22).
   4. Call `e.git.CancelCleanup(e.rootDir, run.BaseBranch)` to revert uncommitted changes and switch back to the base branch. Failures here are logged with a loud warning but do not block the cancel — the user can manually recover with `git reset --hard && git checkout main`.
   5. Emit the existing `RunCancelled` event.
3. **Expand `validRunTransitions`** to allow `RunDraftSRS → RunCancelled` and `RunAwaitingSRSApproval → RunCancelled`. Justification: §23.4 does not forbid cancelling an un-started run, and there is no safety concern because no containers exist and no commits exist for such runs — the cancel flow in that case is a no-op except for the DB transition.
4. **Add an explicit `AllowDirty` opt-in** to `StartRunOptions` and a `--allow-dirty` flag to `cli.RunCmd`. When set, `StartRun` skips `ValidateClean` but logs a `WARN`-level message naming the files that would have blocked the run (via `git.IsDirty` + a new small helper, or by capturing the error message from `ValidateClean` and downgrading it to a log).
5. **Add regression tests at four layers:**
   - **State-machine unit test** (`internal/state/runs_test.go`): extend the `TestUpdateRunStatus_ValidTransitions` table with the two new entries; add `TestCancelRun_FromDraftSRS_IsValid` and `TestCancelRun_FromAwaitingSRSApproval_IsValid`.
   - **Engine unit tests** (`internal/engine/run_test.go`): `TestCancelRun_CallsCancelCleanup` (tracking git service), `TestCancelRun_StopsActiveContainers` (tracking container service), `TestCancelRun_ProceedsWhenGitCleanupFails` (guarantees the fail-open semantics), `TestCancelRun_FromDraftSRS_WorksEndToEnd`, `TestCancelRun_LoadsRunForBaseBranch` (regression guard for the GetRun call).
   - **Engine unit test** (`internal/engine/startrun_test.go`): `TestStartRun_AllowDirtyBypassesValidateClean` using a tracking git service whose `IsDirty` returns `true` and whose `ValidateClean` returns an error — asserts `StartRun` succeeds when `AllowDirty: true` and fails when `AllowDirty: false`.
   - **Binary-level acceptance tests** (`cmd/axiom/phase20_integration_test.go`): `TestCLIRun_SwitchesToWorkBranch` (uses `testfixtures.Materialize` + real git + binary `executeCobra`; asserts `git branch --show-current` returns `axiom/<slug>` after `axiom run`), `TestCLIRun_RefusesDirtyTree` (asserts non-zero exit and the error message), `TestCLIRun_AllowDirtyBypass` (asserts `--allow-dirty` lets it through on a dirty tree), `TestCLICancel_CleansUpAndReturnsToBase` (seeds an untracked scratch file on the work branch, runs `axiom cancel`, asserts the branch switches back to `main`, the scratch file is gone, and the `axiom/<slug>` branch still exists).
6. **Update documentation** to reflect the wired state:
   - `internal/gitops/gitops.go:11` — replace the stale comment.
   - `docs/git-operations.md` — remove the four "not yet" notes; add a new "Cancel Lifecycle" subsection describing the `CancelRun → ListActiveContainers → Stop → CancelCleanup → UpdateRunStatus → emit event` flow; update the `GitService` interface listing at line 179 to include `CancelCleanup`.
   - `docs/cli-reference.md:136-176` — document `--allow-dirty` on `axiom run`; update the `axiom cancel` section to describe the cleanup flow and the new pre-active cancellation support.
   - `docs/getting-started.md` — update the "Git Branch Strategy" section (referenced by issue 6) to describe the full lifecycle including cancel cleanup.
7. **Commit each task separately** per the TDD / frequent-commits discipline established by Issues 01 / 04 / 05.

## 5. File Structure

| File | Role | Change type |
|---|---|---|
| `internal/engine/interfaces.go` | Engine service interfaces | **Modify** — add `CancelCleanup(dir, baseBranch string) error` to `GitService` |
| `internal/engine/run.go` | Run lifecycle | **Modify** — rewrite `CancelRun` to load run, stop containers, call `CancelCleanup`, update status, emit event; add `AllowDirty` to `StartRunOptions`; update `StartRun` to skip `ValidateClean` when `AllowDirty` is set |
| `internal/state/models.go` | Run state machine | **Modify** — add `(RunDraftSRS, RunCancelled)` and `(RunAwaitingSRSApproval, RunCancelled)` to `validRunTransitions` |
| `internal/cli/run.go` | CLI run + cancel commands | **Modify** — add `--allow-dirty` flag to `RunCmd`, pass `AllowDirty` through to `StartRunOptions`; update the `axiom run` long description to mention the flag |
| `internal/cli/cli_test.go` | CLI test fakes | **Modify** — add `CancelCleanup` stub to `noopGitService` |
| `internal/engine/engine_test.go` | Engine test fakes | **Modify** — add `CancelCleanup` stub to `noopGitService` |
| `internal/engine/executor_test.go` | Executor test fakes | **Modify** — add `CancelCleanup` stub to `trackingGitService` |
| `internal/engine/run_test.go` | Run lifecycle tests | **Modify** — add `TestCancelRun_CallsCancelCleanup`, `TestCancelRun_StopsActiveContainers`, `TestCancelRun_ProceedsWhenGitCleanupFails`, `TestCancelRun_FromDraftSRS_WorksEndToEnd`, `TestCancelRun_LoadsRunForBaseBranch` |
| `internal/engine/startrun_test.go` | StartRun tests | **Modify** — add `TestStartRun_AllowDirtyBypassesValidateClean` |
| `internal/state/runs_test.go` | State machine tests | **Modify** — extend valid-transitions table with the two new entries |
| `cmd/axiom/phase20_integration_test.go` | Binary acceptance tests | **Modify** — add `TestCLIRun_SwitchesToWorkBranch`, `TestCLIRun_RefusesDirtyTree`, `TestCLIRun_AllowDirtyBypass`, `TestCLICancel_CleansUpAndReturnsToBase` |
| `internal/gitops/gitops.go` | Package comment | **Modify** — remove the stale "not yet wired" note at the top of the file |
| `docs/git-operations.md` | Reference docs | **Modify** — remove four "not yet" notes; add "Cancel Lifecycle" section; update `GitService` interface listing |
| `docs/cli-reference.md` | CLI reference | **Modify** — document `--allow-dirty`; rewrite `axiom cancel` section |
| `docs/getting-started.md` | Quick start | **Modify** — update "Git Branch Strategy" section with full cancel cleanup lifecycle |

No new files. No changes to `internal/gitops/gitops.go` beyond the package-comment note. No changes to `gitops.Manager` method signatures.

---

## 6. Task Breakdown

### Task 1: Extend `engine.GitService` with `CancelCleanup`

**Files:**
- Modify: `internal/engine/interfaces.go`
- Modify: `internal/engine/engine_test.go` (noopGitService)
- Modify: `internal/engine/executor_test.go` (trackingGitService)
- Modify: `internal/cli/cli_test.go` (noopGitService)

- [ ] **Step 1: Write the failing compile-time check**

  Add a new test file `internal/engine/interfaces_git_cancel_test.go` (or extend the existing `interfaces_exec_test.go`) containing:

  ```go
  package engine

  import "testing"

  func TestGitServiceInterfaceIncludesCancelCleanup(t *testing.T) {
      var _ GitService = (*cancelCleanupAsserter)(nil)
  }

  type cancelCleanupAsserter struct{ noopGitService }

  func (c *cancelCleanupAsserter) CancelCleanup(dir, baseBranch string) error { return nil }
  ```

  `go test ./internal/engine/ -run TestGitServiceInterfaceIncludesCancelCleanup` MUST fail with a "missing method CancelCleanup" compile error before the interface is extended. This pins the intent of the change.

- [ ] **Step 2: Extend the interface**

  In `internal/engine/interfaces.go`, add to `GitService`:

  ```go
  // CancelCleanup reverts uncommitted changes and switches back to the base branch.
  // Called by Engine.CancelRun to satisfy Architecture §23.4 (committed work on
  // the work branch is preserved — only uncommitted state is discarded).
  CancelCleanup(dir, baseBranch string) error
  ```

  Place it after `SetupWorkBranch` for visual pairing (both are run-lifecycle methods).

- [ ] **Step 3: Verify `gitops.Manager` already satisfies the extended interface**

  `gitops.Manager.CancelCleanup` already exists at `internal/gitops/gitops.go:270`. No changes needed; the extension is purely about making the existing implementation visible to the engine.

  Run `go build ./...` — expect a compile error on `noopGitService`, `trackingGitService`, and the CLI `noopGitService` all missing `CancelCleanup`.

- [ ] **Step 4: Add `CancelCleanup` stubs to the three test fakes**

  `internal/engine/engine_test.go` (around line 90, after `SetupWorkBranch`):

  ```go
  func (n *noopGitService) CancelCleanup(dir, baseBranch string) error { return nil }
  ```

  `internal/engine/executor_test.go` (around line 145, after `SetupWorkBranch`):

  ```go
  func (g *trackingGitService) CancelCleanup(dir, baseBranch string) error { return nil }
  ```

  `internal/cli/cli_test.go` (around line 47, after `SetupWorkBranch`):

  ```go
  func (n *noopGitService) CancelCleanup(dir, baseBranch string) error { return nil }
  ```

  Run `go build ./...` — should now compile clean. Run `go test ./internal/engine/ -run TestGitServiceInterfaceIncludesCancelCleanup -count=1` — should pass.

- [ ] **Step 5: Commit**

  ```
  feat(engine): add CancelCleanup to GitService interface

  The gitops.Manager already implements CancelCleanup but the engine
  interface did not expose it, making it impossible to wire into
  Engine.CancelRun. Expose the method and stub it on all test fakes.

  Prep for Issue 06 fix — no behaviour change yet.
  ```

---

### Task 2: Expand run state machine to allow pre-active cancellation

**Files:**
- Modify: `internal/state/models.go`
- Modify: `internal/state/runs_test.go`

- [ ] **Step 1: Write the failing transition test**

  In `internal/state/runs_test.go`, inside `TestUpdateRunStatus_ValidTransitions`, extend the `transitions` table with:

  ```go
  {RunDraftSRS, RunCancelled},
  {RunAwaitingSRSApproval, RunCancelled},
  ```

  Run `go test ./internal/state/ -run TestUpdateRunStatus_ValidTransitions -count=1`. MUST fail with `invalid status transition: draft_srs → cancelled` because these transitions are not yet in `validRunTransitions`.

- [ ] **Step 2: Expand `validRunTransitions`**

  `internal/state/models.go:34-39`:

  ```go
  var validRunTransitions = map[RunStatus][]RunStatus{
      RunDraftSRS:            {RunAwaitingSRSApproval, RunCancelled},
      RunAwaitingSRSApproval: {RunActive, RunDraftSRS, RunCancelled},
      RunActive:              {RunPaused, RunCancelled, RunCompleted, RunError},
      RunPaused:              {RunActive, RunCancelled},
  }
  ```

  Run the same test — should now pass.

- [ ] **Step 3: Add named regression tests**

  Add two small focused tests that create a run, drive it through the relevant prefix (zero transitions for `RunDraftSRS`, one for `RunAwaitingSRSApproval`), call `UpdateRunStatus(RunCancelled)`, and assert `cancelled_at` is set:

  ```go
  func TestUpdateRunStatus_CancelFromDraftSRS(t *testing.T) { ... }
  func TestUpdateRunStatus_CancelFromAwaitingSRSApproval(t *testing.T) { ... }
  ```

  Both should pass without additional code changes.

- [ ] **Step 4: Commit**

  ```
  fix(state): allow cancelling a run from draft_srs or awaiting_srs_approval

  The run state machine previously forbade cancellation from any state
  other than active or paused. That made it impossible for a user to
  abandon a run while waiting for the external orchestrator to submit
  an SRS draft — the only workaround was to manually edit the SQLite
  DB. Expand validRunTransitions so axiom cancel works from draft_srs
  and awaiting_srs_approval.

  No safety concern: pre-active runs have no containers and no commits,
  so the cancel flow is just a DB transition.

  Prep for Issue 06 fix.
  ```

---

### Task 3: Rewrite `Engine.CancelRun` to execute the full architectural cancel protocol

**Files:**
- Modify: `internal/engine/run.go`
- Modify: `internal/engine/run_test.go`
- Modify: `internal/engine/executor_test.go` (extend `trackingGitService` to record `CancelCleanup` calls)
- Modify: `internal/engine/engine_test.go` (extend `noopContainerService` to track `Stop` calls — or introduce a new small tracking container service in `run_test.go`)

- [ ] **Step 1: Write the first failing engine test — `TestCancelRun_CallsCancelCleanup`**

  Build a small `trackingGitService` variant (in `run_test.go` or reuse the executor one) that records every `CancelCleanup` call. Create a run, transition it to `active`, call `CancelRun(run.ID)`, and assert the recorded call matches `(rootDir, "main")`:

  ```go
  func TestCancelRun_CallsCancelCleanup(t *testing.T) {
      gitSvc := &trackingGitService{}
      e := newTestEngineWithGit(t, gitSvc)
      // ... seed project, create run, drive to active ...
      if err := e.CancelRun(run.ID); err != nil {
          t.Fatalf("CancelRun: %v", err)
      }
      if len(gitSvc.cancelCleanupCalls) != 1 {
          t.Fatalf("CancelCleanup calls = %d, want 1", len(gitSvc.cancelCleanupCalls))
      }
      call := gitSvc.cancelCleanupCalls[0]
      if call.dir != e.RootDir() || call.baseBranch != "main" {
          t.Errorf("CancelCleanup args = %+v, want (%q, main)", call, e.RootDir())
      }
  }
  ```

  Run it — MUST fail because current `CancelRun` doesn't call `CancelCleanup`.

- [ ] **Step 2: Rewrite `CancelRun` minimally to pass the test**

  `internal/engine/run.go` — replace the existing `CancelRun` with:

  ```go
  // CancelRun transitions a run to cancelled and performs the architectural
  // cancel protocol: (1) flip the DB status (atomic barrier against scheduler
  // dispatch), (2) stop any containers still running for the run, (3) revert
  // uncommitted git changes and switch back to the base branch, (4) emit the
  // RunCancelled event. Per Architecture §23.4, committed work on the work
  // branch is preserved — only uncommitted state is discarded.
  //
  // Container and git cleanup failures are logged but do not block the cancel.
  // The user's intent to cancel is absolute; leaked containers are recoverable
  // via the next session's startup recovery pass (§22), and a failed git
  // cleanup leaves a clear error message with manual-recovery instructions.
  func (e *Engine) CancelRun(runID string) error {
      run, err := e.db.GetRun(runID)
      if err != nil {
          return fmt.Errorf("loading run %s: %w", runID, err)
      }

      // Step 1: atomic DB barrier. Scheduler's findReadyTasks filters by run
      // status, so flipping this first prevents any new task dispatch.
      if err := e.db.UpdateRunStatus(runID, state.RunCancelled); err != nil {
          return fmt.Errorf("cancelling run: %w", err)
      }

      // Step 2: best-effort container shutdown.
      if e.container != nil {
          if active, err := e.db.ListActiveContainers(runID); err != nil {
              e.log.Warn("listing active containers during cancel",
                  "run_id", runID, "error", err)
          } else {
              ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
              defer cancel()
              for _, cs := range active {
                  if err := e.container.Stop(ctx, cs.ID); err != nil {
                      e.log.Warn("stopping container during cancel",
                          "run_id", runID, "container_id", cs.ID, "error", err)
                  }
              }
          }
      }

      // Step 3: best-effort git cleanup.
      if e.git != nil {
          if err := e.git.CancelCleanup(e.rootDir, run.BaseBranch); err != nil {
              e.log.Warn("git cancel cleanup failed; manual recovery may be required",
                  "run_id", runID, "base_branch", run.BaseBranch, "error", err,
                  "hint", "run `git reset --hard && git checkout "+run.BaseBranch+"` to recover")
          }
      }

      e.emitEvent(events.EngineEvent{
          Type:  events.RunCancelled,
          RunID: runID,
      })

      e.log.Info("run cancelled",
          "run_id", runID,
          "base_branch", run.BaseBranch,
      )
      return nil
  }
  ```

  Add the missing imports (`context`, `time` if not already imported). The `ListActiveContainers` helper already exists at `internal/state/containers.go:45`.

  Run the test — should pass.

- [ ] **Step 3: Add `TestCancelRun_StopsActiveContainers`**

  Introduce a small tracking container service (in `run_test.go`) that records every `Stop` call. Seed a `container_sessions` row with `run_id` matching the run and `stopped_at` NULL. Call `CancelRun` and assert `Stop` was called with the seeded container ID.

  ```go
  func TestCancelRun_StopsActiveContainers(t *testing.T) {
      containerSvc := &trackingContainerService{}
      e := newTestEngineWith(t, nil, containerSvc)
      // ... seed project, run, active containers ...
      if err := e.CancelRun(run.ID); err != nil {
          t.Fatalf("CancelRun: %v", err)
      }
      if !slices.Contains(containerSvc.stopped, "c-meeseeks-1") {
          t.Errorf("expected c-meeseeks-1 to be stopped; got %v", containerSvc.stopped)
      }
  }
  ```

- [ ] **Step 4: Add `TestCancelRun_ProceedsWhenGitCleanupFails`**

  Use a git service whose `CancelCleanup` returns an error. Call `CancelRun` and assert:
  - The method returns `nil` (fail-open semantics)
  - The run status is `cancelled`
  - The `RunCancelled` event was emitted
  - The error was logged (optional — can skip asserting log output)

  This pins the fail-open contract so a future refactor can't accidentally make git cleanup errors fatal.

- [ ] **Step 5: Add `TestCancelRun_FromDraftSRS_WorksEndToEnd`**

  Create a project, call `e.StartRun(...)` (which lands in `draft_srs`), then call `e.CancelRun(run.ID)` and assert:
  - No error returned
  - `GetRun` shows status `cancelled` and `cancelled_at` set
  - Git `CancelCleanup` was called with `(rootDir, "main")` — because even pre-active runs go through the full protocol

  This is the end-to-end proof that a user can cancel a run they just started.

- [ ] **Step 6: Add `TestCancelRun_LoadsRunForBaseBranch`**

  Create a run with a non-default `BaseBranch: "develop"`. Call `CancelRun`. Assert `CancelCleanup` was called with `(rootDir, "develop")`. This is a regression guard against someone hardcoding `"main"` in the cancel flow.

- [ ] **Step 7: Update the existing `TestCancelRun`**

  The existing test at `run_test.go:248-299` uses `testEngine` (which uses `noopGitService`). It will still pass as-is because `noopGitService.CancelCleanup` now exists and returns nil. No change needed — just verify it still passes.

- [ ] **Step 8: Run the full engine test suite**

  `go test ./internal/engine/ -count=1 -timeout 120s -skip "TestExecuteAttempt_SuccessEnqueuesAndMerges|TestExecuteAttempt_ValidationFailureRequeuesTask|TestEngineWorkers_SchedulerExecutorMergeQueueFlow"`. Excluded tests are pre-existing Windows-IPC hangs documented in Issues 04 and 05. All other tests including the new ones must pass.

- [ ] **Step 9: Commit**

  ```
  fix(engine): CancelRun now stops containers and reverts uncommitted git changes

  Previously CancelRun was a five-line DB-status-transition. Per
  Architecture §23.4, cancelling a run MUST:
    1. Flip the DB status first (atomic barrier against scheduler dispatch).
    2. Stop any containers still running for the run.
    3. Revert uncommitted changes and switch back to the base branch
       (committed work on the work branch is preserved).
    4. Emit the RunCancelled event.

  The CLI's cancel description ("Cancel execution, kill containers,
  revert uncommitted changes") now actually matches behaviour.

  Container and git cleanup failures are logged but do not block the
  cancel — the user's intent is absolute, and leaked containers are
  recoverable via the next session's startup recovery pass (§22).

  Fixes Issue 06 part 1/3.
  ```

---

### Task 4: Add `--allow-dirty` opt-in for recovery mode

**Files:**
- Modify: `internal/engine/run.go` (`StartRunOptions` + `StartRun`)
- Modify: `internal/engine/startrun_test.go`
- Modify: `internal/cli/run.go` (CLI flag)
- Modify: `internal/cli/run_test.go`

- [ ] **Step 1: Write the failing test**

  `internal/engine/startrun_test.go`:

  ```go
  func TestStartRun_AllowDirtyBypassesValidateClean(t *testing.T) {
      gitSvc := &dirtyGitService{}  // ValidateClean returns an error
      e := newTestEngineWithGit(t, gitSvc)
      projectID := seedProject(t, e, "allow-dirty")

      _, err := e.StartRun(StartRunOptions{
          ProjectID: projectID,
          Prompt:    "recovery scenario",
          AllowDirty: true,
      })
      if err != nil {
          t.Fatalf("StartRun with AllowDirty: %v", err)
      }
      if gitSvc.validateCleanCalls != 0 {
          t.Errorf("ValidateClean calls = %d, want 0 when AllowDirty is set", gitSvc.validateCleanCalls)
      }
      if gitSvc.setupWorkBranchCalls != 1 {
          t.Errorf("SetupWorkBranch calls = %d, want 1", gitSvc.setupWorkBranchCalls)
      }
  }

  func TestStartRun_RefusesDirtyTreeByDefault(t *testing.T) {
      gitSvc := &dirtyGitService{}
      e := newTestEngineWithGit(t, gitSvc)
      projectID := seedProject(t, e, "refuse-dirty")

      _, err := e.StartRun(StartRunOptions{
          ProjectID: projectID,
          Prompt:    "normal run",
      })
      if err == nil {
          t.Fatal("expected error for dirty tree")
      }
  }
  ```

  Run it — MUST fail because `AllowDirty` field doesn't exist.

- [ ] **Step 2: Add `AllowDirty` to `StartRunOptions`**

  ```go
  type StartRunOptions struct {
      ProjectID  string
      Prompt     string
      BaseBranch string
      BudgetUSD  float64
      Source     string
      // AllowDirty bypasses the working-tree-clean check. Set only for
      // recovery scenarios where the user explicitly opts into resuming
      // work on a branch with uncommitted state. Architecture §28.2
      // requires a clean tree by default; this flag is the escape hatch.
      AllowDirty bool
  }
  ```

- [ ] **Step 3: Update `StartRun` to honor `AllowDirty`**

  ```go
  if opts.AllowDirty {
      e.log.Warn("workspace clean check bypassed via AllowDirty",
          "source", source,
          "hint", "commit or stash before next run to avoid mixing state")
  } else {
      if err := e.git.ValidateClean(e.rootDir); err != nil {
          return nil, fmt.Errorf("workspace not ready: %w", err)
      }
  }
  ```

  Run the tests — both should now pass.

- [ ] **Step 4: Wire the CLI flag**

  `internal/cli/run.go`:

  ```go
  func RunCmd(verbose *bool) *cobra.Command {
      var budgetUSD float64
      var allowDirty bool

      cmd := &cobra.Command{
          Use:   "run <prompt>",
          Short: "Start a new project run",
          Long: "Start a new project: generate SRS, await approval, execute.\n\n" +
              "By default, axiom refuses to start on a dirty working tree (Architecture §28.2).\n" +
              "Pass --allow-dirty to bypass this check for recovery scenarios.",
          Args: cobra.ExactArgs(1),
          RunE: func(cmd *cobra.Command, args []string) error {
              // ...
              return runAction(application, projectID, args[0], budgetUSD, allowDirty, cmd.OutOrStdout())
          },
      }

      cmd.Flags().Float64Var(&budgetUSD, "budget", 0, "budget in USD (defaults to config value)")
      cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "bypass the clean-working-tree check (recovery only)")
      return cmd
  }
  ```

  Update `runAction` signature to accept and forward `allowDirty` into `StartRunOptions.AllowDirty`.

- [ ] **Step 5: Add a CLI test**

  `internal/cli/run_test.go`: assert that passing `--allow-dirty` propagates through to `StartRunOptions.AllowDirty`. Use the existing CLI test scaffolding.

- [ ] **Step 6: Commit**

  ```
  feat(cli): add --allow-dirty escape hatch for recovery scenarios

  Architecture §28.2 requires a clean working tree before `axiom run`,
  but crash-recovery scenarios legitimately need to resume work on a
  branch with uncommitted state. Add AllowDirty to StartRunOptions and
  expose it as --allow-dirty on the CLI. When set, StartRun logs a
  loud WARN and skips ValidateClean; SetupWorkBranch still runs.

  Fixes Issue 06 part 2/3.
  ```

---

### Task 5: Add binary-level acceptance tests

**Files:**
- Modify: `cmd/axiom/phase20_integration_test.go`

- [ ] **Step 1: Add `TestCLIRun_SwitchesToWorkBranch`**

  Use `testfixtures.Materialize("existing-go")` to stand up a real git repo. `axiom init`, commit the `.axiom/` artifacts, then `axiom run "..."`. Use `exec.Command("git", "-C", repoDir, "branch", "--show-current")` to assert the branch is now `axiom/fixture-existing` (slug derived from the fixture name — use `project.Slugify` to avoid hardcoding).

  ```go
  func TestCLIRun_SwitchesToWorkBranch(t *testing.T) {
      repoDir, err := testfixtures.Materialize("existing-go")
      if err != nil {
          t.Fatalf("Materialize: %v", err)
      }
      t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

      withWorkingDir(t, repoDir, func() {
          verbose = false
          executeCobra(t, initCmd(), "--name", "Fixture Existing")
          gitCommitAll(t, repoDir, "axiom init")

          executeCobra(t, cli.RunCmd(&verbose), "Build the first feature")

          currentBranch := gitCurrentBranch(t, repoDir)
          expected := "axiom/" + project.Slugify("Fixture Existing")
          if currentBranch != expected {
              t.Errorf("current branch = %q, want %q", currentBranch, expected)
          }
      })
  }
  ```

  Add a `gitCurrentBranch` helper next to the existing `gitCommitAll` helper.

- [ ] **Step 2: Add `TestCLIRun_RefusesDirtyTree`**

  Same fixture, `axiom init`, but do NOT commit the `.axiom/` artifacts. Call `cli.RunCmd` and assert it returns a non-nil error whose message contains `"working tree has uncommitted changes"`. Use a new `executeCobraExpectError` helper variant that returns the error instead of calling `t.Fatalf` on it.

- [ ] **Step 3: Add `TestCLIRun_AllowDirtyBypass`**

  Same as Step 2 but pass `--allow-dirty`. Expect the run to succeed, and assert the branch has switched.

- [ ] **Step 4: Add `TestCLICancel_CleansUpAndReturnsToBase`**

  Full lifecycle test:
  1. Materialize fixture, `axiom init`, commit, `axiom run "..."` — now on `axiom/fixture-existing` branch.
  2. Write a scratch file `scratch.txt` in the repo root (do not commit).
  3. Call `cli.CancelCmd` via `executeCobra`.
  4. Assert:
     - `git branch --show-current` returns `main` (base branch)
     - `scratch.txt` no longer exists (removed by `git clean -fd`)
     - `git branch --list axiom/fixture-existing` returns a non-empty result (branch preserved per §23.4)

  This is the single most important regression test for the issue.

- [ ] **Step 5: Run the acceptance suite**

  `go test ./cmd/axiom/ -count=1 -timeout 180s`

  All new tests must pass. Existing tests must still pass.

- [ ] **Step 6: Commit**

  ```
  test(cmd/axiom): add binary-level git safety regression tests

  Four new acceptance tests exercise the full git lifecycle through
  the CLI entrypoint against a real git repo:
    - TestCLIRun_SwitchesToWorkBranch asserts the branch on disk
      matches axiom/<slug> after `axiom run`.
    - TestCLIRun_RefusesDirtyTree asserts the dirty-tree error path.
    - TestCLIRun_AllowDirtyBypass asserts the --allow-dirty escape hatch.
    - TestCLICancel_CleansUpAndReturnsToBase asserts uncommitted changes
      are reverted and the repo is back on main after `axiom cancel`,
      while the axiom/<slug> branch is preserved per §23.4.

  Closes the gap where no test ever verified the git contract at the
  binary layer, which is why Issues 01 and 06 silently drifted.

  Fixes Issue 06 part 3/3 (tests).
  ```

---

### Task 6: Documentation updates

**Files:**
- Modify: `internal/gitops/gitops.go` (package comment)
- Modify: `docs/git-operations.md`
- Modify: `docs/cli-reference.md`
- Modify: `docs/getting-started.md`

- [ ] **Step 1: Clean up `internal/gitops/gitops.go:11`**

  The package comment currently says (paraphrased) "git package already supports deterministic branch setup and dirty-tree validation, but `engine.CreateRun` currently records the intended work branch in state without yet invoking `SetupWorkBranch`." Replace with a clean description of the actual wired state: `StartRun` calls `ValidateClean` and `SetupWorkBranch`; `CancelRun` calls `CancelCleanup`.

- [ ] **Step 2: Rewrite four stale notes in `docs/git-operations.md`**

  - Line 11 — remove the "but `engine.CreateRun` currently records the intended work branch in state without yet invoking `SetupWorkBranch`" parenthetical.
  - Line 57 — remove "Current engine note: this is the intended high-level entry point for run startup, but the live `axiom run` path does not yet call it automatically."
  - Line 80 — remove "The manager therefore supports the architecture's dirty-tree requirement, but the current `axiom run` command does not yet invoke this path."
  - Line 202 — rewrite "Work-branch creation and cancellation cleanup are implemented in the git package but not yet triggered by `CreateRun` / `CancelRun`." to describe the wired state.

- [ ] **Step 3: Update the `GitService` interface listing in `docs/git-operations.md`**

  The listing at line 179 is missing `ValidateClean`, `SetupWorkBranch`, and `CancelCleanup`. Replace it with the current 10-method interface.

- [ ] **Step 4: Add a "Cancel Lifecycle" subsection to `docs/git-operations.md`**

  Describe the four-step cancel protocol (DB barrier → container stop → git cleanup → event) and the fail-open semantics for container/git failures.

- [ ] **Step 5: Update `docs/cli-reference.md`**

  - `axiom run` section (around line 136-160): document `--allow-dirty` with the recovery-scenario use case.
  - `axiom cancel` section (around line 160-176): rewrite to describe the cleanup flow — "reverts uncommitted changes on the work branch, switches back to the base branch, and stops any running containers. Committed work on the `axiom/<slug>` branch is preserved for manual review."
  - Note that `axiom cancel` now works from `draft_srs` and `awaiting_srs_approval`.

- [ ] **Step 6: Update `docs/getting-started.md`**

  Find the "Git Branch Strategy" section (referenced by issue 6's "Docs affected" list). Update it to describe the full lifecycle: start → work branch created → tasks committed → user merges OR cancels → uncommitted reverted, base branch restored, work branch preserved.

- [ ] **Step 7: Commit**

  ```
  docs: describe wired git safety lifecycle

  With CancelRun now executing the full architectural cancel protocol
  and axiom run --allow-dirty available as a recovery escape hatch,
  the four "not yet wired" notes in docs/git-operations.md are no
  longer accurate. Rewrite them to describe the actual runtime flow.

  Also update cli-reference and getting-started to document the new
  --allow-dirty flag and the expanded cancel cleanup behaviour.

  Fixes Issue 06 docs drift.
  ```

---

## 7. Acceptance Criteria

The fix is complete when **all** of the following are true:

- [ ] `go build ./...` compiles cleanly.
- [ ] `go vet ./...` is clean.
- [ ] `go test ./internal/state/ -run TestUpdateRunStatus -count=1` passes, including the two new transition entries.
- [ ] `go test ./internal/engine/ -run "TestCancelRun|TestStartRun_AllowDirty|TestGitServiceInterfaceIncludesCancelCleanup" -count=1` passes.
- [ ] `go test ./internal/engine/ -count=1 -timeout 120s -skip "TestExecuteAttempt_SuccessEnqueuesAndMerges|TestExecuteAttempt_ValidationFailureRequeuesTask|TestEngineWorkers_SchedulerExecutorMergeQueueFlow"` passes (three skipped tests are pre-existing Windows-IPC hangs documented in Issues 04/05).
- [ ] `go test ./cmd/axiom/ -count=1 -timeout 180s` passes, including all four new binary-level acceptance tests.
- [ ] `grep -rn "CancelCleanup" --include="*.go" internal/engine/` returns a runtime call site in `internal/engine/run.go` (not just tests).
- [ ] `grep -rn "not yet wired\|does not yet invoke\|not yet called\|not yet triggered\|without yet invoking" docs/git-operations.md` returns zero hits.
- [ ] Binary smoke test in a temp git repo:
  1. `axiom init && git commit -am "axiom init" && axiom run "test"` → succeeds, current branch is `axiom/<slug>`.
  2. Create an uncommitted file, `axiom cancel` → exits 0, current branch is `main`, uncommitted file is gone, `axiom/<slug>` branch still exists.
  3. `axiom run "test 2"` on a dirty tree → refuses with exit code 1.
  4. `axiom run "test 2" --allow-dirty` on a dirty tree → succeeds with a WARN log about bypass.
  5. Immediately after `axiom run` (while in `draft_srs`), `axiom cancel` → exits 0 and transitions the run to `cancelled`.

## 8. Non-Goals (out of scope for this fix)

- **No changes to `gitops.Manager` method signatures.** The existing `ValidateClean`, `SetupWorkBranch`, and `CancelCleanup` APIs are the contract; we only add a method to the engine *interface* that re-declares the existing `Manager` method.
- **No automatic conflict resolution on cancel.** If `git clean -fd` cannot remove a file due to filesystem permissions, the fail-open path logs the error and the user recovers manually. We do not attempt retries or alternative cleanup strategies.
- **No new Engine methods for granular control** (like `CancelRunWithoutGit` or `CancelRunKeepContainers`). The single `CancelRun` method does the architectural protocol atomically.
- **No orchestrator-level work-branch strategy changes** (e.g., per-task branches, branch-per-attempt). The architecture is clear that one work branch per run is the contract.
- **No fix for the three pre-existing Windows IPC hangs** in `internal/engine/executor_test.go` — they are unrelated to Issue 06 and reproduce on clean `main`. They are excluded via `-skip` in the test commands above, matching the Issue 04 and Issue 05 precedents.
- **No changes to `PauseRun`.** Pausing a run does not require git cleanup — the user expects to resume from exactly where they paused, including any in-flight uncommitted state. Only `CancelRun` triggers the cleanup protocol.

## 9. Risks and Mitigations

| Risk | Mitigation |
|---|---|
| `CancelCleanup` partial failure leaves the working tree in a weird state (e.g., reset succeeded but checkout failed). | Fail-open semantics + explicit log message with manual-recovery command. The DB is already marked cancelled, so the user can safely run the recovery command without affecting the state machine. |
| Container stop timeouts block cancel for a long time. | Use a 30-second context timeout for the entire container-stop pass. If it expires, log and proceed — leaked containers are recoverable via the next session's startup recovery pass (§22). |
| Scheduler dispatches a new task for the run in the gap between the DB status flip and the container list query. | The DB status flip is atomic and the scheduler's `findReadyTasks` filters on `project_runs.status`, so post-flip dispatches are impossible. The only remaining race is "task in progress when cancel starts", which is exactly what the container stop pass handles. |
| `validRunTransitions` change could accidentally allow other invalid transitions. | Only two specific entries are added; the existing `TestUpdateRunStatus_InvalidTransition` and `TestUpdateRunStatus_TerminalStatesReject` suites still cover the invariants. Add positive tests for the new entries; do not remove anything. |
| `--allow-dirty` is misused for normal development. | Loud `WARN` log on every bypass; CLI flag help text explicitly says "recovery only"; docs reinforce the use case. |

## 10. Reference Architecture Sections

- **§16.2 Base Snapshot Pinning** — motivates why each run has a stable work branch
- **§22 State Management & Crash Recovery** — motivates the fail-open semantics for container/git cleanup
- **§23.1 Branch Strategy** — mandates `axiom/<project-slug>` naming
- **§23.4 Project Completion** — mandates that committed work is preserved on cancel
- **§28.2 Git Hygiene & `.axiom/` Lifecycle** — mandates clean-tree refusal
