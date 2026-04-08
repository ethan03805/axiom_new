# Issue 08 — P2: The TUI/session layer is mostly presentational and can mislead operators

**Status:** Open
**Severity:** P2
**Date opened:** 2026-04-08
**Source:** `issues.md` §8
**Base commit:** `main` @ `8477421`

---

## 1. Issue (as reported)

> The TUI/session layer is mostly presentational and can mislead operators.
>
> - `internal/tui/model.go:357-386` shows that regular input only appends to transcript/history; it does not create runs, submit prompts, or call any orchestrator entrypoint.
> - `internal/tui/model.go:397-424` has several slash commands that return canned strings rather than performing real actions (`/new`, `/resume`, `/eco`, `/diff`, `/theme`).
> - `docs/getting-started.md:321-360` presents the TUI as an operator-facing execution surface with slash commands and session continuity.

**Why this matters:** For a non-technical user, the TUI looks like the obvious primary interface. Today it behaves more like a mock shell over state projections than a real control surface. Because Issues 01–07 already closed the run lifecycle, engine start, execution path, merge validation, test generation, git safety, and inference plane gaps, the TUI is now the *last* layer still blocking a non-technical user from completing a vertical slice (`init → run → SRS approve → execute → merge`) entirely from inside the interactive terminal.

**Architecture sections affected:** §26.2.3 (Session UX Manager), §26.2.6 (Input Model & Quick Commands), §26.2.10 (Event Model), §27.1 (Interactive Session Commands).

**Implementation plan phases affected:** Phase 15 (Session UX Manager and Bubble Tea TUI).

**Docs affected:** `docs/getting-started.md`, `docs/session-tui.md`.

---

## 2. Recreation

The defect is fully observable in the source tree; no live run is required. The same facts can also be confirmed by reading the existing tests in `internal/tui/tui_test.go`, which assert only that the transcript is mutated — never that any engine lifecycle is called.

### 2.1 Regular input is swallowed (no run is ever created)

`internal/tui/model.go:357-387`:

```go
func (m *Model) submitInput(input string) tea.Cmd {
    if strings.HasPrefix(input, "/") {
        result := m.handleSlashCommand(input)
        if result != "" {
            m.appendTranscript("system", "system_card", result)
        }
        return nil
    }

    if strings.HasPrefix(input, "!") {
        shellCmd := strings.TrimPrefix(input, "!")
        m.appendTranscript("user", "shell", "$ "+shellCmd)
        m.appendTranscript("system", "event", "Shell execution is handled by the engine.")
        return nil
    }

    // Regular user message
    m.appendTranscript("user", "user", input)

    // Record in input history
    if m.sess != nil {
        _ = m.session.RecordInput(m.projectID, m.sess.ID, "prompt", input)
    }

    // Prepend to local history for up-arrow recall
    m.inputHistory = append([]string{input}, m.inputHistory...)
    m.historyIdx = -1

    return nil
}
```

When the user is in **bootstrap** mode and types `Build a REST API with JWT auth`, `submitInput`:
1. Writes the text to the transcript as a `user` message.
2. Records it in `ui_input_history` for up-arrow recall.
3. Returns.

It does **not** call `Engine.StartRun`, does not validate a clean working tree, does not create a `project_runs` row, does not switch the repo to `axiom/<slug>`, and does not emit any lifecycle event. The user sees their message appear in the transcript and then — **nothing**. Because the action card still reads "Describe what you want to build." and because task/budget counters still show the empty state, the only signal that nothing happened is that the mode label never leaves `BOOTSTRAP`.

In **approval** mode the same path is even more misleading: the user may legitimately think they are typing approval feedback (the mode label says `APPROVAL` and the action card says "Review the SRS and approve or reject it") — but the text is again only stored in the transcript. The engine never sees it. The SRS is never rejected, never approved, and never annotated.

The existing test `TestSubmitInput_UserMessage` at `internal/tui/tui_test.go:337-354` asserts *exactly this behavior*: after calling `m.submitInput("Build me an API")`, the test only checks that the transcript contains the user message. There is no engine assertion at all. The test therefore *encodes* the bug: it will keep passing after the fix only because the transcript echo behavior is kept, but it does not guard anything useful about engine wiring.

### 2.2 Slash commands return canned strings that impersonate real actions

`internal/tui/model.go:397-428`:

```go
switch command {
case "/status":    return m.cmdStatus()    // real
case "/help":      return m.cmdHelp()      // real
case "/tasks":     return m.cmdTasks()     // real
case "/budget":    return m.cmdBudget()    // real
case "/clear":     m.transcript = nil; return ""  // real
case "/new":       return "Start a new session by describing what you want to build."  // CANNED
case "/resume":    return "Use 'axiom session resume <id>' to resume a specific session."  // CANNED
case "/pause":     return m.cmdPause()     // real
case "/cancel":    return m.cmdCancel()    // real
case "/srs":       return m.cmdSRS()       // PARTIALLY CANNED — see §2.3
case "/eco":       return "ECO review: No pending ECOs."  // CANNED (ignores ListECOsByRun)
case "/diff":      return "No diffs available. Run tasks to generate changes."  // CANNED
case "/theme":     return "Theme switching is not yet available."  // CANNED (deferred)
default:           return fmt.Sprintf("Unknown command: %s. ...", command)
}
```

Five of the twelve declared commands are canned:

- **`/new`** claims to "start a new session by describing what you want to build" but provides no mechanism for describing it. The user has no way to distinguish "this is an instruction" from "this is a completed action". A naive user who types `/new` and then a prompt will experience the §2.1 bug (nothing happens).
- **`/resume`** tells the user to leave the TUI and run `axiom session resume <id>` — which is exactly the opposite of the TUI's documented purpose as an operator surface with session continuity (`docs/getting-started.md:321-360`).
- **`/eco`** unconditionally returns "No pending ECOs." even when `db.ListECOsByRun(runID)` would show proposed or approved ECOs. `Engine.ProposeECO`, `Engine.ApproveECO`, and `Engine.RejectECO` all exist (`internal/engine/eco.go:24, 89, 135`) and `DB.ListECOsByRun` exists (`internal/state/eco.go:45`). The slash command simply bypasses all of them and lies.
- **`/diff`** claims "No diffs available. Run tasks to generate changes." — even after tasks have merged. Issue 04's fix means the merge queue now actually writes to the work branch, so `git diff <base-branch>...<work-branch>` in `.rootDir` would return real content, but the TUI never calls anything to compute a diff.
- **`/theme`** claims theme switching "is not yet available" — which is technically true but phrased as a feature that is being worked on. In reality the TUI has exactly one hardcoded theme in `internal/tui/styles.go`.

### 2.3 `/srs` returns a status sentence instead of the draft + an approval action

`internal/tui/model.go:552-561`:

```go
func (m *Model) cmdSRS() string {
    switch m.mode {
    case state.SessionApproval:
        return "SRS is awaiting your review. Approve or reject."
    case state.SessionExecution, state.SessionPostrun:
        return "SRS has been approved and is locked."
    default:
        return "No SRS yet. Start a run to generate one."
    }
}
```

In approval mode the user is told the SRS "is awaiting your review. Approve or reject." — but neither the review content nor an approval action is provided:

- `Engine.ReadSRSDraft(runID)` (`internal/engine/srs.go:13`) exists and returns the draft markdown, but the TUI never calls it.
- `Engine.ApproveSRS` / `Engine.RejectSRS` (`internal/engine/srs.go:58, 108`) exist and are already invoked by the CLI `axiom srs approve` / `axiom srs reject` commands (`internal/cli/srs.go:101, 132`), but the TUI has no `/approve` or `/reject` slash command — and regular text typed in approval mode is silently swallowed per §2.1.

The net effect for a non-technical user in approval mode: there is **no way at all** to move the run forward from inside the TUI. They have to exit the TUI and run `axiom srs show && axiom srs approve` from a separate shell. This is the exact failure mode that §26.2.3 forbids: "Route slash commands and shell-mode actions to the appropriate engine subsystem."

### 2.4 Symptoms are not caught by existing tests

`internal/tui/tui_test.go` has 29 tests but none of them assert engine effects. Specifically:

- `TestSubmitInput_UserMessage` asserts only that the transcript contains the user message.
- `TestSubmitInput_SlashCommand` asserts only that `/help` produces output containing `/status`.
- `TestSlashCommand_*` tests for `/status`, `/help`, `/tasks`, `/budget`, `/clear` exist and assert non-empty output.
- **No** test exists for `/new`, `/resume`, `/eco`, `/diff`, `/theme`, or `/srs`.
- **No** test calls `Engine.GetRunStatus` after `submitInput("Build ...")` to verify a run was created.
- **No** test verifies that `/approve`/`/reject` slash commands exist or work.

The test file therefore did not catch the bug when the TUI shipped and will not catch regressions after the fix unless new tests are added. See §4.9 for the required regressions.

### 2.5 Architecture versus reality

Architecture §26.2.3 requires that the Session UX Manager (and therefore the TUI that consumes it) SHALL:

> "Route slash commands and shell-mode actions to the appropriate engine subsystem."

Architecture §26.2.6 requires an initial slash-command set that includes `/new`, `/resume`, `/srs`, `/eco`, `/diff`, `/theme`. The current TUI *declares* these commands but routes none of `/new`, `/resume`, `/eco`, `/diff`, `/theme` and routes `/srs` only to a placeholder sentence.

Architecture §27.1 lists `axiom` (bare invocation) and `axiom tui` as "launch the interactive TUI in the current project, starting or resuming the most relevant session" — language that implies operator parity with the rest of the CLI surface.

**The current implementation violates §26.2.3 directly and §26.2.6 and §27.1 indirectly.**

---

## 3. Root cause

**The TUI was built as a read-only projection of engine state before the corresponding engine write-side entrypoints had their operator surfaces fleshed out. When Issues 01–07 added those entrypoints (`StartRun`, `SubmitSRS`, `ApproveSRS`, `RejectSRS`, merge queue validation, inference plane wiring), the TUI was never revisited to consume them. The placeholder slash-command handlers and the pass-through `submitInput` were left in place as if they were "deferred — wire later," but nothing tracked them and they were never retired.**

Concretely, three independent omissions compound:

1. **No write-side routing in `submitInput`.** The function knows about three prefixes (`/`, `!`, and "everything else") but dispatches everything-else only to the local transcript and input-history tables. It never consults `m.mode` to decide whether the input is a bootstrap prompt (→ `StartRun`), approval feedback (→ hint towards `/approve`//`/reject`), execution-mode clarification (explicitly deferred), or post-run follow-up.

2. **Placeholder slash-command handlers.** `/new`, `/resume`, `/eco`, `/diff`, `/theme`, and the execution branch of `/srs` all return canned sentences. None of them inspect engine state, and none of them emit any kind of deferral marker distinguishing "this feature is genuinely deferred" (`/theme`) from "this feature is implemented in the engine but not wired here" (`/eco`, `/diff`, `/srs` approval flow, `/resume`, `/new`).

3. **No `/approve` / `/reject` slash commands.** The architecture's §26.2.6 command list does not enumerate `/approve`/`/reject` explicitly (the expectation is that the SRS/ECO review surface provides an in-overlay button), but because the TUI has no SRS overlay either, there is literally no path from an approval-mode session to `Engine.ApproveSRS` or `Engine.RejectSRS`. The CLI has these paths (`axiom srs approve` / `axiom srs reject --feedback "..."`); the TUI does not.

The result is a TUI that:

- Correctly **reads** engine state via `session.Manager.StartupSummary`, `engine.GetRunStatus`, and the event bus (`events.TaskProjectionUpdated`, `events.ApprovalRequested`, `events.DiffPreviewReady`).
- Correctly renders that state in the status bar, task rail, action card, and transcript.
- Correctly wires a subset of write operations: `/pause` → `Engine.PauseRun`, `/cancel` → `Engine.CancelRun`, `Ctrl+C` → `tea.Quit`.
- **Does not** wire the most important write operation (starting a run), **does not** wire any of the approval-mode actions (approve/reject SRS), and **fakes** three read operations (`/eco`, `/diff`, the execution branch of `/srs`).

This is a surface-wiring bug, not a design or subsystem bug. Every engine entrypoint the fix needs already exists and is unit-tested.

---

## 4. Plan to fix

The fix is scoped to `internal/tui/` plus documentation and tests. No changes are required to `internal/engine/`, `internal/session/`, `internal/state/`, or any other package.

### 4.1 Route regular input through the engine lifecycle

Rewrite `submitInput` in `internal/tui/model.go` to dispatch on `m.mode`:

```go
func (m *Model) submitInput(input string) tea.Cmd {
    if strings.HasPrefix(input, "/") {
        result := m.handleSlashCommand(input)
        if result != "" {
            m.appendTranscript("system", "system_card", result)
        }
        return nil
    }

    if strings.HasPrefix(input, "!") {
        shellCmd := strings.TrimPrefix(input, "!")
        m.appendTranscript("user", "shell", "$ "+shellCmd)
        m.appendTranscript("system", "event", "Shell execution is not yet routed; use a separate terminal.")
        return nil
    }

    // Echo user message + record history (always).
    m.appendTranscript("user", "user", input)
    if m.sess != nil {
        _ = m.session.RecordInput(m.projectID, m.sess.ID, "prompt", input)
    }
    m.inputHistory = append([]string{input}, m.inputHistory...)
    m.historyIdx = -1

    // Dispatch by mode — this is the §26.2.3 "route to engine subsystem" contract.
    switch m.mode {
    case state.SessionBootstrap:
        return m.startRunFromPrompt(input)
    case state.SessionApproval:
        m.appendTranscript("system", "system_card",
            "To act on the SRS, use /srs to view the draft, then /approve or /reject \"feedback\".")
        return nil
    case state.SessionExecution:
        m.appendTranscript("system", "ephemeral",
            "User clarifications during execution are not yet routed to the orchestrator. "+
                "Use /status, /tasks, /pause, or /cancel to observe or control the run.")
        return nil
    case state.SessionPostrun:
        m.appendTranscript("system", "ephemeral",
            "The run is complete. Use /diff to review changes, or start a new run with /new \"<prompt>\".")
        return nil
    default:
        return nil
    }
}
```

`startRunFromPrompt` is a new private helper:

```go
func (m *Model) startRunFromPrompt(prompt string) tea.Cmd {
    return func() tea.Msg {
        run, err := m.engine.StartRun(engine.StartRunOptions{
            ProjectID:  m.projectID,
            Prompt:     prompt,
            BaseBranch: "main",
            Source:     "tui",
        })
        if err != nil {
            return runStartFailedMsg{err: err}
        }
        return runStartedMsg{run: run}
    }
}
```

Plus two new message types handled in `Update`:

```go
type runStartedMsg struct{ run *state.ProjectRun }
type runStartFailedMsg struct{ err error }
```

`Update` handles them by appending the outcome to the transcript and refreshing the mode (`m.mode = m.session.DetermineMode(m.projectID)`) so the status bar and action card update immediately. On success the card reads "Run created: <id[:8]> on branch <branch>. Waiting for external orchestrator to submit SRS draft." On failure the card reads "Failed to start run: <error>. Commit or stash changes and try again." — matching the CLI output format in `internal/cli/run.go:60-67`.

**Important — clean-tree requirement.** `Engine.StartRun` enforces the Architecture §28.2 clean-tree requirement by default. If the working tree is dirty, the TUI must surface the error without silently swallowing it *and* must not offer an in-TUI `--allow-dirty` bypass. Dirty-tree recovery remains a `--allow-dirty` CLI-only operation. This matches the pattern used by Issue 06 and prevents a non-technical user from accidentally mixing pre-existing edits with Axiom lifecycle state.

### 4.2 Wire `/new [prompt]` to `StartRun`

Replace the canned `/new` handler with a function that:

1. If the current mode is not `bootstrap`, responds "A run is already in progress; use /cancel first to start a new one." and returns (no engine call).
2. If the user supplied inline arguments (`/new Build a REST API with JWT auth`), treats the remainder after `/new ` as the prompt and calls `m.startRunFromPrompt(prompt)` directly.
3. If the user typed bare `/new` with no arguments, appends a hint "Type your prompt below and press Enter." and returns (no engine call). The bare form is equivalent to "clear the composer and switch to bootstrap" — which is already the current state if the check in step 1 passed.

This gives operators two explicit ways to start a run (type `/new <prompt>` on one line, or just type `<prompt>` on one line) and matches architecture §26.2.6's "`/new` — start a new bootstrap session" requirement.

### 4.3 Wire `/resume` to `Engine.ResumeRun`

Replace the canned `/resume` with a handler that:

1. Looks up the most recent run for the project via `m.engine.DB().GetLatestRunByProject(m.projectID)`.
2. If that run is in `paused` status, calls `m.engine.ResumeRun(run.ID)` and appends "Run <id[:8]> resumed." to the transcript. Refresh mode.
3. If the run exists but is not paused, reports its current status: "No paused run to resume. Latest run is <id[:8]> (<status>)."
4. If no run exists, reports "No runs found for this project. Use /new \"<prompt>\" to start one."
5. On any engine error, surfaces it as a transcript error entry.

This intentionally does **not** implement cross-session resume (`axiom session resume <id>`) — that is a different operation. The TUI's `/resume` means "resume the paused run in this project," mirroring `axiom resume` at `internal/cli/run.go:107-141`. The help text and `docs/session-tui.md` must be updated to clarify this.

### 4.4 Wire `/srs` + add `/approve` and `/reject "<feedback>"` slash commands

Replace `cmdSRS` with a function that inspects mode and run status, and displays the draft when one exists:

```go
func (m *Model) cmdSRS() string {
    run, err := m.engine.DB().GetActiveRun(m.projectID)
    if err != nil {
        return "No active run. Use /new \"<prompt>\" to start one."
    }

    switch run.Status {
    case state.RunDraftSRS:
        draft, err := m.engine.ReadSRSDraft(run.ID)
        if err != nil {
            return "Run " + run.ID[:8] + " is in draft_srs. " +
                "Waiting for external orchestrator to submit an SRS draft."
        }
        return "--- SRS Draft (not yet submitted for approval) ---\n" + draft

    case state.RunAwaitingSRSApproval:
        draft, err := m.engine.ReadSRSDraft(run.ID)
        if err != nil {
            return "SRS draft is awaiting approval but the draft file was not found. " +
                "Check .axiom/drafts/ or use 'axiom srs show' for diagnostics."
        }
        return "--- SRS Draft (awaiting approval) ---\n" + draft +
            "\n\nUse /approve or /reject \"<feedback>\" to decide."

    case state.RunActive, state.RunPaused:
        // Read the finalized SRS if it exists on disk.
        data, err := os.ReadFile(filepath.Join(m.engine.RootDir(), ".axiom", "srs.md"))
        if err != nil {
            return "SRS has been approved and is locked (file not readable here)."
        }
        return "--- Approved SRS ---\n" + string(data)

    default:
        return "No SRS available for run status " + string(run.Status) + "."
    }
}
```

Add two new slash commands:

```go
case "/approve":
    return m.cmdApproveSRS()
case "/reject":
    return m.cmdRejectSRS(parts[1:])
```

`cmdApproveSRS`:

```go
func (m *Model) cmdApproveSRS() string {
    run, err := m.engine.DB().GetActiveRun(m.projectID)
    if err != nil {
        return "No active run to approve."
    }
    if run.Status != state.RunAwaitingSRSApproval {
        return "Cannot approve: run is in " + string(run.Status) +
            " (must be awaiting_srs_approval)."
    }
    if err := m.engine.ApproveSRS(run.ID); err != nil {
        return "Failed to approve SRS: " + err.Error()
    }
    // Refresh mode so the status bar and action card update immediately.
    m.mode = m.session.DetermineMode(m.projectID)
    return "SRS approved. Run " + run.ID[:8] + " is now active."
}
```

`cmdRejectSRS(args []string)`:

```go
func (m *Model) cmdRejectSRS(args []string) string {
    if len(args) == 0 {
        return "Reject requires feedback. Usage: /reject \"Your feedback here\""
    }
    feedback := strings.TrimSpace(strings.Trim(strings.Join(args, " "), "\""))
    if feedback == "" {
        return "Reject requires non-empty feedback."
    }
    run, err := m.engine.DB().GetActiveRun(m.projectID)
    if err != nil {
        return "No active run to reject."
    }
    if run.Status != state.RunAwaitingSRSApproval {
        return "Cannot reject: run is in " + string(run.Status) +
            " (must be awaiting_srs_approval)."
    }
    if err := m.engine.RejectSRS(run.ID, feedback); err != nil {
        return "Failed to reject SRS: " + err.Error()
    }
    m.mode = m.session.DetermineMode(m.projectID)
    return "SRS rejected. Run " + run.ID[:8] + " returned to draft_srs for revision."
}
```

Update `cmdHelp` to list `/approve` and `/reject "<feedback>"` under the slash-command table, grouped with the existing SRS commands.

Update `session.Manager.buildCommandRow` (`internal/session/manager.go:259-273`) so that approval mode suggests `/srs`, `/approve`, `/reject`, `/status`, `/help` instead of just `/srs`, `/status`, `/help`. Update `PromptSuggestions` (`internal/session/manager.go:419-449`) similarly.

### 4.5 Wire `/eco` to `DB.ListECOsByRun`

Replace the canned `/eco` handler with a real listing:

```go
func (m *Model) cmdECO() string {
    run, err := m.engine.DB().GetActiveRun(m.projectID)
    if err != nil {
        return "No active run. ECOs are scoped to an active or paused run."
    }
    entries, err := m.engine.DB().ListECOsByRun(run.ID)
    if err != nil {
        return "Failed to list ECOs: " + err.Error()
    }
    if len(entries) == 0 {
        return "No ECOs proposed for run " + run.ID[:8] + "."
    }
    var b strings.Builder
    fmt.Fprintf(&b, "ECOs for run %s:\n", run.ID[:8])
    for _, e := range entries {
        fmt.Fprintf(&b, "  %s  %s  %s\n", e.ECOCode, e.Status, e.Category)
    }
    b.WriteString("\nUse 'axiom eco approve <code>' or 'axiom eco reject <code>' from the CLI to decide.")
    return b.String()
}
```

The ECO approval/rejection slash-command flow is **explicitly deferred** to a follow-up — this fix does not add `/eco approve <code>`. The reason is that `Engine.ApproveECO` requires an `approvedBy` identity string that the TUI does not currently track, and identity wiring is out of scope. The CLI pointer in the last line makes the CLI path discoverable without pretending the TUI already supports it.

### 4.6 `/diff` — minimal real implementation via work-branch diff

Replace the canned `/diff` with a handler that:

1. Looks up the active run to get `run.BaseBranch` and `run.WorkBranch`.
2. Invokes `m.engine.Git()` (which already implements `GitService` — see `internal/engine/interfaces.go`) to compute the diff. A new method `DiffRange(dir, base, head string) (string, error)` on `GitService` is added. The underlying `gitops.Manager` implements it as `git diff <base>..<head>`.
3. If the diff is non-empty, displays a **truncated** preview (first 4 KB, with a trailing "…(N more bytes)" marker) in the transcript. Full diff viewing is explicitly deferred to the diff-preview overlay described in `docs/session-tui.md:357`.
4. If empty or an error occurs, reports it honestly: "No diff between <base> and <head>" or "Failed to compute diff: <error>".

```go
func (m *Model) cmdDiff() string {
    run, err := m.engine.DB().GetActiveRun(m.projectID)
    if err != nil {
        return "No active run. Use /new \"<prompt>\" to start one."
    }
    diff, err := m.engine.Git().DiffRange(m.engine.RootDir(), run.BaseBranch, run.WorkBranch)
    if err != nil {
        return "Failed to compute diff: " + err.Error()
    }
    if diff == "" {
        return fmt.Sprintf("No diff between %s and %s.", run.BaseBranch, run.WorkBranch)
    }
    const maxBytes = 4096
    if len(diff) > maxBytes {
        return fmt.Sprintf("--- Diff %s..%s (truncated) ---\n%s\n… (%d more bytes — use the diff overlay for the full view)",
            run.BaseBranch, run.WorkBranch, diff[:maxBytes], len(diff)-maxBytes)
    }
    return fmt.Sprintf("--- Diff %s..%s ---\n%s", run.BaseBranch, run.WorkBranch, diff)
}
```

**Scope note:** `GitService` currently has `ValidateClean`, `SetupWorkBranch`, and `SetupWorkBranchAllowDirty`. Adding `DiffRange` is a one-line interface extension plus a one-function implementation in `internal/gitops/gitops.go`. The underlying `git diff` is already safely sandboxed to the project root, so no new security surface is introduced. If the reviewer prefers to keep `GitService` minimal, the alternative is to move `DiffRange` to a new `GitReader` interface — but given that `GitService` already exposes write operations, the lighter change is to add it there.

### 4.7 `/theme` — honest deferral marker

Replace the canned "Theme switching is not yet available." with a clearer deferral marker:

```go
case "/theme":
    return "Theme switching is deferred. Only the default 'axiom' theme is currently available. " +
        "Follow https://github.com/openaxiom/axiom/issues/… for progress."
```

This is not a regression — it is the same information, phrased so the user knows it is a genuine "deferred to a future release" status rather than a feature that is being actively worked on. The URL placeholder is replaced with the actual tracking issue or removed.

Alternatively, `/theme` can be **removed from the command set entirely** and dropped from `cmdHelp`. This is the cleaner option and the one we recommend. Architecture §26.2.6 lists `/theme` as part of the "initial slash-command set," but the architecture language says commands SHALL be available — not that placeholder commands must exist. Since the command does not do anything, removing it is less misleading than keeping a "deferred" sentence.

**Recommendation:** remove `/theme` from the slash-command set and the help page, and update `docs/session-tui.md:356` to move "Theme switching" from "Known Deferred Items" with placeholder command to "Known Deferred Items" with "command not yet exposed."

### 4.8 Help text, suggestions, and action-card refresh on mode transitions

1. Update `cmdHelp` to:
   - Remove `/theme` (if §4.7 removal option is taken).
   - Add `/approve` and `/reject "<feedback>"`.
   - Group commands by mode (Bootstrap: `/new`; Approval: `/srs`, `/approve`, `/reject`; Execution: `/tasks`, `/diff`, `/budget`, `/pause`, `/cancel`; Postrun: `/diff`, `/resume`, `/new`; Always: `/status`, `/help`, `/clear`).

2. Update `session.Manager.buildCommandRow` and `PromptSuggestions` to mirror the new command set per mode.

3. After any mode-changing slash command (`/approve`, `/reject`, `/cancel`, `/pause`, `/new`, successful `startRunFromPrompt`), the handler must refresh `m.mode` via `m.session.DetermineMode(m.projectID)` and re-fetch the startup summary (or a slimmer refresh helper) so the status bar, action card, and task rail update immediately. Currently only `/cancel` does this partially; the fix must make the pattern consistent.

   The cleanest way is to introduce a private `m.refreshAfterStateChange()` helper that:
   - Sets `m.mode = m.session.DetermineMode(m.projectID)`.
   - Fetches a fresh `StartupSummary` and updates `m.startup`, `m.tasks`, `m.budget`.
   - Appends a new system-card transcript entry reflecting the updated action card.
   
   Call it from every handler that mutates run state. This pattern also lets us add a single regression test instead of testing each slash command's refresh behavior independently.

### 4.9 Tests

Add the following regression tests to `internal/tui/tui_test.go`. All of them extend the existing `testSetup` harness, which already wires a real `engine.Engine` with a noop git service — we add a fake git service that returns a clean tree and a non-empty `DiffRange` where needed.

| Test | Asserts |
|---|---|
| `TestSubmitInput_BootstrapMode_StartsRun` | Typing a regular prompt in bootstrap mode creates a `project_runs` row via `StartRun` and refreshes `m.mode` to `bootstrap` (still — mode stays in bootstrap until SRS draft is submitted) but the transcript shows the run ID. |
| `TestSubmitInput_BootstrapMode_DirtyTreeReportsError` | With a fake git service that returns a dirty-tree error, `submitInput` appends an error system card and does NOT create a run. |
| `TestSubmitInput_ApprovalMode_ShowsApprovalHint` | With a run in `awaiting_srs_approval`, regular text input produces a hint pointing at `/srs`/`/approve`/`/reject` — and does not mutate the run. |
| `TestSubmitInput_ExecutionMode_ShowsExecutionHint` | With a run in `active`, regular text input produces a clarifications-not-routed hint. |
| `TestSlashCommand_New_WithPromptStartsRun` | `/new Build a REST API` creates a run. |
| `TestSlashCommand_New_BareShowsHint` | Bare `/new` emits a hint and does not create a run. |
| `TestSlashCommand_New_RefusesWhenRunInProgress` | `/new` when a run already exists emits the refusal message. |
| `TestSlashCommand_Resume_PausedRunResumes` | With a paused run, `/resume` calls `ResumeRun` and the run status transitions to `active`. |
| `TestSlashCommand_Resume_NoPausedRunShowsStatus` | With a non-paused latest run, `/resume` reports its current status rather than calling ResumeRun. |
| `TestSlashCommand_Resume_NoRunShowsHint` | With no runs, `/resume` suggests `/new`. |
| `TestSlashCommand_SRS_DraftSRSShowsWaitingMessage` | In draft_srs, `/srs` reports waiting for external orchestrator. |
| `TestSlashCommand_SRS_AwaitingApprovalShowsDraftAndActions` | In awaiting_srs_approval with a draft on disk, `/srs` returns draft content *and* the "Use /approve or /reject" instructions. |
| `TestSlashCommand_SRS_ActiveShowsApprovedSRS` | In active with `.axiom/srs.md` on disk, `/srs` returns its contents. |
| `TestSlashCommand_Approve_CallsApproveSRS` | `/approve` in awaiting_srs_approval transitions the run to active and refreshes `m.mode` to execution. |
| `TestSlashCommand_Approve_WrongStatusReportsError` | `/approve` in any other status reports the error and does not call `ApproveSRS`. |
| `TestSlashCommand_Reject_RequiresFeedback` | `/reject` with no arguments emits the "feedback required" error. |
| `TestSlashCommand_Reject_WithFeedbackCallsRejectSRS` | `/reject "needs section 4.2"` transitions the run back to draft_srs and records the feedback via the event. |
| `TestSlashCommand_ECO_NoActiveRun` | `/eco` with no active run reports the error. |
| `TestSlashCommand_ECO_EmptyListShowsNoneMessage` | `/eco` with an active run and no proposed ECOs reports "No ECOs proposed". |
| `TestSlashCommand_ECO_ListsProposed` | `/eco` with one proposed ECO lists it by code, status, and category. |
| `TestSlashCommand_Diff_NoActiveRun` | `/diff` with no active run reports the error. |
| `TestSlashCommand_Diff_EmptyRangeReportsEmpty` | `/diff` with a fake git service that returns an empty diff reports "No diff between ..." |
| `TestSlashCommand_Diff_TruncatesLargeOutput` | `/diff` with a fake git service that returns > 4 KB of output truncates and appends the "N more bytes" marker. |
| `TestSlashCommand_Theme_Removed` | `/theme` returns the "Unknown command" response (confirming §4.7 removal option is taken). |
| `TestHelp_ListsApproveAndRejectCommands` | `/help` output includes `/approve` and `/reject`. |
| `TestHelp_DoesNotListTheme` | `/help` output does NOT include `/theme`. |
| `TestRefreshAfterStateChange_UpdatesStartupSummary` | After `/approve`, `m.startup.ActionCard` changes from the approval card to the execution card. |

Additionally, update the existing `TestSubmitInput_UserMessage` test to assert that when in bootstrap mode, a `project_runs` row is created after `submitInput`, not just that the transcript has the message.

### 4.10 Integration test in `cmd/axiom/`

Add a single end-to-end test in `cmd/axiom/phase20_integration_test.go` (or a new `phase21_tui_integration_test.go`) that:

1. Initializes a project fixture (via the existing `initCmd()` helper).
2. Commits the fixture to satisfy the clean-tree requirement.
3. Invokes `axiom tui --plain` with a scripted stdin containing a prompt followed by Ctrl+D.
4. Asserts that the exit is clean and the output contains "Run created".
5. Queries the DB directly (via `state.Open`) to assert that a `project_runs` row exists with `initial_prompt` matching the scripted prompt and `start_source = "tui"`.

The scripted-stdin approach works because the `--plain` renderer already supports non-TTY stdin handling in `internal/tui/plain.go`. If the plain renderer's current behavior is "render startup frame and exit," then this fix also extends the plain renderer to accept a prompt on stdin and call `startRunFromPrompt` once. That extension is in scope because without it, the plain fallback in CI cannot exercise the new write path.

**Scope note:** The integration test requires `axiom tui --plain` to support a non-interactive "one prompt in, one result out" mode. If the reviewer prefers to defer the plain-mode stdin extension, the integration test can call the `Model.submitInput` method directly via an exported `tui.RunOnce` helper and skip the subprocess. We recommend the subprocess version because it catches composition-root regressions that unit tests cannot.

### 4.11 Docs updates

- **`docs/session-tui.md`**
  - Update the slash-command table (lines 167-184) to:
    - Remove `/theme` (per §4.7).
    - Add `/approve` and `/reject "<feedback>"` rows with descriptions.
    - Rewrite `/new`, `/resume`, `/eco`, `/diff` descriptions to reflect the real behavior.
  - Update the "Event Handling" table (lines 194-205) to add an `srs_submitted` / `srs_approved` / `srs_rejected` event handler that refreshes the startup summary on receipt (these are already emitted by `Engine.SubmitSRS` / `ApproveSRS` / `RejectSRS` — the TUI just needs to consume them).
  - Update the "Known Deferred Items" section (lines 353-360) to:
    - Remove the line about `/theme` having a placeholder command.
    - Remove "Approval card overlays" from the canonical list (approval is now functional via `/srs` + `/approve`/`/reject`) — or clarify that approval-card *overlays* are still deferred while the slash-command flow is live.
    - Leave shell-mode execution, file-mention autocomplete, LLM suggestions, and task inspection views deferred.
  - Add a new subsection "Write Operations from the TUI" listing the operations the TUI routes to the engine: `StartRun` (via bootstrap prompt or `/new`), `ResumeRun` (via `/resume`), `ApproveSRS`/`RejectSRS` (via `/approve`//`/reject`), `PauseRun`/`CancelRun` (via `/pause`//`/cancel`). This is the direct answer to the `docs/session-tui.md` reader question "what can I actually do from the TUI?"

- **`docs/getting-started.md`** — update lines 321-360 to describe the real slash-command surface and show a worked example: `axiom init → axiom tui → type prompt → /srs → /approve → observe execution → /diff → /cancel or wait`. Remove any language suggesting `/new` or `/resume` are just mnemonics.

- **`docs/cli-reference.md`** — regenerate or hand-update the TUI section (if it lists the slash commands) to match.

### 4.12 Out of scope (explicitly deferred)

The following are NOT addressed by this fix and should remain deferred:

- **SRS / ECO / diff full overlays with syntax highlighting.** The `OverlaySRS`, `OverlayECO`, and `OverlayDiff` enum values exist (`internal/tui/model.go:22-30`) but `renderOverlay` only handles `OverlayHelp`. Overlay rendering for these three is deferred because it would require wiring lipgloss box rendering plus scrollable viewports for each surface — much larger than this fix's scope. The slash-command handlers provide a textual substitute that is good enough for a first working slice.
- **`/eco approve <code>` / `/eco reject <code>`.** Identity wiring (who-is-approving) is not tracked in the TUI yet. The fix lists ECOs but does not approve/reject them — users must use the CLI.
- **File-mention autocomplete (`@`).** Unchanged from current deferred status.
- **Shell-mode execution (`!`).** Unchanged — the fix only updates the placeholder message to be more honest ("not yet routed" instead of "is handled by the engine").
- **Clarifications during execution.** Routing free text during an active run to a sub-orchestrator is a §9 Sub-Orchestrator concern and out of scope. The fix emits a hint and returns.
- **LLM-generated prompt suggestions.** Unchanged.
- **Diff preview overlay with syntax highlighting.** The fix provides a truncated textual diff; the overlay remains deferred.
- **Theme switching.** Removed entirely per §4.7 recommendation.

---

## 5. Files expected to change

| File | Change |
|---|---|
| `internal/tui/model.go` | Rewrite `submitInput` for mode-aware routing; add `startRunFromPrompt`, `runStartedMsg`, `runStartFailedMsg`; rewrite `handleSlashCommand` cases for `/new`, `/resume`, `/srs`, `/eco`, `/diff`; add `cmdApproveSRS`, `cmdRejectSRS`, `cmdECO`, `cmdDiff`, `cmdNewRun`, `cmdResumeRun`; remove `/theme`; add `refreshAfterStateChange` helper; update `cmdHelp`. |
| `internal/engine/interfaces.go` | Add `DiffRange(dir, base, head string) (string, error)` to `GitService` interface. |
| `internal/gitops/gitops.go` | Implement `(*Manager).DiffRange`. |
| `internal/engine/engine_test.go` | Add noop `DiffRange` to the test git service. |
| `internal/cli/cli_test.go` | Add noop `DiffRange` to the test git service. |
| `internal/session/manager.go` | Update `buildCommandRow` and `PromptSuggestions` for new approval-mode commands. |
| `internal/tui/tui_test.go` | Replace existing `TestSubmitInput_UserMessage` assertion; add ~25 new tests per §4.9; extend the `testSetup` helper with a configurable fake git service for dirty-tree and diff-output tests. |
| `internal/tui/plain.go` | Add non-interactive `RunOnce(prompt string)` helper for the integration test (if §4.10 subprocess path is taken). |
| `cmd/axiom/phase20_integration_test.go` (or new `phase21_tui_integration_test.go`) | New integration test per §4.10. |
| `docs/session-tui.md` | Slash-command table, event handling table, known-deferred-items, new "Write Operations from the TUI" subsection. |
| `docs/getting-started.md` | TUI section rewrite for lines 321-360. |
| `docs/cli-reference.md` | TUI slash-command list sync. |

No changes are required to `internal/engine/run.go`, `internal/engine/srs.go`, `internal/engine/eco.go`, `internal/state/*`, `internal/inference/*`, or any subsystem package. Every engine entrypoint the fix needs already exists and is unit-tested.

---

## 6. Notes, risks, and open questions

1. **Risk — `StartRun` emits a `run_created` event that the TUI already listens for.** After §4.1, the TUI calls `StartRun` synchronously from a `tea.Cmd` goroutine and then also receives the `RunCreated` event through `m.eventCh`. Both paths call `refreshAfterStateChange`. This is idempotent (mode determination reads the same DB state), so there is no correctness risk — but there is a cosmetic risk of double-rendering the action card. The fix must either debounce or make `refreshAfterStateChange` a no-op when the state has not changed since the last call. The simplest debounce is comparing `m.mode` before and after; if unchanged, skip the action-card append.

2. **Risk — existing `TestSubmitInput_UserMessage` will break.** The fix deliberately changes the behavior of that test's input path (a bootstrap-mode prompt now creates a run). The test must be updated, not deleted. See §4.9.

3. **Risk — fake git service drift.** Adding `DiffRange` to `GitService` means every in-repo test helper that mocks `GitService` must add a noop `DiffRange`. `grep -rn "GitService\|gitService" internal/` before the change to enumerate all implementations. The Issue 06 fix already added a `noopGitService` pattern to several test files — this fix extends the same pattern.

4. **Risk — plain-mode stdin extension.** §4.10 proposes adding a non-interactive "one prompt in, one result out" mode to the plain renderer. If the reviewer wants to keep the plain renderer strictly read-only, fall back to the exported `tui.RunOnce` helper alternative in §4.10. Either way, there must be a test at the `cmd/axiom/` level that catches composition-root regressions.

5. **Risk — operators expect `/new` to always start immediately.** Some users will run `/new` alone (expecting a fresh composer), others will run `/new Build a REST API` (expecting inline submission). §4.2 handles both. The help text must make the dual form explicit.

6. **Open question — should `/srs` display inline or in an overlay?** This plan uses inline-transcript display for simplicity and testability. A full `OverlaySRS` view with scrollback is deferred per §4.12. The transition from inline to overlay is a future refactor that replaces the return-a-string handler with an overlay-state transition. No data changes are needed.

7. **Open question — should the TUI auto-refresh after external SRS submission?** When an external orchestrator submits an SRS via the API while the TUI is open, the TUI will receive an `SRSSubmitted` event through the existing event bus subscription. Currently `handleEvent` does not handle that event type and will fall through to the default "collapsed single-line entry" case. The fix should add a handler for `SRSSubmitted`, `SRSApproved`, `SRSRejected` that calls `refreshAfterStateChange`, so operators see the status bar and action card transition in real time when an external orchestrator acts. This is in scope for §4.8 and must be listed in the updated `docs/session-tui.md` event handling table.

8. **Open question — should the clean-tree check be surfaced as a slash command?** Issue 06 added `axiom run --allow-dirty` for recovery scenarios. A `/run --allow-dirty <prompt>` TUI variant is technically possible, but we recommend against it for UX reasons: the recovery path should remain a deliberately inconvenient escape hatch, not a one-line TUI command. Operators in recovery can exit the TUI and use the CLI.

9. **Interaction with Issues 01–07.** This is the last P-level issue that blocks a non-technical user from completing a vertical slice entirely inside `axiom tui`. Issue 01 (external orchestrator handoff), Issue 02 (engine workers), Issue 03 (execution path), Issue 04 (merge queue validation), Issue 05 (test-generation wiring), Issue 06 (git safety), and Issue 07 (inference plane) collectively made the underlying operations real. This fix routes those operations through the TUI surface so operators can reach them without leaving the interactive terminal. Landing this fix means the §26.2.3 contract "route to appropriate engine subsystem" is finally honored.

10. **No new secrets exposure.** The fix threads the existing prompt through `StartRun` and renders the existing SRS draft in the transcript. The transcript is already persisted in `ui_messages` and may contain user prompts — this fix does not change that exposure model. No new secret handling is introduced.

11. **Root-cause escalation guard.** Following the Issue 06 / Issue 07 pattern: the `cmd/axiom/` integration test (§4.10) is the single guard that catches future composition-root regressions. Any refactor that detaches the TUI from `Engine.StartRun`/`Engine.ApproveSRS` will fail that test, not a unit test in `internal/tui/`. Prefer the subprocess-based integration test over exported helpers whenever feasible.

---

## 7. Acceptance criteria

- [ ] Typing a regular prompt in bootstrap mode calls `Engine.StartRun` and creates a `project_runs` row with `start_source = "tui"` and `initial_prompt` matching the input.
- [ ] Typing a regular prompt in approval mode emits a hint pointing at `/srs`/`/approve`/`/reject` and does NOT mutate run state.
- [ ] Typing a regular prompt in execution mode emits a "clarifications not yet routed" hint.
- [ ] `/new "<prompt>"` starts a run in bootstrap mode. Bare `/new` emits a hint. `/new` during an active run is refused.
- [ ] `/resume` resumes a paused run via `Engine.ResumeRun`. With a non-paused latest run, `/resume` reports status. With no runs, `/resume` suggests `/new`.
- [ ] `/srs` in `awaiting_srs_approval` returns the draft content plus the "Use /approve or /reject" instructions.
- [ ] `/srs` in `active` returns the approved SRS content from `.axiom/srs.md`.
- [ ] `/approve` in `awaiting_srs_approval` calls `Engine.ApproveSRS` and refreshes the status bar/action card within the same keystroke.
- [ ] `/approve` in any other status reports an error.
- [ ] `/reject "<feedback>"` calls `Engine.RejectSRS` with the feedback and returns the run to `draft_srs`.
- [ ] `/reject` without feedback emits the feedback-required error.
- [ ] `/eco` lists proposed ECOs for the active run, or reports "No ECOs proposed" when the list is empty, or reports "No active run" when none exists.
- [ ] `/diff` returns a truncated textual diff from `git diff <base>..<head>`, reports "No diff" when empty, and reports "No active run" when none exists.
- [ ] `/theme` is removed from the slash-command set and from the help page.
- [ ] `/help` includes `/approve` and `/reject` and groups commands by mode.
- [ ] External-orchestrator SRS submissions trigger `refreshAfterStateChange` in the TUI via the `SRSSubmitted` event handler.
- [ ] `cmd/axiom/` integration test passes: scripted `axiom tui --plain` with a prompt on stdin creates a run and exits cleanly.
- [ ] `docs/session-tui.md`, `docs/getting-started.md`, and `docs/cli-reference.md` reflect the new slash-command surface; no remaining "canned" or "placeholder" language.
- [ ] All existing tests pass unchanged; the ~25 new regression tests in §4.9 pass.
- [ ] `go test ./...` green across all 34 packages.

---

## 8. References

- Architecture §26.2.3 — Session UX Manager
- Architecture §26.2.6 — Input Model & Quick Commands
- Architecture §26.2.10 — Event Model
- Architecture §27.1 — Interactive Session Commands
- Architecture §28.2 — Git Hygiene & .axiom/ Lifecycle (clean-tree requirement)
- Implementation Plan Phase 15 — Session UX Manager and Bubble Tea TUI
- [`issues.md` §8](../../issues.md) — original finding
- [`issues/01/01-p0-fix-report.md`](../01/01-p0-fix-report.md) — reference for `StartRun` surface and CLI SRS operator pattern
- [`issues/06/06-p1-fix-report.md`](../06/06-p1-fix-report.md) — reference for clean-tree enforcement and `--allow-dirty` escape hatch
- [`issues/07/07-fix.md`](../07/07-fix.md) — reference for composition-root acceptance tests
- [`internal/cli/run.go`](../../internal/cli/run.go) — reference implementation of `Engine.StartRun` call site
- [`internal/cli/srs.go`](../../internal/cli/srs.go) — reference implementation of SRS show/approve/reject
- [`docs/session-tui.md`](../../docs/session-tui.md) — current documentation (will be updated)
- [`docs/getting-started.md`](../../docs/getting-started.md) — current user-facing TUI walk-through (will be updated)
