# Getting Started with Axiom

## Prerequisites

- **Go 1.25+** (module requires Go 1.25)
- **Git** (any recent version)
- **Docker** (required for container isolation — Meeseeks, reviewers, and validation sandboxes run in Docker)

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

## Git Branch Strategy

When a run starts, Axiom creates a dedicated work branch:

```
axiom/<project-slug>
```

All task commits are made to this branch. Your current branch is never modified during execution. When the run completes, you review the full diff and merge at your discretion. Axiom never pushes or merges to remote automatically.

The work branch is deterministic — given the same project slug, the branch name is always the same. If a run is resumed after a pause or crash, Axiom detects the existing branch and checks it out.

**Important:** Axiom requires a clean working tree before starting a run. Commit or stash any uncommitted changes first.

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

**SRS flow:** The orchestrator generates an SRS draft → the engine validates its structure → the user reviews and approves or rejects → on approval, the SRS is written as a read-only file with SHA-256 integrity verification → the run transitions to active.

**ECO flow:** If environmental issues arise during execution (broken dependencies, API changes), the orchestrator proposes an Engineering Change Order (ECO). ECOs are strictly limited to 6 categories (dependency, API, security, platform, license, provider). ECOs are recorded as append-only markdown files under `.axiom/eco/` and never modify the original SRS.

See [SRS and ECO Reference](srs-eco.md) for the full API and lifecycle details.

## Task System and Scheduling

After the SRS is approved and the run becomes active, the orchestrator decomposes the SRS into a task tree. The engine's task system and scheduler handle:

- **Task creation** with dependency validation and cycle detection
- **Concurrent execution** with write-set locking (file, package, module, or schema scope)
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
4. Runs project-wide integration checks (build, test, lint)
5. On success: commits with an architecture-compliant message, re-indexes changed files, releases write-set locks, and marks the task done
6. On failure: reverts all applied files, requeues the task with structured failure feedback

Only one merge is processed at a time, preventing concurrent commit conflicts.

See [Approval Pipeline Reference](approval-pipeline.md) for implementation details.

## Test-Generation Separation

After an implementation task merges, Axiom creates a separate test-generation task from a **different model family** (Architecture Section 11.5). This prevents circular validation — tests are not meaningful if the same model wrote both the code and the tests.

The lifecycle:
1. Implementation merges successfully via the merge queue.
2. A test-generation task is created, dependent on the implementation, with the implementation's model family excluded.
3. The test task is dispatched to a different model family (e.g., if implementation used Claude, tests use GPT).
4. If tests pass, the feature is marked as converged (done).
5. If tests fail, an implementation-fix task is created with the failing test output as context, and the fix goes through the full approval pipeline.

A feature is not considered complete until both the implementation and its generated tests converge. This is tracked via convergence pairs in the database.

See [Test-Generation Separation Reference](test-generation.md) for implementation details.

## CLI Command Reference

After initialization, the full CLI surface is available. See [CLI Reference](cli-reference.md) for complete documentation.

### Project Lifecycle

```bash
axiom run "Build a REST API with auth"        # Start a new run
axiom run --budget 25 "Build a REST API"       # Start with specific budget
axiom status                                   # Show project status
axiom pause                                    # Pause execution
axiom resume                                   # Resume paused execution
axiom cancel                                   # Cancel execution
axiom export                                   # Export project state as JSON
```

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
axiom bitnet start                             # Start server
axiom bitnet stop                              # Stop server
axiom bitnet models                            # List loaded models
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

Type `/` followed by a command name:

```bash
/status    # Show project status
/tasks     # Show task breakdown
/budget    # Show budget details
/srs       # View SRS state
/diff      # Preview latest changes
/help      # Show all commands
/clear     # Clear transcript
```

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

The API provides:
- **REST endpoints** for project lifecycle (run, pause, resume, cancel), SRS/ECO approval, status queries, task trees, cost breakdown, events, and semantic index queries
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

Still planned for a later phase:

- `axiom doctor` — system health and dependency checks (Phase 19)

See the [Architecture Document](../ARCHITECTURE.md) and [Implementation Plan](../IMPLEMENTATION_PLAN.md) for the full roadmap.
