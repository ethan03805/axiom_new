# Issue 01 - P0 - `axiom run` lacks a usable external-orchestrator handoff

## Status

- Severity: P0
- State: Reproduced and triaged
- Last reviewed: 2026-04-06
- Source: `issues.md` finding 1

## Expected behavior

`axiom run "<prompt>"` should start the external-orchestrator handoff lifecycle:

1. capture the user prompt
2. persist it as part of run state
3. prepare the run and work-branch metadata for orchestration
4. remain in `draft_srs` while the user-appointed external orchestrator generates the first SRS draft
5. let that orchestrator submit the SRS through an engine-authoritative surface
6. move the run to `awaiting_srs_approval`

## Actual behavior

The current runtime only creates a `project_runs` record and stops there. The prompt is discarded, no complete handoff state is retained, and the run remains in `draft_srs` without enough information or API wiring for an external orchestrator to continue reliably.

## Reproduction

Confirmed on 2026-04-06 with a locally built binary and a clean throwaway git repo.

### Commands used

1. `go build -o %TEMP%\axiom-review.exe ./cmd/axiom`
2. create temp repo with an initial `main` commit
3. `%TEMP%\axiom-review.exe init --name repro-app`
4. `%TEMP%\axiom-review.exe run "Build a REST API with auth"`
5. `%TEMP%\axiom-review.exe status`
6. `%TEMP%\axiom-review.exe export`

### Observed results

- `axiom run` prints `Status: draft_srs`
- `axiom status` still reports `draft_srs`
- the repo stays on `main`; it does not switch to `axiom/repro-app`
- no `.axiom/srs-draft-<run-id>.md` file is created
- no `.axiom/srs.md` or `.axiom/srs.md.sha256` file is created
- `axiom export` shows run metadata only; there is no stored prompt-backed handoff state to continue from

## Root cause

### 1. The prompt is dropped at the entrypoints

- `internal/cli/run.go` accepts `prompt` but does not pass it to the engine.
- `internal/api/server.go` decodes `RunRequest.Prompt` but also drops it before calling the engine.

### 2. The engine/state contract cannot retain the prompt

- `internal/engine/run.go` defines `RunOptions` with only `ProjectID`, `BaseBranch`, and `BudgetUSD`.
- `internal/state/models.go` defines `ProjectRun` without any prompt/handoff input field.
- `internal/state/migrations/001_initial_schema.sql` defines `project_runs` without a prompt column, so restart-safe external handoff is impossible.

### 3. The live runtime has no embedded orchestrator, and that is acceptable for now

- Given the current product constraints, the initial SRS should come from a user-appointed external Claw / Claude Code / Codex / OpenCode orchestrator, not from an embedded Axiom bootstrap worker.
- `internal/engine/run.go` still hardcodes `orchestrator_mode = "embedded"`, which conflicts with that intended operating model.
- `internal/app/app.go` does not wire an embedded orchestrator service anyway, so the codebase currently lands in an inconsistent middle state: no real embedded orchestrator, but also no complete external handoff.

### 4. The external-orchestrator handoff is incomplete

- The REST API exposes SRS approve/reject only after a draft already exists.
- There is no REST read surface that gives an external orchestrator a complete engine-authored pending-run handoff payload.
- There is no REST endpoint to submit a draft SRS for a run that is still in `draft_srs`.
- `internal/api/websocket.go` accepts `submit_srs` on the control WebSocket but only returns `accepted`; it never dispatches to `Engine.SubmitSRS`.

### 5. Tests currently encode the narrowed behavior

- `cmd/axiom/phase20_integration_test.go` explicitly expects `draft_srs` after `axiom run`.
- That expectation is only half-right. `draft_srs` is a valid waiting state in the external-orchestrator model, but the tests do not assert prompt persistence, branch setup, or a working submit-SRS continuation path.

## Fix plan

### 1. Add a single high-level run-start entrypoint

Introduce an engine method such as `StartRun` or `BeginRun` and stop calling `CreateRun` directly from CLI and API surfaces.

This entrypoint should:

- accept `prompt`
- accept the caller source (`cli`, `tui`, `api`, `control-ws`)
- accept the orchestrator runtime / identity data
- validate workspace preconditions
- create the run
- persist the prompt
- set the run up for external orchestration without attempting embedded SRS generation

`CreateRun` can stay as a low-level helper, but it should stop being the public lifecycle entrypoint.

### 2. Persist the initial prompt and handoff metadata

Add a schema migration and state-layer support for pending-run inputs.

Recommended minimum additions:

- `project_runs.initial_prompt TEXT NOT NULL`
- `project_runs.start_source TEXT NOT NULL`
- `project_runs.orchestrator_mode TEXT NOT NULL` should be set correctly for the actual operating model
- optionally `project_runs.handoff_error TEXT NULL` or equivalent handoff-status field for failed orchestration attempts

Update:

- `state.ProjectRun`
- `state.CreateRun` / readers
- `engine.RunOptions`
- `axiom export`
- status projections that need to surface handoff failure/recovery state

### 3. Make external orchestration the only supported startup path for now

Do not implement embedded auto-bootstrap as part of this P0.

Instead, make the near-term contract explicit:

- the user appoints and launches the orchestrator outside Axiom
- that orchestrator uses repo context and its own tool access to reason about the prompt
- it submits the resulting SRS draft back through Axiom
- the engine remains the sole authority for validation, persistence, approvals, and later execution

### 4. Complete the external handoff surfaces

Add first-class ways for a user-appointed external orchestrator to continue a run safely.

Recommended additions:

- CLI / export surfaces that expose the stored prompt and current run handoff state
- API read surface for the pending run input and current draft / approval state
- REST or WebSocket submission path that actually invokes `Engine.SubmitSRS`
- clear status output that says the run is waiting on an external orchestrator, not waiting on an internal bootstrap job

The important rule is that every submission path still goes through engine validation in `SubmitSRS`.

### 5. Add a first-class SRS operator surface

- CLI:
  - `axiom srs show`
  - `axiom srs approve`
  - `axiom srs reject --feedback "..."`
- API:
  - `GET /api/v1/projects/:id/srs` for draft/current SRS visibility
  - `POST /api/v1/projects/:id/srs/submit` or equivalent
- WebSocket control channel:
  - make `submit_srs` call `Engine.SubmitSRS`

### 6. Reuse existing git workspace helpers during run start

The repo already has the required branch/setup primitives in `internal/gitops`.

`StartRun` should reuse:

- `SetupWorkBranch(...)`
- clean-tree validation

This will fix the currently verified symptom where `axiom run` leaves the repo on `main` even though the run metadata says `axiom/<slug>`.

### 7. Rewrite tests around the real lifecycle

Update and expand tests so they prove the intended external-orchestrator path instead of the current partial handoff.

Add or change coverage for:

- `init -> run -> prompt persisted -> status still draft_srs`
- `init -> run -> branch setup performed`
- readback of pending run input for the orchestrator
- CLI/API/WebSocket SRS submission paths
- `approve -> .axiom/srs.md + .sha256 -> run active`
- restart recovery with a persisted prompt and/or pending draft
- external-mode handoff that persists prompt and waits for `submit_srs`

The current `phase20_integration_test.go` expectation for `draft_srs` after `run` should stay, but it must be expanded to assert the actual handoff contract.

### 8. Update docs to match the repaired lifecycle

At minimum update:

- `docs/getting-started.md`
- `docs/cli-reference.md`
- `docs/api-server.md`
- `docs/srs-eco.md`
- `docs/database-schema.md`
- `docs/development.md`

The docs should clearly distinguish:

- the current external-orchestrator-only startup model
- the fact that the user must appoint and launch that orchestrator outside Axiom
- how users view/approve/reject SRS drafts

## Recommended implementation order

1. schema migration plus state model changes
2. `engine.RunOptions` / `StartRun` API
3. CLI / status / export changes for pending-run handoff visibility
4. API REST and WebSocket read/submit paths
5. CLI SRS operator commands
6. integration tests
7. docs cleanup

## Acceptance criteria

- `axiom run "<prompt>"` persists the prompt and leaves the run in `draft_srs`
- `draft_srs` is treated as the valid waiting state for an external orchestrator, not as an implicit embedded-bootstrap phase
- the appointed external orchestrator can retrieve the pending run input through engine-authoritative surfaces
- the external orchestrator can submit an SRS draft without bypassing engine validation
- successful submission transitions the run to `awaiting_srs_approval`
- the pending draft can be viewed and approved or rejected through supported operator surfaces
- approval writes `.axiom/srs.md` and `.axiom/srs.md.sha256`, stores `srs_hash`, and transitions the run to `active`
- restarting the app preserves enough state to continue handoff or approval cleanly

## Notes

- Fixing this issue restores the prompt -> SRS -> approval gate only. It does not by itself make post-approval execution work; the other P0 issues still block background scheduling, real task execution, and real merge validation.
- For the near term, no embedded auto-bootstrap should be added. The initial SRS should come from a user-appointed external Claw / Claude Code / Codex / OpenCode runtime.
