# SRS and ECO Reference

This document covers the Software Requirements Specification (SRS) lifecycle and Engineering Change Order (ECO) management in Axiom. Per Architecture Sections 6, 7, and 8.7.

## SRS Lifecycle

### Overview

The SRS is the scope-locking contract that governs an Axiom run. Once a user accepts the SRS, the project scope is immutable. Changes to environmental realities (broken dependencies, API changes) are handled through the ECO process, not scope modifications.

Current operating model: the initial SRS is expected to come from a user-appointed external orchestrator such as Claw, Claude Code, Codex, or OpenCode. Axiom does not auto-launch an embedded orchestrator in normal app flows today.

### State Machine

```
                  SubmitSRS()              ApproveSRS()
  draft_srs ──────────────► awaiting_srs_approval ──────────► active
       ▲                          │
       │          RejectSRS()     │
       └──────────────────────────┘
```

A run begins in `draft_srs` when created. In the current operating model, `draft_srs` is the valid waiting state while the appointed external orchestrator prepares the first SRS draft and submits it via `SubmitSRS()`. The user (or delegated Claw) reviews and either approves or rejects. On rejection, the run returns to `draft_srs` for revision and resubmission.

### SRS Structure Validation

Every submitted SRS is validated for required sections per Architecture Section 6.1:

| Required Section | Heading |
|------------------|---------|
| Architecture | `## 1. Architecture` |
| Requirements & Constraints | `## 2. Requirements & Constraints` |
| Test Strategy | `## 3. Test Strategy` |
| Acceptance Criteria | `## 4. Acceptance Criteria` |

The SRS must also begin with a title in the format `# SRS: <Project Name>`.

Validation is structural only — it checks that the expected sections exist, not that their content is complete. Content quality is the orchestrator's responsibility.

### SRS Approval Flow

**On approval (`ApproveSRS`):**
1. Read the pending draft from `.axiom/srs-draft-<run-id>.md`
2. Write `.axiom/srs.md` with read-only permissions (`0o444`)
3. Write `.axiom/srs.md.sha256` containing the hex-encoded SHA-256 hash
4. Store the hash in the `project_runs.srs_hash` column
5. Transition the run to `active` status
6. Delete the draft file
7. Emit `srs_approved` event with the hash

**On rejection (`RejectSRS`):**
1. Transition the run back to `draft_srs` status
2. Emit `srs_rejected` event with the rejection feedback
3. The draft file is preserved for the orchestrator to revise

### Immutability

After approval, the SRS file at `.axiom/srs.md` is read-only (file permissions `0o444`). The engine will not overwrite it through normal operation. During startup recovery, the engine verifies the SHA-256 hash on disk against both `.axiom/srs.md.sha256` and the latest stored `project_runs.srs_hash`.

### Bootstrap Mode

During SRS generation, the orchestrator operates in bootstrap mode with scoped context access per Architecture Section 8.7:

| Project Type | Context Provided |
|-------------|------------------|
| **Greenfield** | User prompt + project configuration only. No repo-map or semantic index. |
| **Existing project** | User prompt + project configuration + read-only repo-map (file listing). Excludes `.axiom/`, `.git/`, `node_modules/`. |

The `BuildBootstrapContext()` function assembles this context:

Current runtime note: `BuildBootstrapContext()` is the engine-side helper for the bootstrap phase, but the live app does not yet run an embedded bootstrap orchestrator. For now, this context is intended to support the external-orchestrator path.

```go
ctx, err := srs.BuildBootstrapContext(projectRoot, isGreenfield)
// ctx.ProjectRoot — project directory
// ctx.IsGreenfield — whether the project is new
// ctx.RepoMap — file listing for existing projects (empty for greenfield)
```

### SRS Draft Persistence

Pending SRS drafts are persisted to the filesystem at `.axiom/srs-draft-<run-id>.md`. This allows:
- Multiple revision cycles (submit → reject → revise → resubmit)
- Recovery if the engine restarts during the approval phase
- Inspection by the user before approval

API:
```go
srs.WriteDraft(projectRoot, runID, content)  // persist draft
srs.ReadDraft(projectRoot, runID)            // read draft
srs.DeleteDraft(projectRoot, runID)          // remove draft (noop if missing)
```

### SRS Hash

```go
hash := srs.ComputeHash([]byte(content))  // hex-encoded SHA-256
```

Returns a 64-character lowercase hex string. The same hash is written to both the `.axiom/srs.md.sha256` file and the `project_runs.srs_hash` database column.

## ECO Lifecycle

### Overview

Engineering Change Orders (ECOs) provide a controlled mechanism for adapting to external environmental realities without violating the immutable-scope principle. ECOs address situations where the real world contradicts assumptions in the SRS.

### Valid ECO Categories

ECOs are strictly limited to the following categories per Architecture Section 7.2:

| Code | Category | Example |
|------|----------|---------|
| `ECO-DEP` | Dependency Unavailable | `left-pad` removed from npm |
| `ECO-API` | API Breaking Change | REST endpoint returns 404 |
| `ECO-SEC` | Security Vulnerability | CVE in chosen auth library |
| `ECO-PLT` | Platform Incompatibility | Library doesn't support target OS |
| `ECO-LIC` | License Conflict | GPL dependency in MIT project |
| `ECO-PRV` | Provider Limitation | API rate limit too low |

The engine rejects ECOs that do not match a defined category.

### ECO Proposal Validation

A proposal must include all of the following:

| Field | Description |
|-------|-------------|
| `Category` | One of the 6 valid ECO codes |
| `AffectedRefs` | SRS sections/requirements affected (e.g., `FR-001, AC-005`) |
| `Description` | Description of the environmental issue |
| `ProposedChange` | Proposed functional substitute |

```go
err := eco.ValidateProposal(eco.Proposal{
    Category:       "ECO-DEP",
    AffectedRefs:   "FR-001, AC-002",
    Description:    "Library passport-oauth2 is deprecated.",
    ProposedChange: "Replace with arctic v2.1.",
})
```

### ECO Engine Flow

**Proposing an ECO (`ProposeECO`):**
1. Validate the proposal (category, required fields)
2. Verify the run is in `active` or `paused` status
3. Auto-generate an ECO code (`ECO-001`, `ECO-002`, ...)
4. Create the ECO in the `eco_log` table with `proposed` status
5. Emit `eco_proposed` event

**Approving an ECO (`ApproveECO`):**
1. Transition ECO status from `proposed` to `approved`
2. Write the ECO record as a markdown file under `.axiom/eco/`
3. Emit `eco_resolved` event with `resolution: approved`

**Rejecting an ECO (`RejectECO`):**
1. Transition ECO status from `proposed` to `rejected`
2. Emit `eco_resolved` event with `resolution: rejected`

### ECO File Format

ECO files are written to `.axiom/eco/<ECO-code>.md` in the format specified by Architecture Section 7.4:

```markdown
## ECO-001: [ECO-DEP] Dependency Unavailable

**Filed:** 2026-04-05T20:46:16Z
**Status:** Approved
**Affected SRS Sections:** 1.3, FR-003, AC-005

### Environmental Issue
The library `passport-oauth2` specified in SRS 1.3 has been deprecated.

### Proposed Substitute
Replace with `arctic` (v2.1).

### Impact Assessment
- Affected references: 1.3, FR-003, AC-005
```

ECO files are append-only — each ECO gets its own file, and files are never overwritten or deleted.

### ECO-to-Task Integration

The existing data model supports ECO-driven task management:
- `Task.ECORef` — foreign key linking a task to the ECO that created it
- `TaskCancelledECO` status — tasks cancelled due to an ECO
- `task_srs_refs` table — maps tasks to SRS requirements for impact analysis

When an ECO is approved, the orchestrator identifies affected tasks and replans them. Unaffected completed work is preserved. This replanning logic is the orchestrator's responsibility (Phase 10+).

### What ECOs Are NOT

ECOs cannot be used for:
- Adding new features or requirements
- Changing acceptance criteria
- Expanding or reducing project scope
- Altering the fundamental architecture
- User preference changes

Any such change requires canceling the project and starting a new one.

## Events

Phase 9 added three new authoritative event types:

| Event | Emitted By | Details |
|-------|-----------|---------|
| `srs_submitted` | `SubmitSRS` | Run ID |
| `srs_approved` | `ApproveSRS` | Run ID, SRS hash |
| `srs_rejected` | `RejectSRS` | Run ID, rejection feedback |

ECO events were already defined in Phase 3:

| Event | Emitted By | Details |
|-------|-----------|---------|
| `eco_proposed` | `ProposeECO` | ECO ID, code, category |
| `eco_resolved` | `ApproveECO`/`RejectECO` | ECO ID, code, resolution (approved/rejected) |

## Test Coverage

| Package | Tests | Coverage |
|---------|-------|----------|
| `internal/srs` | 17 | Structure validation (8), bootstrap context (3), draft persistence (5), hash computation (1) |
| `internal/eco` | 13 | Category validation (3), proposal validation (5), file persistence (5) |
| `internal/engine` (SRS) | 9 | Submit (3), approve (2), reject (2), round-trip (1), immutability (1) |
| `internal/engine` (ECO) | 7 | Propose (3), approve (2), reject (2) |

## API Reference

### SRS Package (`internal/srs/`)

```go
// Validate SRS structure against required sections
func ValidateStructure(content string) error

// Build bootstrap context for SRS generation
func BuildBootstrapContext(projectRoot string, isGreenfield bool) (*BootstrapContext, error)

// Persist/read/delete SRS drafts
func WriteDraft(projectRoot, runID, content string) error
func ReadDraft(projectRoot, runID string) (string, error)
func DeleteDraft(projectRoot, runID string) error

// Compute SHA-256 hash
func ComputeHash(content []byte) string
```

### ECO Package (`internal/eco/`)

```go
// Validate ECO category codes
func ValidCategory(code string) bool
func CategoryDescription(code string) string

// Validate a full ECO proposal
func ValidateProposal(p Proposal) error

// Write/list ECO files under .axiom/eco/
func WriteECOFile(projectRoot string, r Record) error
func ListECOFiles(projectRoot string) ([]string, error)
```

### Engine SRS Methods

```go
func (e *Engine) SubmitSRS(runID string, content string) error
func (e *Engine) ApproveSRS(runID string) error
func (e *Engine) RejectSRS(runID string, feedback string) error
```

### Engine ECO Methods

```go
func (e *Engine) ProposeECO(proposal ECOProposal) (int64, error)
func (e *Engine) ApproveECO(ecoID int64, approvedBy string) error
func (e *Engine) RejectECO(ecoID int64) error
```
