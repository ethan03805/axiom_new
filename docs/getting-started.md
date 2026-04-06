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

## What's Next

The following features are implemented in later phases:

- `axiom run "<prompt>"` — CLI command to start a run (Phase 14; engine SRS flow available since Phase 9)
- Test-generation separation and convergence logic (Phase 13)
- `axiom tui` — interactive terminal UI (Phase 15)
- `axiom api start` — external orchestration API (Phase 16)
- `axiom doctor` — system health checks (Phase 19)

See the [Architecture Document](../ARCHITECTURE.md) and [Implementation Plan](../IMPLEMENTATION_PLAN.md) for the full roadmap.
