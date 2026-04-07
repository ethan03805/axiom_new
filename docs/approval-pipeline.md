# Approval Pipeline Reference

The approval pipeline is the mechanism by which Meeseeks output moves from container staging to the project filesystem. The package-level implementation models the full five-stage architecture pipeline, and all pipeline operations are intended to be executed by the Trusted Engine.

Current runtime note: the live engine now wires the scheduler, execution worker, and merge queue together. Ready tasks are dispatched by the scheduler, executed by the engine's attempt executor, passed through manifest validation / sandbox checks / review, and then committed through the serialized merge queue.

Per Architecture Section 14.2, the pipeline has five stages:

```
Stage 1: Manifest Validation (internal/manifest/)
  ↓
Stage 2: Validation Sandbox (internal/validation/)
  ↓
Stage 3: Reviewer Evaluation (internal/review/)
  ↓
Stage 4: Orchestrator Gate (internal/review/)
  ↓
Stage 5: Merge Queue (internal/mergequeue/ — Phase 12)
```

## Stage 1: Manifest Validation

**Package:** `internal/manifest/`

Every Meeseeks emits a `manifest.json` alongside its output files in `/workspace/staging/`. The engine validates this manifest before any output enters the validation sandbox.

### Manifest Format

Per Architecture Section 10.4:

```json
{
    "task_id": "task-042",
    "base_snapshot": "abc123def",
    "files": {
        "added": [
            {"path": "src/handlers/auth.go", "binary": false},
            {"path": "public/logo.png", "binary": true, "size_bytes": 24576}
        ],
        "modified": [
            {"path": "src/routes/api.go", "binary": false}
        ],
        "deleted": ["src/handlers/old_auth.go"],
        "renamed": [
            {"from": "src/utils/hash.go", "to": "src/crypto/hash.go"}
        ]
    }
}
```

Binary files include a `size_bytes` field. Renames are first-class operations — they are never degraded into synthetic delete-plus-add pairs in persistent audit records.

### Parsing

`ParseManifest(data []byte) (*Manifest, error)` parses JSON into a typed `Manifest` struct. Required fields are validated:

- `task_id` must be non-empty
- `base_snapshot` must be non-empty

### Validation Checks

`ValidateManifest(m *Manifest, stagingDir string, allowedScope []string, cfg ValidationConfig) []error` performs all Stage 1 checks from Architecture Section 14.2:

| Check | Description |
|-------|-------------|
| **File existence** | All files listed in manifest exist in staging |
| **No unlisted files** | No files in staging are unlisted in manifest (`manifest.json` itself is excluded) |
| **Path canonicalization** | No path traversal (`../`), no absolute paths |
| **Symlink rejection** | Symlinks detected via `os.Lstat` are rejected |
| **Non-regular file rejection** | Device files, FIFOs, and other special files are rejected |
| **File size limits** | Files exceeding `MaxFileSizeBytes` (default 50 MB) are rejected |
| **Scope enforcement** | If `allowedScope` is non-nil, all paths must match at least one scope prefix |
| **Duplicate detection** | Duplicate paths within the manifest are rejected |
| **Deleted path safety** | Deleted paths are checked for traversal |
| **Rename path safety** | Both "from" and "to" paths of renames are checked for traversal |

A nil `allowedScope` means unrestricted — any path is allowed. The function returns a slice of all errors found (not just the first).

### Configuration

```go
type ValidationConfig struct {
    MaxFileSizeBytes int64 // 0 means no limit; default 50 MB
}
```

### Artifact Hash Tracking

`ComputeArtifacts(m *Manifest, stagingDir string, attemptID int64) ([]ArtifactRecord, error)` computes SHA-256 hashes and file sizes for each file operation:

| Operation | PathFrom | PathTo | SHA256After | SizeAfter |
|-----------|----------|--------|-------------|-----------|
| `add` | nil | file path | computed | computed |
| `modify` | nil | file path | computed | computed |
| `delete` | file path | nil | nil | nil |
| `rename` | old path | new path | computed from "to" | computed from "to" |

Hashing uses streaming I/O (`io.Copy` into `sha256.New()`) to avoid loading large files entirely into memory. Each `ArtifactRecord` includes the `AttemptID` for audit persistence via `state.TaskArtifact`.

### Path Helpers

- `AllOutputPaths()` — files that should exist in staging (added, modified, rename "to" paths)
- `AllReferencedPaths()` — all paths in the manifest including deleted and rename "from" paths

### Test Coverage (23 tests)

- Parsing: valid JSON, empty files, invalid JSON, missing task_id, missing base_snapshot
- Validation: file existence, unlisted files, path traversal, absolute paths, oversized files, scope enforcement (restricted and unrestricted), duplicate paths, rename tracking, deleted path safety, rename path safety
- Artifacts: added, modified, deleted, renamed file tracking with SHA-256 verification
- Path helpers: AllOutputPaths, AllReferencedPaths

---

## Stage 2: Validation Sandbox

**Package:** `internal/validation/`

The validation sandbox runs automated checks (compilation, linting, tests) against untrusted Meeseeks output in isolated Docker containers. This prevents malicious or broken generated code from executing in the trusted environment.

### Sandbox Specification

Per Architecture Section 13.3:

- **Base:** read-only snapshot of project at current HEAD
- **Overlay:** writable layer with Meeseeks output applied
- **Network:** NONE (no outbound access) — hardcoded, not configurable
- **Secrets:** NONE (no API keys, tokens, credentials)
- **Resources:** CPU + memory limited per config
- **Timeout:** configurable (default 10 minutes)

### Container Spec Building

`BuildSandboxSpec(params SandboxParams) engine.ContainerSpec` constructs a container spec:

- Container name: `axiom-validator-<task-id>`
- Network is always `"none"` regardless of config (security invariant per Section 13.3)
- Project directory mounted read-only at `/workspace/project`
- Staging directory mounted read-write at `/workspace/staging`
- Environment variables: `AXIOM_CONTAINER_TYPE=validator`, `AXIOM_TASK_ID`, `AXIOM_RUN_ID`
- CPU limit, memory limit, and timeout from `config.ValidationConfig`

### Language Detection

`DetectLanguages(projectDir string) []string` inspects the project directory for language markers:

| Marker File | Language |
|-------------|----------|
| `go.mod` | `go` |
| `package.json` | `node` |
| `requirements.txt` | `python` |
| `pyproject.toml` | `python` |
| `setup.py` | `python` |
| `Cargo.toml` | `rust` |

Results are sorted alphabetically for deterministic ordering.

### Language-Specific Validation Profiles

Per Architecture Section 13.5, each language ecosystem has specific dependency handling and validation commands:

| Profile | Compile | Lint | Test | Dependency Strategy |
|---------|---------|------|------|-------------------|
| **Go** | `go build ./...` | `golangci-lint run ./...` | `go test ./...` | Vendored modules or read-only GOMODCACHE |
| **Node** | `npx tsc --noEmit` | `npx eslint .` | `npm test` | `npm ci --ignore-scripts --offline` |
| **Python** | `python -m py_compile` | `ruff check .` | `python -m pytest` | `pip install --no-index --find-links` |
| **Rust** | `cargo build` | `cargo clippy -- -D warnings` | `cargo test` | Pre-populated cargo registry |

`GetProfile(lang string) Profile` returns the profile for a language. Unknown languages return an empty profile.

### Service Orchestration

`Service.RunChecks(ctx, req CheckRequest) ([]CheckResult, error)` orchestrates the full validation:

1. Build container spec from request parameters
2. Start the validation sandbox container
3. Run checks via the `CheckRunner` interface
4. Collect results
5. Destroy the sandbox container (guaranteed via `defer`)

The `CheckRunner` interface abstracts the actual execution of checks inside the container, allowing tests to inject mock runners.

Current wiring: the executor and merge queue both call into the validation service at runtime. The default app composition wires `validation.DockerCheckRunner` (`internal/validation/runner_docker.go`), which runs the language-specific profile commands (`go build ./...`, `golangci-lint run ./...`, `go test ./...`, and the Node/Python/Rust equivalents from `GetProfile`) inside the sandbox container via `engine.ContainerService.Exec` (`docker exec`). Each command is wrapped as `sh -c "cd /workspace/project && <cmd>"` so both stage 2 (with a staging overlay) and stage 5 (only `/workspace/project` mounted) evaluate against the mounted project root.

A fail-closed `FallbackRunner` is still used as an explicit opt-out when Docker is unavailable (no `docker.image` configured in `.axiom/config.toml`) or when the operator sets `AXIOM_VALIDATION_DISABLED=1`. In both cases the runner emits a failing `compile` check so the merge queue cannot silently commit.

### Configuration

From `.axiom/config.toml`:

```toml
[validation]
timeout_minutes = 10
cpu_limit = 1.0
mem_limit = "4g"
network = "none"                    # MUST be "none"
allow_dependency_install = true
security_scan = false               # optional trivy/gosec
dependency_cache_mode = "prefetch"
fail_on_cache_miss = true           # never fetch from network during validation
```

When `security_scan = false` (default), the security check is skipped. When `fail_on_cache_miss = true` and a dependency cache is missing, validation fails with a structured `dependency_cache_miss` result.

### Result Aggregation

```go
type CheckResult struct {
    CheckType  state.ValidationCheckType // compile, lint, test, security
    Status     state.ValidationStatus    // pass, fail, skip
    Output     string
    DurationMs int64
}
```

- `AllPassed(results) bool` — returns true if no check has status `fail`
- `FormatResults(results) string` — produces a human-readable summary for inclusion in the ReviewSpec (per Section 13.9)

### Test Coverage (22 tests)

- Language detection: Go, Node, Python, Rust, multi-language, empty project
- Profiles: Go, Node, Python, Rust, unknown language
- Sandbox spec: config mapping, network always none
- Result aggregation: all pass, one fail, empty
- Service: container start failure, successful check run, security scan skip/include, dependency cache miss

---

## Stage 3: Reviewer Evaluation

**Package:** `internal/review/`

The engine spawns a reviewer container with a ReviewSpec containing the original TaskSpec, Meeseeks output, and validation results. The reviewer evaluates and returns APPROVE or REJECT with feedback.

Phase 18 hardens this handoff: repo-derived content in TaskSpecs and ReviewSpecs is wrapped as untrusted data with source provenance, and instruction-like comments are sanitized before the reviewer sees them.

### Risky File Detection

Per Architecture Section 11.6, certain file types require elevated review regardless of task tier. `IsRiskyFile(path string) bool` checks against these patterns:

| Category | Patterns |
|----------|----------|
| **CI/CD** | `.github/workflows/`, `.gitlab-ci`, `Jenkinsfile`, `.circleci/` |
| **Package manifests** | `package.json`, `go.mod`, `go.sum`, `requirements.txt`, `Cargo.toml`, `Cargo.lock`, lockfiles |
| **Infrastructure** | `Dockerfile`, `docker-compose*`, `*.tf`, `*.tfvars` |
| **Build scripts** | `Makefile`, `CMakeLists.txt`, `build.gradle*`, `scripts/` |
| **Auth/Security** | Paths containing `/auth/`, `/security/`, `/crypto/` |
| **Migrations** | Paths containing `migration` |

`FindRiskyFiles(paths []string) []string` returns the subset of paths matching risky patterns.

### Reviewer Tier Escalation

`ReviewerTier(taskTier, riskyFiles) TaskTier` applies escalation rules:

- If risky files are present and task tier is `local` or `cheap`, escalate to `standard`
- `standard` and `premium` tiers are unchanged (already at or above the minimum)
- No risky files: use the original task tier

### Model Family Diversification

Per Architecture Section 11.3:

- `RequiresDiversification(tier) bool` — returns true for `standard` and `premium` tiers
- For `local` and `cheap` tiers, diversification is optional

`SelectReviewerModel(models, meeseeksFamily, tier) (*ModelInfo, error)` selects a reviewer:

1. If diversification is required, prefer a model from a different family than the Meeseeks
2. If no alternative family exists, fall back to the same family (best-effort)
3. If no models are available at all, return an error

### Verdict Parsing

`ParseVerdict(output string) (ReviewVerdict, string)` extracts the verdict from reviewer output:

- Looks for `### Verdict: APPROVE | REJECT` (case-insensitive)
- Captures feedback from the `### Feedback (if REJECT)` section
- For REJECT verdicts with no explicit feedback section, all content after the verdict line is captured as feedback
- Malformed output defaults to REJECT (fail-safe)

### Reviewer Container Spec

`BuildReviewContainerSpec(params) ContainerSpec` constructs the container spec:

- Container name: `axiom-reviewer-<task-id>`
- Network: `none` (Section 11.8: no network)
- Spec directory mounted read-only at `/workspace/spec`
- No project filesystem mount (Section 11.8)
- Environment: `AXIOM_CONTAINER_TYPE=reviewer`, `AXIOM_TASK_ID`, `AXIOM_RUN_ID`

### Service Orchestration

`Service.RunReview(ctx, req ReviewRequest) (*ReviewResult, error)` orchestrates the full review:

1. **Detect risky files** — scan affected files for risky patterns
2. **Escalate tier** — if risky files found, escalate local/cheap to standard
3. **Select reviewer model** — via `ModelSelector` interface with family diversification
4. **Start reviewer container** — build spec and start via `ContainerService`
5. **Run review** — collect output via `ReviewRunner` interface
6. **Parse verdict** — extract APPROVE/REJECT and feedback
7. **Destroy container** — guaranteed via `defer`

Returns a `ReviewResult` with verdict, feedback, reviewer model/family, and effective tier.

Current wiring note: the executor now invokes the review service in the live runtime. The default app composition currently uses a fail-closed fallback reviewer runner until a concrete reviewer runtime is configured.

### Prompt-Safe Review Payloads

The review pipeline now relies on the shared phase-18 prompt-safety layer:

- TaskSpec context blocks are written as `<untrusted_repo_content>` blocks
- Meeseeks output in the ReviewSpec is also wrapped as untrusted content
- source paths and line ranges are preserved where available
- secret-heavy blocks are excluded rather than forwarded verbatim

This keeps reviewer instructions separate from repository text and reduces the chance of prompt injection through comments or generated code.

### Test Coverage (29 tests)

- Risky file detection: CI/CD, package manifests, infrastructure/security, build scripts
- Tier escalation: standard unchanged, local no risky, local with risky, cheap with risky, premium stays premium
- Diversification: standard required, premium required, local not required, cheap not required
- Model selection: diversified, no diversification needed, no models, all same family fallback
- Verdict parsing: approve, reject with feedback, malformed defaults to reject, case insensitive
- Container spec: correct network/env/mounts
- Service: approve flow, reject flow, container start failure, risky file escalation
- Orchestrator gate: approve pass-through, reject pass-through
- FindRiskyFiles: mixed file list filtering

---

## Stage 4: Orchestrator Gate

**Package:** `internal/review/`

`OrchestratorGate(req GateRequest) GateResult` implements the final approval gate per Architecture Section 14.2 Stage 4. The orchestrator validates the approved output against SRS requirements.

Currently a pass-through for reviewer decisions — if the reviewer approves, the gate approves. Future versions will include SRS cross-validation via IPC for more sophisticated orchestrator reasoning.

```go
type GateRequest struct {
    Verdict  state.ReviewVerdict
    Feedback string
}

type GateResult struct {
    Approved bool
    Feedback string
}
```

---

## Stage 5: Merge Queue

**Package:** `internal/mergequeue/`

The serialized merge queue ensures every commit is validated against the actual current project state. Only one merge is processed at a time, preventing stale-context conflicts. The engine adapter delegates merge-time integration checks to the configured validation service (`validation.DockerCheckRunner` by default) and advances attempt phases through `merging` and `succeeded` / `failed`. Per Architecture Section 23.3, a merge is only committed when `DockerCheckRunner` returns no failing results for every language profile command run inside the sandbox — a broken `go build`, `go test`, or `golangci-lint run` prevents the commit and requeues the task with structured feedback.

### Merge Process (Architecture Section 16.4)

```
1. Receive approved output from approval pipeline (Enqueue)
2. Validate base_snapshot against current HEAD
3. If stale: check for actual file conflicts via git diff
   - No conflicts: proceed with integration checks
   - Conflicts: requeue task with updated context
4. Apply Meeseeks output to project (copy files, delete, rename)
5. Run project-wide integration checks (build, test, lint)
6. If integration fails: revert applied files, requeue task with failure feedback
7. If integration passes: stage files and commit with architecture-compliant message
8. Re-index changed files via semantic indexer
9. Release write-set locks
10. Mark task done (dependent tasks unblocked by scheduler)
```

### Key Types

- **`MergeItem`** — Represents an approved task output ready to merge. Includes task metadata, staging directory, commit info, and file operations (add, delete, rename).
- **`Queue`** — The serialized merge queue. Thread-safe with mutex-protected item list. Processes one item per `Tick()`.
- **`CommitInfo`** — Metadata for architecture-compliant commit messages (Section 23.2).

### Interfaces

The merge queue uses dependency injection for all external operations:

| Interface | Purpose |
|-----------|---------|
| `GitOps` | HEAD checks, file staging, commits, changed-file detection |
| `Validator` | Project-wide integration checks (build/test/lint) |
| `Indexer` | Incremental re-indexing after commits |
| `LockReleaser` | Write-set lock release after merge or failure |
| `TaskCompleter` | Task status transitions (done or requeue) |
| `EventEmitter` | Engine event publication |

### Failure Handling

On any failure (conflict, integration check failure, commit failure), the merge queue:
1. Reverts any files written to the project directory
2. Releases write-set locks
3. Requeues the task with structured failure feedback
4. Emits a `merge_failed` event

Integration failure feedback is stored on the latest attempt record so the next TaskSpec includes it (Sections 23.3, 30.2).

### Conflict Detection

When the base_snapshot differs from current HEAD, the merge queue uses `git diff --name-only` to determine which files actually changed. Only output files that overlap with genuinely changed files are treated as conflicts. This avoids excessive requeuing when unrelated files have been committed by other tasks.

---

## Pipeline Flow on Failure

Per Architecture Section 14.2, failures at each stage trigger a retry cycle:

| Stage | On Failure |
|-------|-----------|
| **Manifest validation** | Attempt fails immediately; no validation or review |
| **Validation sandbox** | Errors packaged as structured feedback; fresh Meeseeks spawned with original spec + failure feedback; max 3 retries per tier before escalation |
| **Reviewer rejection** | Fresh Meeseeks spawned with original spec + reviewer feedback; reviewer container also destroyed; new reviewer for revision |
| **Orchestrator rejection** | Fresh Meeseeks spawned with original spec + orchestrator feedback |
| **Merge queue conflict** | Task requeued with updated context from current HEAD |

The retry/escalation logic is handled by the task service (`internal/task/`) and scheduler (`internal/scheduler/`). The approval pipeline packages provide the results and feedback that drive those decisions.

## Post-Merge: Test-Generation Separation (Phase 13)

After an implementation task successfully merges, the test-generation system (Architecture Section 11.5) provides the service used to create a separate test task from a different model family:

1. `testgen.CreateTestTask(implTaskID)` creates a test-type task dependent on the implementation.
2. The implementation's model family is recorded in a `convergence_pairs` record for exclusion.
3. The scheduler dispatches the test task with `excludeFamily` set, ensuring a different model family.
4. If tests fail, `testgen.HandleTestFailure()` creates an implementation-fix task with failure context.
5. The fix task goes through the full approval pipeline (manifest → validation → review → merge).
6. A feature is not considered done until `testgen.IsFeatureDone()` returns true (convergence achieved).

Current engine note: the service and scheduler hooks are implemented, but merge-queue success does not yet call `CreateTestTask` automatically.

See [Test-Generation Separation Reference](test-generation.md) for the full API.

---

## Attempt Phase Tracking

The approval pipeline advances attempt phases through the lifecycle defined in `state.AttemptPhase`:

```
executing → validating → reviewing → awaiting_orchestrator_gate → queued_for_merge → merging → succeeded
                ↓              ↓                   ↓                      ↓              ↓
              failed         failed              failed                 failed          failed
```

Phase transitions are enforced by `state.ValidPhaseTransition()` and persisted via `state.DB.UpdateAttemptPhase()`.

---

## Integration Points

| Component | How It Connects |
|-----------|----------------|
| `internal/state/` | `ValidationRun`, `ReviewRun`, `TaskArtifact` records persisted per attempt |
| `internal/events/` | Events emitted at key pipeline stages (attempt phase changes) |
| `internal/container/` | `ContainerService` interface used for sandbox and reviewer containers |
| `internal/engine/` | `ContainerSpec` type used for sandbox and reviewer specs |
| `internal/ipc/` | `ReviewSpec` written to spec directory for reviewer containers |
| `internal/config/` | `ValidationConfig` drives sandbox parameters |
| `internal/task/` | `HandleTaskFailure` routes validation/review failures to retry/escalation |
| `internal/scheduler/` | Scheduler dispatches tasks; pipeline results determine next steps |
