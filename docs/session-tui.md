# Session UX Manager and TUI Reference

## Overview

The Session UX Manager and Bubble Tea TUI implement the interactive operator experience for Axiom (Architecture Section 26.2). The system has two layers:

1. **Session UX Manager** (`internal/session/`) — Engine-side service that manages session lifecycle, mode transitions, startup summaries, transcript persistence, compaction, export, and prompt suggestions. Used by both the TUI and plain CLI renderer.
2. **Bubble Tea TUI** (`internal/tui/`) — Full-screen interactive terminal UI built on Bubble Tea, Bubbles, and Lip Gloss. Runs as a thin client over engine-emitted view models. Does NOT read SQLite directly.

## Session UX Manager

### Architecture

The Session UX Manager is a trusted-engine component (Section 26.2.3) responsible for terminal-session orchestration. It interacts with the engine via:

- `engine.GetRunStatus()` — for status projections
- `engine.DB()` — for session CRUD operations
- `engine.Bus()` — for emitting view-model events

### Session Lifecycle

```
CreateSession(projectID) → UISession
ResumeSession(sessionID) → UISession
ResumeOrCreateSession(projectID) → UISession  // resume latest or create new
```

Sessions are created with an auto-determined mode based on the current run state. When resumed, the mode is refreshed from the latest run state. Sessions are persisted in the `ui_sessions` table and survive engine restarts.

### Session Modes

The session operates in four explicit modes (Section 26.2.7), driven by engine run state:

| Mode | Run Status | Purpose |
|------|-----------|---------|
| `bootstrap` | No run, or `draft_srs` | Initial prompt capture and SRS drafting |
| `approval` | `awaiting_srs_approval` | SRS/ECO review and decision flow |
| `execution` | `active` or `paused` | Operator console for task progress and approvals |
| `postrun` | `completed`, `cancelled`, or `error` | Final review, export, and follow-up |

Mode determination logic (`DetermineMode`):
1. Check for an active run (draft, awaiting, active, paused)
2. If none, check for the most recent run of any status (completed, cancelled, error → postrun)
3. If no runs exist at all, return `bootstrap`

### Startup Summary

The startup summary (Section 26.2.4) is a deterministic, engine-authored frame rendered immediately on launch — before any LLM call is made:

```go
summary, err := manager.StartupSummary(projectID)
```

Returns `StartupSummaryData` containing:
- `ProjectName`, `ProjectSlug`, `RootDir` — workspace identity
- `Mode` — current session mode
- `Branch`, `RunID`, `RunStatus` — run info (if active)
- `Tasks` — task count summary
- `Budget` — budget summary
- `ActionCard` — primary action text derived from mode:
  - **bootstrap:** "Describe what you want to build."
  - **approval:** "Review the SRS and approve or reject it."
  - **execution:** "Execution active: N task(s) in progress, M queued."
  - **postrun:** "Run complete. Review diffs, export, or start a new session."
- `Commands` — suggested slash commands for the current mode

Emits a `startup_summary` view-model event on the bus.

### Transcript Persistence

Messages are stored in `ui_messages` with auto-incrementing sequence numbers:

```go
seq, err := manager.AddTranscriptMessage(sessionID, role, kind, content)
```

Roles: `user`, `assistant`, `system`
Kinds: `user`, `assistant`, `system_card`, `event`, `tool`, `approval`, `ephemeral`

The manager maintains an in-memory sequence counter per session to avoid DB queries for every message.

### Compaction

Long sessions are compacted to prevent unbounded growth (Section 26.2.9):

```go
err := manager.CompactSession(sessionID, keepCount)
```

Compaction:
1. Reads all messages for the session
2. Summarizes the oldest messages (those beyond `keepCount` from the end)
3. Stores the summary in `ui_session_summaries` with kind `transcript_compaction`
4. Deletes the compacted messages from `ui_messages`
5. Emits a `transcript_compacted` event

The default compaction threshold is configured via `cli.compact_after_messages` (default: 200).

### Export

```go
export, err := manager.ExportSession(sessionID)
```

Returns `SessionExport` containing the session metadata, all remaining messages, and all compaction summaries.

### Prompt Suggestions

```go
suggestions := manager.PromptSuggestions(projectID)
```

Returns mode-specific suggestions (Section 26.2.8):
1. **Approval:** "Review and approve the SRS", "Reject SRS with feedback"
2. **Execution:** "/status", "/tasks", "/diff", "/budget", "/pause"
3. **Postrun:** "/diff", "Export session transcript", "Start a new run"
4. **Bootstrap:** "Describe what you want to build", example prompts

Suggestions are heuristic-first and deterministic. LLM-generated suggestions are deferred.

### Input History

```go
err := manager.RecordInput(projectID, sessionID, "prompt", content)
history, err := manager.InputHistory(projectID, limit)  // most recent first
```

Input history is stored per-project in `ui_input_history` and supports up-arrow recall in the TUI.

## Bubble Tea TUI

### Technology

| Library | Purpose |
|---------|---------|
| [Bubble Tea](https://github.com/charmbracelet/bubbletea) | Event loop, terminal state machine |
| [Bubbles](https://github.com/charmbracelet/bubbles) | Reusable components (textarea, viewport) |
| [Lip Gloss](https://github.com/charmbracelet/lipgloss) | Styling, layout, color adaptation |

### Layout (Section 26.2.5)

The TUI layout is full-screen and composed of:

```
┌─────────────────────────────────────────────────────┐
│ Status Bar: project | MODE | branch | status | budget│
├───────────────────────────────────┬──────────────────┤
│                                   │ Tasks            │
│ Transcript viewport               │   Done: 5        │
│   [system] Action card            │   Running: 2     │
│   > User message                  │   Queued: 4      │
│   [event] Task completed          │   ─────────      │
│   ...                             │   $3.50/$10.00   │
│                                   │                  │
├───────────────────────────────────┴──────────────────┤
│ Composer: [type a message or / for commands...]      │
│ [BOOTSTRAP]  / commands  ! shell  @ mention          │
└─────────────────────────────────────────────────────┘
```

- **Status bar** — project name, mode label, branch, run status, budget
- **Transcript viewport** — messages, system cards, events, approval cards
- **Task rail** — live task summary with status counts and budget (hidden if width < 60)
- **Footer composer** — multiline text input with mode indicator and shortcut hints
- **Overlay surfaces** — help view, slash command palette (Esc to dismiss)

### Slash Commands (Section 26.2.6)

| Command | Description |
|---------|-------------|
| `/status` | Show project status, run state, budget, tasks |
| `/tasks` | Show task breakdown by status |
| `/budget` | Show budget details and warnings |
| `/srs` | View SRS approval state |
| `/eco` | View pending ECOs |
| `/diff` | Preview latest changes |
| `/new` | Start a new bootstrap session |
| `/resume` | Resume an existing session |
| `/pause` | Pause active execution |
| `/cancel` | Cancel active execution |
| `/clear` | Clear visible transcript (preserves session state) |
| `/theme` | Switch display theme (deferred) |
| `/help` | Show all commands and keyboard shortcuts |

### Input Model

- `/` at start of input opens slash command execution
- `!` at start of input enters shell mode (displays command intent)
- Up/Down arrows navigate input history
- Enter submits input
- Ctrl+C quits
- Esc dismisses overlays

### Event Handling

The TUI subscribes to the engine event bus and handles:

| Event | Action |
|-------|--------|
| `task_projection_updated` | Refresh task summary and budget |
| `session_mode_changed` | Update mode display |
| `approval_requested` | Add approval card to transcript |
| `diff_preview_ready` | Notify user in transcript |
| Other authoritative events | Collapsed single-line entries |

### Theme

The default `axiom` theme uses ANSI 256 colors for broad terminal compatibility:

| Element | Color |
|---------|-------|
| Primary text | White (15) |
| Secondary text | Light gray (7) |
| Accent | Blue (39) |
| Muted | Dark gray (8) |
| Error | Red (196) |
| Warning | Orange (214) |
| Success | Green (46) |
| Mode label | White on blue |
| Status bar | White on dark gray |

## Plain-Text Fallback (Section 26.2.11)

When stdout is not a TTY, or the user explicitly requests plain rendering (`axiom tui --plain`), Axiom falls back to a line-oriented text renderer:

```go
renderer := tui.NewPlainRenderer(engine, manager, config, projectID, logger)
renderer.RenderStartup(w)         // Deterministic startup frame
renderer.RenderMessage(w, role, content)  // Single message line
renderer.RenderStatus(w)          // Project status
renderer.RenderSessionList(w, projectID)  // List sessions
renderer.RenderExport(w, export)  // Full session export
renderer.RenderApproval(w, type, desc)    // Approval prompt
renderer.RenderTaskList(w, tasks) // Task summary
renderer.RenderEvent(w, type, details)    // Single event line
renderer.RunStatus(w)             // One-line status for polling
```

The plain renderer preserves the same workflow states, approval prompts, and session operations as the full-screen TUI.

## CLI Commands

### `axiom tui`

Launch the interactive full-screen TUI. Creates or resumes a session automatically.

```bash
axiom tui [--plain]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--plain` | false | Force plain-text renderer |

Behavior:
- If stdout is a TTY and `--plain` is not set: launches full-screen Bubble Tea TUI
- If stdout is not a TTY or `--plain` is set: renders the startup frame in plain text

### `axiom session list`

List all resumable sessions for the current project.

```bash
$ axiom session list
Sessions for project:
  a1b2c3d4  (unnamed)  mode:bootstrap  last:2026-04-06 01:27
  e5f6g7h8  (unnamed)  mode:execution  run:i9j0k1l2  last:2026-04-06 02:15
```

### `axiom session resume <session-id>`

Resume a persisted session by ID.

```bash
$ axiom session resume a1b2c3d4-e5f6-7890-abcd-ef1234567890
Resumed session a1b2c3d4 (mode: bootstrap)
```

The full session ID is required. The session's mode is refreshed from the current run state.

### `axiom session export <session-id>`

Export a session's full transcript and compaction summaries.

```bash
$ axiom session export a1b2c3d4-e5f6-7890-abcd-ef1234567890
Session Export: a1b2c3d4
  Project:  test-project
  Mode:     bootstrap
  Created:  2026-04-06 01:27:20

--- Transcript ---
[01:27:20] > Build me a REST API
[01:27:21]   Starting SRS generation...
```

## Database Tables

The session system uses four tables (created in migration `001_initial_schema.sql`):

| Table | Purpose |
|-------|---------|
| `ui_sessions` | Session identity, project/run association, mode, timestamps |
| `ui_messages` | Transcript messages with sequence ordering |
| `ui_session_summaries` | Compaction summaries |
| `ui_input_history` | Per-project input history for recall |

### Session Repository Methods

| Method | Description |
|--------|-------------|
| `CreateSession(s)` | Insert a new session |
| `GetSession(id)` | Get session by ID |
| `ListSessionsByProject(projectID)` | List all sessions for a project |
| `GetLatestSessionByProject(projectID)` | Get most recently active session |
| `UpdateSessionActivity(id)` | Touch last_active_at timestamp |
| `UpdateSessionMode(id, mode)` | Change session mode |
| `UpdateSessionRunID(id, runID)` | Associate session with a run |
| `AddMessage(m)` | Insert a transcript message |
| `GetMessages(sessionID)` | Get all messages ordered by seq |
| `GetMessageCount(sessionID)` | Count messages in session |
| `GetMaxSeqBySession(sessionID)` | Max sequence number (for seq initialization) |
| `DeleteMessagesBySessionBefore(sessionID, seq)` | Delete messages before cutoff |
| `AddSessionSummary(s)` | Insert a compaction summary |
| `GetSessionSummaries(sessionID)` | List summaries for a session |
| `AddInputHistory(h)` | Record an input entry |
| `GetInputHistoryByProject(projectID, limit)` | Recent history, most recent first |

## View-Model Events

The Session UX Manager emits these view-model events (Section 26.2.10) via the engine bus. These are NOT persisted to SQLite — they are fanned out to subscribers only.

| Event | Emitted When |
|-------|-------------|
| `startup_summary` | Startup summary is generated |
| `session_mode_changed` | Session mode transitions |
| `prompt_suggestion` | New suggestions available |
| `task_projection_updated` | Task list changes |
| `approval_requested` | SRS/ECO approval needed |
| `approval_resolved` | Approval decision made |
| `diff_preview_ready` | Diff available for review |
| `transcript_compacted` | Old messages compacted |

## Test Coverage

| Package | Tests | Coverage |
|---------|-------|----------|
| `internal/session` | 19 | Session create/resume (4), mode determination (5), startup summary (2), transcript (1), compaction (1), export (2), suggestions (2), events (1), input history (1) |
| `internal/tui` | 29 | Model creation (2), view rendering (3), input handling (2), slash commands (6), overlay (1), status bar (1), task rail (1), window resize (1), transcript (1), submit input (2), plain renderer (7) |
| `internal/cli` (session) | 7 | Session list with/without sessions (2), export with messages (1), resume found/not-found (2), TUI plain flag (1), TUI plain mode (1) |

## Known Deferred Items

- **File mention autocomplete** (`@` trigger) — slash command palette provides the extension point but autocomplete is not yet connected to the semantic index
- **LLM-generated prompt suggestions** — deferred per Section 26.2.8; only deterministic heuristic suggestions are implemented
- **Theme switching** — `/theme` command exists as a placeholder; only the default `axiom` theme is available
- **Diff preview overlay** — `/diff` command returns a text message; full overlay with syntax highlighting is deferred
- **Task inspection views** — task rail shows summary counts; individual task detail view is deferred
- **Approval card overlays** — approvals appear as transcript entries; dedicated overlay dialogs are deferred
- **Shell mode execution** — `!` prefix captures intent but does not execute shell commands (engine-mediated execution is deferred)
