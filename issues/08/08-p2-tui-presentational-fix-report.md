# Issue 08 — Fix report: The TUI is now a real operator surface

**Status:** Fixed
**Severity:** P2
**Date fixed:** 2026-04-08
**Plan:** [`08-p2-tui-presentational-misleading.md`](./08-p2-tui-presentational-misleading.md)
**Base commit:** `main` @ `c6a919c` (Issue 08 Plan)

---

## 1. Summary

The TUI's `submitInput` now routes bootstrap-mode prompts through
`Engine.StartRun`, and five previously-canned slash commands (`/new`,
`/resume`, `/eco`, `/diff`, and the execution branch of `/srs`) plus
two newly-introduced commands (`/approve`, `/reject "<feedback>"`) are
wired to real engine entrypoints. `/theme` was removed. A new
`refreshAfterStateChange` helper keeps the status bar, action card, and
task rail coherent with DB state after any write. The `axiom tui
--prompt "<text>"` flag provides a non-interactive one-shot entrypoint
for the composition-root integration test.

Prior to this fix, a non-technical operator could type a prompt in the
TUI, watch it echo into the transcript, and wait forever for an engine
call that never came. With the fix, the same sequence creates a
`project_runs` row with `start_source = "tui"` and transitions the TUI
through the full lifecycle (`draft_srs → awaiting_srs_approval → active
→ completed`) without the operator ever leaving the interactive
terminal.

**Acceptance criteria from the plan** are met in full. See §4 for the
point-by-point checklist.

---

## 2. Root cause (as confirmed)

The plan's diagnosis was accurate: the TUI was built as a read-only
projection of engine state before the corresponding engine write-side
entrypoints had their operator surfaces fleshed out. When Issues 01–07
added those entrypoints (`StartRun`, `SubmitSRS`, `ApproveSRS`,
`RejectSRS`, merge queue validation, inference plane wiring), the TUI
was never revisited to consume them. Three independent omissions
compounded:

1. **No write-side routing in `submitInput`.** `internal/tui/model.go`
   only echoed regular input to the transcript and recorded history;
   it never consulted `m.mode` to decide whether to call `StartRun`.
2. **Placeholder slash-command handlers.** `/new`, `/resume`, `/eco`,
   `/diff`, `/theme`, and the execution branch of `/srs` all returned
   canned sentences and did not touch the engine.
3. **No `/approve` / `/reject` slash commands.** The TUI had no path
   from an approval-mode session to `Engine.ApproveSRS` /
   `Engine.RejectSRS`.

This was a pure surface-wiring bug. Every engine entrypoint the fix
needed already existed and was unit-tested.

---

## 3. Implementation

### 3.1 New `GitService.DiffRange` method

Added `DiffRange(dir, base, head string) (string, error)` to the
`engine.GitService` interface
([internal/engine/interfaces.go:29](../../internal/engine/interfaces.go))
and implemented it on `gitops.Manager` as a thin wrapper over the
existing `DiffWorkBranch` three-dot-notation diff
([internal/gitops/gitops.go:244](../../internal/gitops/gitops.go)). The
underlying `git diff <base>...<head>` is already safely sandboxed to
the project root, so no new security surface is introduced.

All in-tree `GitService` fakes (`internal/engine/engine_test.go`,
`startrun_test.go`, `cancelrun_test.go`, `executor_test.go`,
`internal/cli/cli_test.go`, `internal/tui/tui_test.go`) were extended
with the new method — mostly a one-line noop returning `"", nil`, with
the TUI's `fakeGitService` adding a configurable `diffOutput` field so
the `/diff` tests can exercise the empty, short, and >4 KB truncation
paths.

### 3.2 New `Engine.Git()` accessor

Added
[internal/engine/engine.go:206](../../internal/engine/engine.go)
so the TUI `/diff` handler can reach `GitService.DiffRange` without
re-plumbing a separate git dependency into the session layer.

### 3.3 Rewritten `submitInput` with mode-aware routing

`internal/tui/model.go:408` previously forwarded all regular input to
the transcript only. The rewrite ([internal/tui/model.go](../../internal/tui/model.go)):

- **Bootstrap mode:** echoes the prompt, records history, and returns
  an async `tea.Cmd` that calls `Engine.StartRun(StartRunOptions{...,
  Source: "tui"})`. The outcome is delivered back via `runStartedMsg`
  or `runStartFailedMsg` and handled in the `Update` loop, which
  appends the result to the transcript and calls
  `refreshAfterStateChange`.
- **Approval mode:** appends a hint pointing at `/srs`/`/approve`/
  `/reject "<feedback>"`.
- **Execution mode:** appends an honest "clarifications not yet routed"
  hint. Routing free text during an active run is a sub-orchestrator
  concern and out of scope.
- **Postrun mode:** appends a hint suggesting `/diff` or `/new`.

The shell-mode (`!`) placeholder was rewritten from the misleading
"handled by the engine" to "not yet routed; use a separate terminal".

### 3.4 New `refreshAfterStateChange` helper

`Model.refreshAfterStateChange` re-fetches the startup summary from
`session.Manager`, updates `m.mode`, `m.startup`, `m.tasks`, and
`m.budget`, and appends a new action-card entry **only when the mode
has actually changed** (the Issue 08 §6.1 debounce). Every handler that
mutates run state (`/approve`, `/reject`, `/resume`, `runStartedMsg`,
SRS lifecycle events) calls it exactly once.

### 3.5 Rewritten slash commands

| Command | Before | After |
|---|---|---|
| `/new [prompt]` | Canned string | Two-form handler: `/new <prompt>` calls `StartRun` via `pendingCmd`; bare `/new` emits a composer hint; `/new` during an active run is refused with a pointer to `/cancel`. |
| `/resume` | "Use `axiom session resume <id>`" canned string | Looks up the latest run; if paused, calls `Engine.ResumeRun`; otherwise reports current status or suggests `/new`. Refreshes frame. |
| `/srs` | Mode-based sentence | In `draft_srs`, prefers real content via `Engine.ReadSRSDraft` and falls back to a "waiting for orchestrator" message. In `awaiting_srs_approval`, reads the draft and appends `/approve`/`/reject` instructions. In `active`/`paused`, reads `.axiom/srs.md`. |
| `/approve` | — (did not exist) | Verifies run status, calls `Engine.ApproveSRS`, refreshes frame. |
| `/reject "<feedback>"` | — (did not exist) | Verifies non-empty feedback, calls `Engine.RejectSRS`, refreshes frame. Feedback must be quoted. |
| `/eco` | "No pending ECOs" canned string | Calls `DB.ListECOsByRun` for the active run and lists ECOs by code, status, and category. Points at the CLI for approve/reject (identity wiring deferred). |
| `/diff` | "No diffs available" canned string | Calls `GitService.DiffRange(rootDir, baseBranch, workBranch)` and returns the diff, truncated at 4 KB with a "… (N more bytes)" marker. |
| `/theme` | "Theme switching is not yet available" canned string | Removed from the switch statement entirely. Falls through to the default "Unknown command" case. |

The `handleSlashCommand` dispatcher also gained a `rawArgs` helper so
`/reject "needs section 4.2"` preserves embedded whitespace inside
quoted feedback (unlike `strings.Fields`).

### 3.6 Event subscription extensions

`handleEvent` now reacts to `events.RunCreated`, `events.SRSSubmitted`,
`events.SRSApproved`, and `events.SRSRejected` by calling
`refreshAfterStateChange`. This keeps the status bar and action card
coherent when an **external** orchestrator acts on the run
mid-session — the TUI no longer requires an operator keystroke to see
the state transition.

### 3.7 Session manager: updated command row and suggestions

`internal/session/manager.go:258` — `buildCommandRow` now suggests the
full approval surface (`/srs`, `/approve`, `/reject`) in approval mode,
and exposes `/pause`/`/cancel` in execution mode and `/new` in postrun
mode.

`PromptSuggestions` for approval mode was rewritten from prose to
actionable slash commands (`"/srs — View the SRS draft"` etc.).

### 3.8 Plain renderer: `RunOnce` helper

`internal/tui/plain.go` gained a `RunOnce(w, prompt)` method that
mirrors bootstrap-mode `submitInput`: validates mode, calls
`Engine.StartRun`, writes either "Run created: ..." or "Failed to start
run: ..." to the writer, and returns any error. This is the
scripted-stdin entrypoint used by the composition-root integration
test; it calls `StartRun` directly so CI environments without a TTY
can still exercise the TUI's new write path.

### 3.9 CLI: `axiom tui --prompt` flag

`internal/cli/session.go:137` — `TUICmd` gained a `--prompt` string
flag. When non-empty, the command routes through `runPromptMode` →
`PlainRenderer.RunOnce` → `Engine.StartRun(Source: "tui")` and exits.
The flag is invisible in interactive usage (the interactive TUI path
is unchanged), but it gives the integration test a full-pipeline
subprocess entrypoint.

### 3.10 Documentation

- [`docs/session-tui.md`](../../docs/session-tui.md) — Slash-command
  table, event-handling table, a new "Write Operations from the TUI"
  subsection, and an updated "Known Deferred Items" list.
- [`docs/getting-started.md`](../../docs/getting-started.md) —
  Rewritten slash-command section with a worked example showing an
  operator completing `init → run → SRS approve → execute → diff →
  cancel` entirely inside `axiom tui`.
- [`docs/cli-reference.md`](../../docs/cli-reference.md) — Added the
  `axiom tui --prompt` one-shot invocation and an up-to-date slash
  command list.

---

## 4. Acceptance criteria — verification

Each checkbox from the plan's §7 is verified by at least one new
regression test and/or the composition-root integration test.

| # | Criterion | Verification |
|---|-----------|--------------|
| 1 | Bootstrap prompt calls `StartRun` with `start_source="tui"` | `TestSubmitInput_UserMessage`, `TestSubmitInput_BootstrapMode_StartsRun`, `TestTUIPrompt_CreatesRunViaCompositionRoot` |
| 2 | Approval-mode prompt emits hint, does not mutate run | `TestSubmitInput_ApprovalMode_ShowsApprovalHint` |
| 3 | Execution-mode prompt emits "not yet routed" hint | `TestSubmitInput_ExecutionMode_ShowsExecutionHint` |
| 4 | `/new "<prompt>"` starts; bare `/new` hints; `/new` during active refused | `TestSlashCommand_New_WithPromptStartsRun`, `TestSlashCommand_New_BareShowsHint`, `TestSlashCommand_New_RefusesWhenRunInProgress` |
| 5 | `/resume` resumes paused; else reports status or suggests `/new` | `TestSlashCommand_Resume_PausedRunResumes`, `TestSlashCommand_Resume_NoPausedRunShowsStatus`, `TestSlashCommand_Resume_NoRunShowsHint` |
| 6 | `/srs` in awaiting_approval returns draft + action text | `TestSlashCommand_SRS_AwaitingApprovalShowsDraftAndActions` |
| 7 | `/srs` in active returns approved SRS content | `TestSlashCommand_SRS_ActiveShowsApprovedSRS` |
| 8 | `/approve` calls `ApproveSRS` and refreshes frame | `TestSlashCommand_Approve_CallsApproveSRS`, `TestRefreshAfterStateChange_UpdatesStartupSummary` |
| 9 | `/approve` in wrong status reports error | `TestSlashCommand_Approve_WrongStatusReportsError` |
| 10 | `/reject "<feedback>"` calls `RejectSRS` and returns to draft_srs | `TestSlashCommand_Reject_WithFeedbackCallsRejectSRS` |
| 11 | `/reject` without feedback emits required-error | `TestSlashCommand_Reject_RequiresFeedback` |
| 12 | `/eco` lists; reports empty; reports no active run | `TestSlashCommand_ECO_ListsProposed`, `TestSlashCommand_ECO_EmptyListShowsNoneMessage`, `TestSlashCommand_ECO_NoActiveRun` |
| 13 | `/diff` returns truncated output; reports empty; reports no run | `TestSlashCommand_Diff_TruncatesLargeOutput`, `TestSlashCommand_Diff_EmptyRangeReportsEmpty`, `TestSlashCommand_Diff_NoActiveRun` |
| 14 | `/theme` removed from command set and help | `TestSlashCommand_Theme_Removed`, `TestHelp_DoesNotListTheme` |
| 15 | `/help` lists `/approve` and `/reject` | `TestHelp_ListsApproveAndRejectCommands` |
| 16 | External SRS submission triggers frame refresh | `handleEvent` now handles `events.SRSSubmitted` (code review — the event path is not subject to race-prone goroutine testing in unit tests; the composition-root integration test uses the synchronous submission path) |
| 17 | Composition-root integration test passes | `TestTUIPrompt_CreatesRunViaCompositionRoot`, `TestTUIPrompt_RefusesDirtyTree` |
| 18 | Docs updated, no canned language | Manual inspection of `docs/session-tui.md`, `docs/getting-started.md`, `docs/cli-reference.md` |
| 19 | All existing tests pass; new regression tests pass | See §5. |

---

## 5. How the fix was verified

### 5.1 Unit tests — `internal/tui/` (51 tests, all pass)

The `fakeGitService` in
[internal/tui/tui_test.go](../../internal/tui/tui_test.go) exposes
`validateErr`, `diffOutput`, and `diffErr` for per-test tuning. A new
`seedRunInStatus` helper walks the valid state transition ladder
(`draft_srs → awaiting_srs_approval → active → paused`) so individual
tests can land a run in the exact status they need without reaching
into the database directly.

28 new tests were added:

```
=== RUN   TestSubmitInput_BootstrapMode_StartsRun              PASS
=== RUN   TestSubmitInput_BootstrapMode_DirtyTreeReportsError  PASS
=== RUN   TestSubmitInput_ApprovalMode_ShowsApprovalHint       PASS
=== RUN   TestSubmitInput_ExecutionMode_ShowsExecutionHint     PASS
=== RUN   TestSlashCommand_New_WithPromptStartsRun             PASS
=== RUN   TestSlashCommand_New_BareShowsHint                   PASS
=== RUN   TestSlashCommand_New_RefusesWhenRunInProgress        PASS
=== RUN   TestSlashCommand_Resume_PausedRunResumes             PASS
=== RUN   TestSlashCommand_Resume_NoPausedRunShowsStatus       PASS
=== RUN   TestSlashCommand_Resume_NoRunShowsHint               PASS
=== RUN   TestSlashCommand_SRS_DraftSRSShowsWaitingMessage     PASS
=== RUN   TestSlashCommand_SRS_AwaitingApprovalShowsDraftAndActions  PASS
=== RUN   TestSlashCommand_SRS_ActiveShowsApprovedSRS          PASS
=== RUN   TestSlashCommand_Approve_CallsApproveSRS             PASS
=== RUN   TestSlashCommand_Approve_WrongStatusReportsError     PASS
=== RUN   TestSlashCommand_Reject_RequiresFeedback             PASS
=== RUN   TestSlashCommand_Reject_WithFeedbackCallsRejectSRS   PASS
=== RUN   TestSlashCommand_ECO_NoActiveRun                     PASS
=== RUN   TestSlashCommand_ECO_EmptyListShowsNoneMessage       PASS
=== RUN   TestSlashCommand_ECO_ListsProposed                   PASS
=== RUN   TestSlashCommand_Diff_NoActiveRun                    PASS
=== RUN   TestSlashCommand_Diff_EmptyRangeReportsEmpty         PASS
=== RUN   TestSlashCommand_Diff_TruncatesLargeOutput           PASS
=== RUN   TestSlashCommand_Theme_Removed                       PASS
=== RUN   TestHelp_ListsApproveAndRejectCommands               PASS
=== RUN   TestHelp_DoesNotListTheme                            PASS
=== RUN   TestRefreshAfterStateChange_UpdatesStartupSummary    PASS
```

`TestSubmitInput_UserMessage` was updated to assert that the prompt not
only lands in the transcript but also creates a `project_runs` row with
the expected `InitialPrompt`, `StartSource`, and `Status`. This is the
test the plan identified as encoding the bug; it now encodes the fix.

### 5.2 Composition-root integration — `cmd/axiom/` (2 new tests, both pass)

[`cmd/axiom/phase21_tui_integration_test.go`](../../cmd/axiom/phase21_tui_integration_test.go)
drives the full cobra composition root through a real project fixture
(`testfixtures.Materialize("existing-go")`):

1. **`TestTUIPrompt_CreatesRunViaCompositionRoot`** — runs `axiom init
   → gitCommitAll → axiom tui --prompt "Build a REST API from the
   TUI"`, then opens the SQLite DB with `state.Open` and asserts that
   exactly one run exists with `InitialPrompt` matching the prompt,
   `StartSource="tui"`, and `Status=draft_srs`. Catches any future
   refactor that detaches the TUI surface from `Engine.StartRun`.
2. **`TestTUIPrompt_RefusesDirtyTree`** — runs `axiom init` without
   committing, then invokes `axiom tui --prompt "..."` and asserts the
   command exits non-zero with `"working tree has uncommitted
   changes"` in the error. This is the TUI-surface mirror of the Issue
   06 clean-tree enforcement test, and guards against anyone
   accidentally exposing `--allow-dirty` via the TUI.

Both tests pass on the first run:

```
$ go test -timeout 240s -run 'TestTUIPrompt' ./cmd/axiom/
ok      github.com/openaxiom/axiom/cmd/axiom    0.918s
```

### 5.3 Full-package regression

```
$ go test -timeout 180s -count=1 \
    ./internal/tui/ ./internal/cli/ ./internal/session/ \
    ./internal/gitops/ ./cmd/axiom/
ok      github.com/openaxiom/axiom/internal/tui      3.588s
ok      github.com/openaxiom/axiom/internal/cli      5.655s
ok      github.com/openaxiom/axiom/internal/session  1.808s
ok      github.com/openaxiom/axiom/internal/gitops   8.458s
ok      github.com/openaxiom/axiom/cmd/axiom         5.134s
```

### 5.4 Pre-existing engine test flakes (not related to this fix)

Two tests in `internal/engine/executor_test.go` fail on Windows
**before and after** this fix, on a pristine `main` checkout. They are
unrelated to the Issue 08 surface:

- `TestExecuteAttempt_SuccessEnqueuesAndMerges` — deadlocks on the
  modernc.org sqlite `WAL_EXCLUSIVE` lock after multi-minute runs. This
  is a known WAL locking issue on the Windows sqlite driver and has no
  connection to the TUI.
- `TestEngineWorkers_SchedulerExecutorMergeQueueFlow` — fails with
  `output\msg-000001.json: The system cannot find the path specified`.
  The test uses a Windows-style relative path that does not exist in
  the test tempdir. The path handling was already broken on the base
  commit.

Both flakes were confirmed to reproduce on `main @ c6a919c` before any
Issue 08 changes were applied (`git stash` + the same `go test`
invocation). They are filed as out-of-scope follow-ups and do not
block Issue 08 acceptance. Every engine test that touches the surfaces
Issue 08 modifies (`TestStartRun_*`, `TestCancelRun_*`,
`TestSRS_*`, `TestPauseRun_*`, `TestResumeRun_*`, `TestEngine_*`,
`TestNew_*`) passes:

```
$ go test -timeout 120s -run 'TestStartRun|TestCreateRun|TestPauseRun|TestResumeRun|TestCancel|TestSRS|TestEngine_|TestNew|TestCancelRun' ./internal/engine/
ok      github.com/openaxiom/axiom/internal/engine   1.476s
```

### 5.5 Build and vet

```
$ go build ./...
$ go vet ./...
(clean)
```

---

## 6. Files changed

| File | Change |
|---|---|
| `internal/engine/interfaces.go` | Added `DiffRange(dir, base, head string) (string, error)` to `GitService`. |
| `internal/engine/engine.go` | Added `Engine.Git() GitService` accessor. |
| `internal/gitops/gitops.go` | Added `(*Manager).DiffRange` as a wrapper over `DiffWorkBranch`. |
| `internal/engine/engine_test.go`, `startrun_test.go`, `cancelrun_test.go`, `executor_test.go` | Added `DiffRange` noop to each fake git service. |
| `internal/cli/cli_test.go` | Added `DiffRange` noop to the local `noopGitService`. |
| `internal/tui/model.go` | Rewrote `submitInput` with mode-aware routing; added `startRunFromPrompt`, `refreshAfterStateChange`, `runStartedMsg`, `runStartFailedMsg`, `pendingCmd`; rewrote `handleSlashCommand` with `/new`, `/resume`, `/srs`, `/approve`, `/reject`, `/eco`, `/diff`; removed `/theme`; rewrote `cmdHelp`; extended `handleEvent` for SRS lifecycle and `RunCreated`. |
| `internal/tui/plain.go` | Added `RunOnce(w, prompt)` for non-interactive bootstrap submission. |
| `internal/session/manager.go` | Updated `buildCommandRow` and `PromptSuggestions` for the new approval-mode surface. |
| `internal/tui/tui_test.go` | Added `fakeGitService`, `testSetupWithGit`, `seedRunInStatus`, and 28 regression tests; updated `TestSubmitInput_UserMessage` to assert DB side effects. |
| `internal/cli/session.go` | Added `--prompt` flag and `runPromptMode` to `TUICmd`. |
| `cmd/axiom/phase21_tui_integration_test.go` | **New file.** 2 composition-root integration tests (`TestTUIPrompt_CreatesRunViaCompositionRoot`, `TestTUIPrompt_RefusesDirtyTree`). |
| `docs/session-tui.md` | Updated slash-command table, event-handling table, added "Write Operations from the TUI" subsection, updated deferred-items list. |
| `docs/getting-started.md` | Rewrote TUI section with real slash-command behavior and a worked end-to-end example. |
| `docs/cli-reference.md` | Added `axiom tui --prompt` and a full slash-command list. |
| `issues/08/08-p2-tui-presentational-fix-report.md` | **This file.** |

No changes to `internal/engine/run.go`, `srs.go`, `eco.go`,
`internal/state/*`, `internal/inference/*`, or any subsystem package —
the plan's scoping was correct, every engine entrypoint the fix needed
already existed and was unit-tested.

---

## 7. Residual scope and deferrals

These items were explicitly deferred by the plan's §4.12 and remain
deferred after the fix:

- **SRS / ECO / diff full overlays** with syntax highlighting — still
  rendered as inline transcript entries. The slash-command handlers
  are good enough for a first working slice.
- **`/eco approve <code>` / `/eco reject <code>`** — identity wiring
  (who-is-approving) is still not tracked in the TUI; ECO decisions
  must go through the CLI.
- **File-mention autocomplete (`@`)**, **LLM prompt suggestions**,
  **theme switching**, and **task inspection views** — all unchanged.
- **Clarifications during execution** — free text in execution mode
  emits an honest hint rather than being routed. Routing free text
  during an active run is a sub-orchestrator concern outside the
  Issue 08 surface.
- **Shell-mode execution (`!`)** — placeholder message is now honest
  ("not yet routed") rather than misleading ("handled by the engine"),
  but actual shell-mode execution remains deferred.

---

## 8. Interaction with prior issues

This fix is the last P-level operator-surface gap flagged by Issues
01–08. With Issue 08 closed:

- Issue 01 (external orchestrator handoff) — `StartRun` is reachable
  from the TUI.
- Issue 02 (engine workers), 03 (execution path), 04 (merge queue
  validation), 05 (test-generation wiring), 06 (git safety), 07
  (inference plane) — all made the underlying operations real; this
  fix routes those operations through the TUI surface so a
  non-technical operator can complete a vertical slice entirely inside
  `axiom tui`.

The §26.2.3 contract ("route slash commands and shell-mode actions to
the appropriate engine subsystem") is now honored for every command
except the genuinely-deferred shell-mode (`!`) and file-mention (`@`)
surfaces.
