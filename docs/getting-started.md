# Getting Started with Axiom

## Prerequisites

- **Go 1.25+** (module requires Go 1.25)
- **Git** (any recent version)
- **Docker** (required for container isolation — Meeseeks, reviewers, and validation sandboxes run in Docker). The merge queue will refuse to commit until the validation sandbox can run `docker exec` successfully: `validation.DockerCheckRunner` runs the language-specific build/test/lint commands inside the sandbox for every merge, and failures (or infra errors) block the commit and requeue the task. This is how Axiom guarantees every commit has passed a real build, test, and lint.

## Installation

### From Source

```bash
git clone https://github.com/ethan03805/axiom_new.git
cd axiom_new
go build -o ~/bin/axiom ./cmd/axiom
```

Or using the Makefile (which injects version info):

```bash
make build        # builds to bin/axiom
make install      # installs to $GOPATH/bin
```

### Verify Installation

```bash
axiom version
# axiom dev (abc1234) built 2026-04-05T... windows/amd64
```

### Run Diagnostics

Before initializing a project, verify the local runtime dependencies:

```bash
axiom doctor
```

`axiom doctor` checks Docker, BitNet configuration/availability, provider reachability, local CPU pressure, cache readiness, and secret-scanner initialization. It works both inside and outside a project.

## Quick Start

### 1. Initialize a Project

Navigate to any git repository and run:

```bash
cd /path/to/your/project
axiom init --name "my-project"
```

This creates a `.axiom/` directory with:
- `config.toml` — project configuration (committed to git)
- `axiom.db` — SQLite runtime state (gitignored)
- `models.json` — model capability index (committed to git)
- `.gitignore` — excludes ephemeral runtime state
- Subdirectories for containers, validation, ECOs, and logs

### 2. Check Project Status

```bash
axiom status
```

### 3. Directory Name as Default

If you omit `--name`, the directory name is used:

```bash
mkdir my-app && cd my-app
git init
axiom init
# Project name defaults to "my-app", slug to "my-app"
```

## Configuration

The generated `.axiom/config.toml` contains all configuration with architecture defaults. See [Configuration Reference](configuration.md) for details.

User-wide defaults and secrets such as the OpenRouter API key live in `~/.axiom/config.toml`. Optional managed BitNet launch settings also belong there when you want `axiom bitnet start` / `stop` to control a local server process.

### Set Your OpenRouter API Key Before `axiom run`

The default `orchestrator.runtime` is `claw`, which implies cloud
meeseeks. As of the Issue 07 fix, `axiom` now validates the inference
plane at startup and refuses to open a project when the selected runtime
requires a cloud provider but no key is set:

```text
no inference provider available for configured orchestrator runtime: runtime "claw" requires an openrouter API key
```

To resolve, add your OpenRouter key to the global config (keeping
secrets out of the project config per Architecture §29.4):

```toml
# ~/.axiom/config.toml
[inference]
openrouter_api_key = "sk-or-v1-..."
```

After the first successful startup, you should see a single INFO log
line summarizing the plane state:

```text
inference plane ready providers=[openrouter] budget_max_usd=10 log_prompts=false runtime=claw
```

The API key itself never appears in the log line. See [Operations &
Diagnostics Reference § Inference Plane Startup Health Check](operations-diagnostics.md#inference-plane-startup-health-check)
for the full set of startup outcomes.

See [Operations & Diagnostics Reference](operations-diagnostics.md) for startup recovery, prompt logs, `axiom doctor`, and managed BitNet behavior.

## Secret Handling and Prompt Safety

Phase 18 makes prompt packaging safer by default:

- repository text is treated as untrusted data
- `.axiom/`, `.env*`, and log files are excluded from prompt context
- detected secrets are redacted before model requests are sent
- secret-bearing requests route to local inference by default
- security-critical code such as auth or crypto can still use external models when the payload itself is safe

Task and review prompts now wrap repo-derived content in explicit `<untrusted_repo_content>` blocks with source paths and line ranges. This keeps instructions separate from repository data and reduces prompt-injection risk from comments or generated files.

See [Security, Secret Handling, and Prompt Safety](security-prompt-safety.md) for the full behavior and configuration model.

## Generate Runtime Instructions

If you want Claude Code, Codex, OpenCode, or a Claw runtime to use Axiom deterministically, generate the runtime instruction artifacts for that runtime:

```bash
axiom skill generate --runtime codex
```

This writes runtime-specific instruction files into the repository so the orchestrator is taught to route work through Axiom instead of directly implementing the task itself.

Current operating model: Axiom does not auto-launch an embedded orchestrator. After `axiom run`, you appoint and launch a Claw, Claude Code, Codex, or OpenCode runtime yourself and point it at the repo and/or API; that external orchestrator owns prompt-to-SRS generation for now.

Re-run the command after changing `.axiom/config.toml`, especially `[api].port`, `[budget]`, `[git].branch_prefix`, or the selected orchestrator runtime.

See [Runtime Skill System Reference](runtime-skills.md) for the generated files and per-runtime behavior.

## Project Structure

After initialization, your project will contain:

```
your-project/
├── .axiom/
│   ├── config.toml          # Project configuration (committed)
│   ├── axiom.db             # SQLite state (gitignored)
│   ├── models.json          # Model capability index (committed)
│   ├── .gitignore           # Excludes runtime state
│   ├── containers/          # Ephemeral container data (gitignored)
│   │   ├── specs/           # TaskSpec/ReviewSpec per task
│   │   ├── staging/         # Meeseeks output staging per task
│   │   └── ipc/             # JSON IPC message dirs per task
│   │       └── <task-id>/
│   │           ├── input/   # Engine -> Container messages
│   │           └── output/  # Container -> Engine messages
│   ├── validation/          # Validation sandbox data (gitignored)
│   ├── eco/                 # Engineering Change Orders (committed)
│   └── logs/                # Runtime logs (gitignored)
│       └── prompts/
├── src/                     # Your source code
└── ...
```

The `.axiom/logs/prompts/` directory is populated only when prompt logging is enabled.

## Git Hygiene

Axiom automatically manages `.gitignore` entries inside `.axiom/`:

| Path | Git Status | Reason |
|------|-----------|--------|
| `.axiom/config.toml` | Committed | Shared project configuration |
| `.axiom/models.json` | Committed | Reproducible model selection |
| `.axiom/eco/*.md` | Committed | Audit trail |
| `.axiom/axiom.db` | Gitignored | Machine-specific runtime state |
| `.axiom/containers/` | Gitignored | Ephemeral container data |
| `.axiom/validation/` | Gitignored | Ephemeral sandbox data |
| `.axiom/logs/` | Gitignored | Runtime logs |

Prompt logs are written only when `observability.log_prompts = true`.

## Git Branch Strategy

Architecture target branch names follow:

```
axiom/<project-slug>
```

The branch name is deterministic — given the same project slug, the branch name is always the same.

**Full lifecycle:**

1. **Start** — `axiom run "<prompt>"` validates that the working tree is clean (Architecture §28.2), then creates (or resumes) the `axiom/<slug>` branch and switches the repo onto it. All task work lands on this branch, never on the base branch.
2. **Dirty-tree refusal** — if the working tree has uncommitted changes, `axiom run` exits non-zero with a message naming the condition. Pass `--allow-dirty` for crash-recovery scenarios where resuming on a branch with uncommitted state is intentional.
3. **Task commits** — meeseeks output lands as individual commits on the work branch, each with an architecture-compliant commit message (§23.2).
4. **Completion** — the user reviews the work branch diff (`git diff main...axiom/<slug>`) and merges at their discretion. Axiom never pushes, pulls, or merges automatically.
5. **Cancellation** — `axiom cancel` reverts any uncommitted changes on the work branch, returns the repo to the base branch, and stops any running containers. Committed work on the `axiom/<slug>` branch is preserved per §23.4 — the branch is not deleted. `axiom cancel` works from every non-terminal state, including `draft_srs` and `awaiting_srs_approval`.

See [Git Operations Reference](git-operations.md) for implementation details.

## Container Architecture

Axiom runs all untrusted agents (Meeseeks workers, reviewers, validators) in Docker containers with strict isolation. Containers:

- Have **no network access** (`--network=none`)
- Have **no project filesystem mount** (only spec, staging, and IPC dirs are mounted)
- Run as **non-root** (`--user 1000:1000`)
- Use a **read-only root filesystem** (`--read-only`)
- Communicate with the engine exclusively via **filesystem IPC** (JSON files)

See [IPC & Container Lifecycle Reference](ipc-container.md) for details.

## SRS and ECO Workflow

Before autonomous execution begins, the orchestrator generates a Software Requirements Specification (SRS) that must be approved. The SRS is the immutable scope contract for the run.

Current operating model: the initial SRS must come from a user-appointed external orchestrator. Axiom does not auto-bootstrap or auto-launch one in live app flows today.

**SRS flow:** The orchestrator generates an SRS draft → the engine validates its structure → the user reviews and approves or rejects → on approval, the SRS is written as a read-only file with SHA-256 integrity verification → the run transitions to active.

**ECO flow:** If environmental issues arise during execution (broken dependencies, API changes), the orchestrator proposes an Engineering Change Order (ECO). ECOs are strictly limited to 6 categories (dependency, API, security, platform, license, provider). ECOs are recorded as append-only markdown files under `.axiom/eco/` and never modify the original SRS.

Current implementation note: `axiom run` creates the run in `draft_srs`, but it does not auto-generate the SRS. The prompt handoff and submit-SRS path are still being completed, so do not expect the engine to bootstrap the first draft by itself.

See [SRS and ECO Reference](srs-eco.md) for the full API and lifecycle details.

## Task System and Scheduling

After the SRS is approved and the run becomes active, the orchestrator decomposes the SRS into a task tree. The engine's task system, scheduler, and executor handle:

- **Task creation** with dependency validation and cycle detection
- **Concurrent execution** with write-set locking (file, package, module, or schema scope)
- **Attempt execution** from TaskSpec creation through container IPC, validation, review, and merge enqueueing
- **Automatic retry** (up to 3 times per tier) and **escalation** (up to 2 tier bumps: local → cheap → standard → premium)
- **Lock conflict resolution** — tasks that need locked resources wait and are automatically requeued when locks are released

See [Task System, Scheduler, and Locking Reference](task-scheduler.md) for details.

## Approval Pipeline

After a Meeseeks completes a task, its output passes through a multi-stage approval pipeline before reaching the project filesystem:

1. **Manifest Validation** — the engine parses `manifest.json` from the Meeseeks output and validates all paths (no traversal, no symlinks, within scope, no oversized files)
2. **Validation Sandbox** — the engine runs compile, lint, and test checks in an isolated Docker container with no network and no secrets
3. **Reviewer Evaluation** — a separate LLM reviews the output against the original TaskSpec. For standard/premium tiers, the reviewer is from a different model family than the Meeseeks. Risky files (CI/CD, package manifests, Dockerfiles, auth code) always receive standard-tier or higher review
4. **Orchestrator Gate** — final approval before the merge queue

If any stage fails, the Meeseeks output is rejected and a fresh attempt is made with structured feedback.

See [Approval Pipeline Reference](approval-pipeline.md) for implementation details.

## Merge Queue

After a task's output passes through the approval pipeline (manifest validation, validation sandbox, reviewer evaluation, orchestrator gate), it enters the serialized merge queue. The merge queue ensures every commit is validated against the actual current project state:

1. Validates the task's `base_snapshot` against the current HEAD
2. If stale, checks for real file conflicts using `git diff`
3. Applies the Meeseeks output to the project directory
4. The package-level merge queue supports project-wide integration checks (build, test, lint)
5. On success: commits with an architecture-compliant message, re-indexes changed files, releases write-set locks, and marks the task done
6. On failure: reverts all applied files, requeues the task with structured failure feedback

Only one merge is processed at a time, preventing concurrent commit conflicts.

See [Approval Pipeline Reference](approval-pipeline.md) for implementation details.

## Test-Generation Separation

The test-generation service enforces the separate-task, different-model-family workflow from Architecture Section 11.5. This prevents circular validation — tests are not meaningful if the same model wrote both the code and the tests.

The lifecycle:
1. Implementation merges successfully via the merge queue.
2. The merge-queue adapter (`mergeQueueTaskAdapter.CompleteTask`) automatically calls `testgen.CreateTestTask` to spawn a dependent test task with the implementation's model family recorded on a `convergence_pairs` row.
3. The scheduler dispatches the test task with `excludeFamily` set, so it runs on a different model family (e.g., if implementation used Claude, tests use GPT).
4. If the test task merges successfully, the adapter automatically calls `testgen.MarkConverged` and the convergence pair transitions to `converged` — the feature is done.
5. If the test task's merge-queue integration checks reject the generated tests, `RequeueTask` routes the failure through `testgen.HandleTestFailure`, which spawns an implementation-fix task containing the failing test output. Once the fix task merges, the adapter recognises it as a fix task and marks the original pair converged.
6. If the test meeseeks exhausts all retries, `Engine.failAttempt` calls `testgen.MarkBlocked` on the pair.

A feature is not considered complete until both the implementation and its generated tests converge. This is tracked via convergence pairs in the database, and `Engine.CompleteRun` refuses to transition a run to `completed` while any pair is non-converged.

See [Test-Generation Separation Reference](test-generation.md) for implementation details.

## CLI Command Reference

After initialization, the full CLI surface is available. See [CLI Reference](cli-reference.md) for complete documentation.

### Project Lifecycle

```bash
axiom run "Build a REST API with auth"        # Create a run for external-orchestrator handoff
axiom run --budget 25 "Build a REST API"       # Same, with a specific budget ceiling
axiom status                                   # Show project status
axiom pause                                    # Pause execution
axiom resume                                   # Resume paused execution
axiom cancel                                   # Cancel execution
axiom export                                   # Export project state as JSON
```

Current workflow note: after `axiom run`, use your appointed external orchestrator to generate and submit the SRS draft. The CLI does not perform embedded bootstrap.

### Model Management

```bash
axiom models refresh                           # Update model registry
axiom models list                              # List all models
axiom models list --tier premium               # Filter by tier
axiom models list --family claude              # Filter by family
axiom models info <model-id>                   # Show model details
```

### BitNet (Local Inference)

```bash
axiom bitnet status                            # Show server status
axiom bitnet start                             # Start managed server (if configured)
axiom bitnet stop                              # Stop managed server
axiom bitnet models                            # List loaded models
```

If `[bitnet].command` is not configured, Axiom still supports manually run BitNet servers, but `axiom bitnet start` will tell you that manual setup is required.

### Diagnostics

```bash
axiom doctor                                   # Check Docker, BitNet, network, cache, and scanner state
```

### Semantic Index

```bash
axiom index refresh                            # Full re-index
axiom index query --type lookup_symbol --name MyFunc
axiom index query --type list_exports --package ./internal/config
axiom index query --type module_graph
```

### Interactive TUI

```bash
axiom tui                                      # Launch full-screen TUI
axiom tui --plain                              # Plain-text mode
```

### Session Management

```bash
axiom session list                             # List resumable sessions
axiom session resume <session-id>              # Resume a session
axiom session export <session-id>              # Export transcript
```

## Interactive TUI and Sessions

Axiom includes a full-screen interactive terminal UI (Phase 15) built on Bubble Tea:

```bash
axiom tui                      # Launch full-screen TUI
axiom tui --plain              # Plain-text startup frame (for non-TTY)
```

The TUI provides:
- **Status bar** — project name, session mode, branch, budget
- **Transcript viewport** — messages, system cards, and event notifications
- **Task rail** — live task counts (done, running, queued, failed)
- **Footer composer** — text input with slash command support

### Slash Commands

Type `/` followed by a command name. The TUI routes write operations
directly to the engine — you do not need to leave the terminal to
complete an `init → run → approve → execute → merge` slice:

```bash
# Bootstrap
/new "Build a REST API"      # Start a new run (or just type the prompt directly)

# Approval (after the external orchestrator submits an SRS draft)
/srs                         # View the SRS draft
/approve                     # Approve the SRS (run transitions to active)
/reject "needs section 4.2"  # Reject with feedback (run returns to draft_srs)

# Execution
/status                      # Show project status
/tasks                       # Show task breakdown
/diff                        # Preview `git diff <base>...<work>` (first 4 KB)
/budget                      # Show budget details
/pause                       # Pause the run
/cancel                      # Cancel the run (reverts uncommitted changes)

# Postrun
/diff                        # Review final changes
/resume                      # Resume a paused run
/new "<prompt>"              # Start another run

# Always
/help                        # Show all commands, grouped by mode
/clear                       # Clear transcript
```

**Worked example (greenfield project, all from inside `axiom tui`):**

1. `axiom init --name "My App"` and commit the `.axiom/` directory.
2. `axiom tui` → type `Build a REST API with JWT auth` and press Enter.
   The status bar shows `Run created: <id> on branch axiom/my-app`.
3. Your external orchestrator submits an SRS draft (via the API) — the
   TUI announces `SRS draft submitted` and transitions to approval mode.
4. Type `/srs` to view the draft, then `/approve` to accept it.
5. Watch task execution in the task rail; use `/pause`, `/diff`, and
   `/status` as needed.
6. When the run completes, use `/diff` to review the final changes on
   the work branch, then merge (or type `/cancel` to discard).

No `axiom srs approve` or `axiom run` invocation is required — the TUI
is a full-surface control plane, not just a status viewer.

### Session Management

Sessions persist across engine restarts:

```bash
axiom session list             # List resumable sessions
axiom session resume <id>      # Resume a specific session
axiom session export <id>      # Export session transcript
```

Sessions automatically track the current mode (bootstrap, approval, execution, postrun) based on run state. When resumed, the mode is refreshed.

See [Session & TUI Reference](session-tui.md) for full details.

## External Orchestration (API Server)

Axiom exposes a REST + WebSocket API server for external orchestrators (Claw, etc.):

```bash
# Generate an API token
axiom api token generate
# Output: axm_sk_<random-token>

# Start the API server
axiom api start
# Starting API server on port 3000...
```

This is currently the required orchestration path for SRS generation. You must appoint and launch the orchestrator yourself outside Axiom; Axiom does not auto-launch an embedded orchestrator in normal app flows.

The API provides:
- **REST endpoints** for project lifecycle (run creation / handoff, pause, resume, cancel), SRS/ECO approval, status queries, task trees, cost breakdown, events, and semantic index queries
- **Event WebSocket** (`ws://localhost:3000/ws/projects/:id`) for real-time project events
- **Control WebSocket** (`ws://localhost:3000/ws/projects/:id/control`) for external orchestrator action requests with idempotency support

All requests require a bearer token in the `Authorization` header. Tokens support `read-only` and `full-control` scopes with configurable expiration.

For remote access, Axiom supports Cloudflare Tunnel:

```bash
axiom tunnel start
# Output: https://<random>.trycloudflare.com
```

See [API Server Reference](api-server.md) for the full endpoint documentation.

## What's Next

Available now:

- `axiom skill generate --runtime <claw|claude-code|codex|opencode>` — generate runtime instruction artifacts that force supported orchestrators to stay inside the Axiom workflow

- [Runtime Skill System Reference](runtime-skills.md) - detailed artifact list, enforcement strategy, and regeneration guidance

- [Security, Secret Handling, and Prompt Safety](security-prompt-safety.md) - secret scanning, local-only routing defaults, and prompt-safe spec packaging

- [Operations & Diagnostics Reference](operations-diagnostics.md) - startup recovery, doctor checks, prompt logs, and managed BitNet operations

- [Release Packaging Reference](release-packaging.md) - candidate bundle layout, fixture repos, manifest structure, and test-matrix packaging

See the [Architecture Document](../ARCHITECTURE.md) and [Implementation Plan](../IMPLEMENTATION_PLAN.md) for the full roadmap.
