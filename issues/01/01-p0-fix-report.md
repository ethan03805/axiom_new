# Issue 01 - P0 Fix Report: External Orchestrator Handoff

**Date:** 2026-04-06
**Status:** Fixed and verified
**Issue:** `issues/P0/01-p0-external-orchestrator-handoff-incomplete.md`

---

## Summary

`axiom run "<prompt>"` was incomplete: it discarded the prompt, never set up the work branch, hardcoded `orchestrator_mode = "embedded"`, and provided no way for an external orchestrator to submit or manage SRS drafts. This fix restores the full external-orchestrator handoff lifecycle.

## Changes Made

### 1. Schema migration — `internal/state/migrations/008_external_handoff.sql`

Added two columns to `project_runs`:
- `initial_prompt TEXT NOT NULL DEFAULT ''` — stores the user's prompt for restart-safe handoff
- `start_source TEXT NOT NULL DEFAULT 'cli'` — records how the run was initiated (cli, api, tui, control-ws)

### 2. State model updates — `internal/state/models.go`, `internal/state/runs.go`

- Added `InitialPrompt` and `StartSource` fields to `ProjectRun` struct
- Updated `CreateRun` INSERT to include the new columns
- Updated all SELECT queries and both `scanRun`/`scanRunRow` functions to read the new columns
- Added `UpdateRunHandoff(id, prompt, source, orchMode)` method for post-create handoff metadata persistence

### 3. Engine: `StartRun` entrypoint — `internal/engine/run.go`

Introduced `StartRunOptions` and `Engine.StartRun()` as the high-level run-start entrypoint. This method:
- Validates the prompt is non-empty
- Validates the workspace is clean (`git status --porcelain`)
- Creates the run record via the existing low-level `CreateRun`
- Persists prompt, start source, and `orchestrator_mode = "external"`
- Sets up the work branch via `gitops.SetupWorkBranch`
- Emits a `run_created` event with prompt and orchestrator metadata

`CreateRun` remains as a low-level helper but is no longer the public lifecycle entrypoint.

### 4. GitService interface extension — `internal/engine/interfaces.go`

Added `ValidateClean(dir string) error` and `SetupWorkBranch(dir, baseBranch, workBranch string) error` to the `GitService` interface. The `gitops.Manager` already implemented these methods; they were just not part of the interface.

### 5. CLI updates — `internal/cli/run.go`, `internal/cli/commands.go`

- `runAction` now calls `Engine.StartRun` instead of `Engine.CreateRun`
- Output updated to show orchestrator mode, prompt, and instructions for external orchestrator workflow
- Registered new `srs` command group

### 6. SRS operator CLI commands — `internal/cli/srs.go` (new file)

Added `axiom srs` command group with four subcommands:
- `axiom srs show` — shows current SRS draft/approved SRS and prompt
- `axiom srs submit <file>` — submits an SRS draft from a file
- `axiom srs approve` — approves the pending SRS draft
- `axiom srs reject --feedback "..."` — rejects with feedback, returns to draft_srs

### 7. API endpoints — `internal/api/server.go`, `internal/api/handlers.go`, `internal/api/types.go`

New REST endpoints:
- `GET /api/v1/projects/:id/srs` — returns draft/approved SRS content and status
- `POST /api/v1/projects/:id/srs/submit` — accepts SRS draft from external orchestrator
- `GET /api/v1/projects/:id/run/handoff` — returns complete pending-run handoff payload (prompt, source, mode, branches, budget)

Updated:
- `POST /api/v1/projects/:id/run` — now requires `prompt` field and calls `StartRun`
- Added `SRSSubmitRequest` type

### 8. WebSocket fix — `internal/api/websocket.go`

The `submit_srs` control WebSocket message now actually dispatches to `Engine.SubmitSRS` instead of just returning `"accepted"`. Returns the run ID and new status on success.

### 9. Engine: SRS draft reader — `internal/engine/srs.go`

Added `Engine.ReadSRSDraft(runID)` to expose draft reading through the engine (used by the new API handler).

### 10. Export updates — `internal/cli/export.go`

`axiom export` now includes `initial_prompt`, `start_source`, and `orchestrator_mode` in the run section.

### 11. Status updates — `cmd/axiom/main.go`

`axiom status` now shows:
- Orchestrator mode
- "Waiting: external orchestrator to submit SRS draft" when in draft_srs + external mode
- The initial prompt (truncated to 80 chars)

### 12. Test updates

**Updated existing tests:**
- `internal/cli/cli_test.go` — added `noopGitService` to test helper, registered `srs` in required commands
- `internal/cli/run_test.go` — updated assertions for new output format and prompt persistence
- `cmd/axiom/phase20_integration_test.go` — added git commit after init (clean tree requirement), added assertions for external orchestrator output and prompt visibility

**New test file: `internal/engine/startrun_test.go`** — 7 tests covering:
- `TestStartRun_PersistsPromptAndMetadata` — prompt, source, mode persisted correctly
- `TestStartRun_RequiresPrompt` — empty prompt rejected
- `TestStartRun_DefaultsSourceToCLI` — missing source defaults to "cli"
- `TestExternalHandoff_FullLifecycle` — start -> submit SRS -> approve -> active with .axiom/srs.md written
- `TestExternalHandoff_RejectAndResubmit` — reject -> draft_srs -> resubmit -> awaiting_srs_approval
- `TestStartRun_SubmitInvalidSRSFails` — invalid SRS structure rejected, run stays in draft_srs
- `TestRestartRecovery_PromptPersisted` — prompt and metadata survive re-read from DB

## Verification

### Build
```
go build ./...  # clean, zero errors
```

### Test results
```
go test ./...   # 33 packages, all pass
```

All packages:
- `cmd/axiom` — integration tests pass (init -> commit -> run -> status flow)
- `internal/engine` — 7 new lifecycle tests + all existing tests pass
- `internal/cli` — run action tests updated and pass
- `internal/api` — all existing tests pass
- `internal/state` — migration applies cleanly, all existing tests pass
- All other 28 packages — unchanged and passing

## Acceptance Criteria Verification

| Criterion | Status |
|-----------|--------|
| `axiom run "<prompt>"` persists the prompt and leaves run in `draft_srs` | Verified via `TestStartRun_PersistsPromptAndMetadata` |
| `draft_srs` treated as external orchestrator waiting state | Verified: `orchestrator_mode = "external"`, status output says "Waiting: external orchestrator" |
| External orchestrator can retrieve pending run input | Verified: `GET /api/v1/projects/:id/run/handoff` returns prompt, source, mode |
| External orchestrator can submit SRS without bypassing engine validation | Verified: CLI `axiom srs submit`, API `POST .../srs/submit`, WebSocket `submit_srs` all go through `Engine.SubmitSRS` with structure validation |
| Successful submission transitions to `awaiting_srs_approval` | Verified via `TestExternalHandoff_FullLifecycle` |
| Pending draft viewable and approvable/rejectable | Verified: `axiom srs show`, `axiom srs approve`, `axiom srs reject --feedback`, API equivalents |
| Approval writes `.axiom/srs.md`, `.axiom/srs.md.sha256`, stores hash, transitions to `active` | Verified via `TestExternalHandoff_FullLifecycle` |
| Restart preserves state for handoff/approval continuation | Verified via `TestRestartRecovery_PromptPersisted` |

## Files Modified

| File | Change |
|------|--------|
| `internal/state/migrations/008_external_handoff.sql` | New migration |
| `internal/state/models.go` | Added `InitialPrompt`, `StartSource` fields |
| `internal/state/runs.go` | Updated queries, scans, added `UpdateRunHandoff` |
| `internal/engine/interfaces.go` | Extended `GitService` with `ValidateClean`, `SetupWorkBranch` |
| `internal/engine/run.go` | Added `StartRunOptions`, `StartRun` |
| `internal/engine/srs.go` | Added `ReadSRSDraft` |
| `internal/engine/engine_test.go` | Added noop methods to test git service |
| `internal/engine/startrun_test.go` | New: 7 lifecycle tests |
| `internal/cli/commands.go` | Registered `SRSCmd` |
| `internal/cli/run.go` | Updated to use `StartRun` |
| `internal/cli/srs.go` | New: SRS operator commands |
| `internal/cli/export.go` | Added prompt/source/mode to export |
| `internal/cli/cli_test.go` | Added `noopGitService`, updated assertions |
| `internal/cli/run_test.go` | Updated assertions for new output |
| `internal/api/types.go` | Added `SRSSubmitRequest` |
| `internal/api/server.go` | Added SRS/handoff routes, updated run endpoint |
| `internal/api/handlers.go` | Added `HandleGetSRS`, `HandleSRSSubmit`, `HandleGetRunHandoff` |
| `internal/api/websocket.go` | Wired `submit_srs` to `Engine.SubmitSRS` |
| `cmd/axiom/main.go` | Updated status output |
| `cmd/axiom/phase20_integration_test.go` | Added clean-tree commit, updated assertions |
