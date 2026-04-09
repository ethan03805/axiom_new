# CLI Reference

## Currently Implemented Commands

### `axiom version`

Show the Axiom version, git commit, build date, and platform.

```bash
$ axiom version
axiom 1.0.0 (abc1234) built 2026-04-05T12:00:00Z windows/amd64
```

### `axiom init`

Initialize a new Axiom project in the current directory.

```bash
axiom init [flags]
```

**Flags:**
| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--name` | `-n` | directory name | Project name |
| `--verbose` | `-v` | false | Enable verbose logging |

**What it does:**
1. Creates the `.axiom/` directory structure
2. Generates a minimal project-scoped `config.toml` containing `[project].name` and `[project].slug`
3. Writes `.gitignore` for ephemeral runtime state
4. Creates an empty `models.json`
5. Creates and migrates the SQLite database
6. Creates the project record in the database
7. Validates the generated configuration

**Example:**
```bash
$ mkdir my-app && cd my-app
$ git init
$ axiom init --name "My Application"
Axiom project initialized in /home/user/my-app
  Project: My Application
  Slug:    my-application
  Config:  /home/user/my-app/.axiom/config.toml
  Branch:  axiom/my-application

Next: run 'axiom run "<prompt>"' to create a run for your appointed external orchestrator.
```

Current operating model: `axiom run` creates the run state, but a user-appointed external orchestrator must generate and submit the first SRS draft.

The generated `.axiom/config.toml` is intentionally sparse. Runtime defaults are still applied by layered config loading, and user-machine secrets such as `inference.openrouter_api_key` stay in `~/.axiom/config.toml`.

BitNet settings are treated the same way. `axiom init` does not emit a
`[bitnet]` table, so a global `bitnet.enabled = false` remains in
effect for the new project unless you add an explicit project-local
override.

**Errors:**
- Fails if `.axiom/` already exists (use a fresh directory)
- Fails if the generated config is somehow invalid (should not happen with defaults)

Note: `axiom init` itself does not open the engine, so it does not
trigger the inference-plane startup health check. The check runs the
first time any command that calls `app.Open` is invoked (`axiom run`,
`axiom status`, the TUI, etc). If the default runtime `claw` is
configured but no OpenRouter key is set, that later command fails with
`no inference provider available for configured orchestrator runtime`.
See [Getting Started § Set Your OpenRouter API Key Before `axiom run`](getting-started.md#set-your-openrouter-api-key-before-axiom-run).

### `axiom status`

Show the current project status.

```bash
axiom status [flags]
```

**Flags:**
| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--verbose` | `-v` | false | Enable verbose logging |

**Requires:** An initialized Axiom project (searches current directory and parents for `.axiom/`).

**Output:** The status command uses the engine's status projection system (Phase 3) to display run state, task summary, and budget information.

**Example (no active run):**
```bash
$ axiom status
Axiom project: my-app
  Root:   /home/user/my-app
  Status: idle (no active run)
  Budget: $10.00 (configured maximum)
```

**Example (active run):**
```bash
$ axiom status
Axiom project: my-app
  Root:   /home/user/my-app
  Run:    a1b2c3d4-...
  Status: active
  Branch: axiom/my-app
  Budget: $3.50 / $10.00
  Tasks:  12 total, 5 done, 2 running, 4 queued, 1 failed
```

**Example (budget warning):**
```bash
$ axiom status
Axiom project: my-app
  ...
  Budget: $8.50 / $10.00 [WARNING: 80% threshold reached]
```

## Engine Run Lifecycle (Phase 3)

The engine provides run lifecycle methods that enforce state machine transitions and emit events. These are available programmatically through the engine API and are wired to CLI commands (`axiom run`, `axiom pause`, `axiom resume`, `axiom cancel`) since Phase 14:

| Operation | State Transition | Event Emitted |
|-----------|-----------------|---------------|
| Create run | (new) -> `draft_srs` | `run_created` |
| Pause run | `active` -> `paused` | `run_paused` |
| Resume run | `paused` -> `active` | `run_resumed` |
| Cancel run | `draft_srs`/`awaiting_srs_approval`/`active`/`paused` -> `cancelled` | `run_cancelled` |
| Complete run | `active` -> `completed` | `run_completed` |
| Fail run | `active` -> `error` | `run_error` |

> **Convergence gate on `CompleteRun`.** Per Architecture §11.5, `Engine.CompleteRun` refuses to transition a run to `completed` while any convergence pair for the run is not in `converged` status. Attempts to complete a run with open/testing/fixing/blocked pairs return a structured error that names the blocking impl tasks, and the run stays in `active`. `CancelRun` and `FailRun` bypass this gate by design (they record outcomes that differ from "completed").

## Engine SRS Lifecycle (Phase 9)

The SRS approval state machine governs how a run transitions from draft to active execution. See [SRS and ECO Reference](srs-eco.md) for details.

| Operation | State Transition | Event Emitted |
|-----------|-----------------|---------------|
| Submit SRS | `draft_srs` -> `awaiting_srs_approval` | `srs_submitted` |
| Approve SRS | `awaiting_srs_approval` -> `active` | `srs_approved` |
| Reject SRS | `awaiting_srs_approval` -> `draft_srs` | `srs_rejected` |

## Engine ECO Lifecycle (Phase 9)

ECOs allow controlled environmental changes during execution without modifying the immutable SRS.

| Operation | State Transition | Event Emitted |
|-----------|-----------------|---------------|
| Propose ECO | (new) -> `proposed` | `eco_proposed` |
| Approve ECO | `proposed` -> `approved` | `eco_resolved` |
| Reject ECO | `proposed` -> `rejected` | `eco_resolved` |

## Implemented Commands (Phase 14)

### `axiom run "<prompt>"`

Create a new project run in `draft_srs` status for external-orchestrator handoff. `axiom run` refuses to start on a dirty working tree (Architecture §28.2) and switches the repo onto `axiom/<slug>` before handing off to the orchestrator.

```bash
axiom run "<prompt>" [--budget <usd>] [--allow-dirty]
```

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--budget` | config value | Budget in USD |
| `--allow-dirty` | `false` | Bypass the clean-working-tree check (recovery only — logs a loud `WARN` and routes through the recovery-mode work branch setup) |

**Example:**
```bash
$ axiom run "Build a REST API with user auth"
Run created: a1b2c3d4-...
  Status: draft_srs
  Branch: axiom/my-project
  Budget: $10.00
```

Next step: use your appointed external orchestrator to generate and submit the SRS draft.

Use `--allow-dirty` only for crash-recovery scenarios where resuming work on a branch with legitimate uncommitted state is intentional. For everyday use, commit or stash local changes before `axiom run`.

**Errors:**

- `working tree has uncommitted changes` — commit or stash, or pass `--allow-dirty` for recovery.
- `no inference provider available for configured orchestrator runtime: runtime "<name>" requires an openrouter API key` — the inference-plane startup health check (Issue 07 fix) refused to open the engine because the configured runtime requires a cloud provider that has not been configured. Set `[inference].openrouter_api_key` in `~/.axiom/config.toml` and retry. See [Getting Started § Set Your OpenRouter API Key Before `axiom run`](getting-started.md#set-your-openrouter-api-key-before-axiom-run).

### `axiom pause`

Transition an active run to `paused`. Only works when a run is currently in `active` status.

### `axiom resume`

Transition a paused run back to `active`. Only works when a run is currently in `paused` status.

### `axiom cancel`

Cancel a run and execute the architectural cancel protocol:

1. Flip the run status to `cancelled` (atomic barrier against further task dispatch).
2. Stop any containers still running for the run.
3. Revert uncommitted changes on the work branch and switch the repo back to the base branch (`git reset --hard HEAD`, `git clean -fd`, `git checkout <base>`).
4. Emit the `run_cancelled` event.

Committed work on the `axiom/<slug>` branch is **preserved** per Architecture §23.4 — the branch is not deleted, so the user can review it, cherry-pick from it, or delete it manually.

`axiom cancel` works from every non-terminal run state, including `draft_srs` and `awaiting_srs_approval` — a user who realises they typed the wrong prompt can cancel immediately without waiting for the orchestrator to respond.

Container and git cleanup are **fail-open**: if either step fails, the cancel still completes and the user's intent is recorded. A failed git cleanup logs an explicit `git reset --hard && git checkout <base>` recovery command so the user can finish cleanup manually.

### `axiom export`

Export project state as human-readable JSON to stdout. Includes project info, active run, and tasks.

### `axiom models refresh`

Update model registry from shipped data, OpenRouter (if API key configured), and BitNet (if enabled).

### `axiom models list`

List all registered models in tabular format.

```bash
axiom models list [--tier <tier>] [--family <family>]
```

**Flags:**
| Flag | Description |
|------|-------------|
| `--tier` | Filter by tier: local, cheap, standard, premium |
| `--family` | Filter by model family |

### `axiom models info <model-id>`

Show detailed model information including pricing, capabilities, strengths, and weaknesses.

### `axiom bitnet start`

Start the local BitNet inference server. When `[bitnet].command` is configured, Axiom launches and monitors the process; otherwise the command reports that manual setup is required.

### `axiom bitnet stop`

Stop the local BitNet inference server if it was started by Axiom. Manually managed BitNet processes must still be stopped manually.

### `axiom bitnet status`

Show BitNet server status, endpoint, and loaded model count.

### `axiom bitnet models`

List models loaded in the BitNet server.

### `axiom index refresh`

Force a full re-index of the project's semantic index.

### `axiom index query`

Query the semantic index.

```bash
axiom index query --type <query_type> [--name <symbol>] [--package <pkg>]
```

**Query types:**
| Type | Required flags | Description |
|------|---------------|-------------|
| `lookup_symbol` | `--name` | Find symbols by name |
| `reverse_dependencies` | `--name` | Find references to a symbol |
| `list_exports` | `--package` | List package exports |
| `find_implementations` | `--name` | Find interface implementations |
| `module_graph` | (none) | Show package dependency graph |

## Interactive Session Commands (Phase 15)

### `axiom tui`

Launch the interactive full-screen TUI.

```bash
axiom tui [--plain]
```

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--plain` | false | Force plain-text renderer instead of full-screen TUI |

**Behavior:**
- If stdout is a TTY and `--plain` is not set: launches full-screen Bubble Tea TUI with status bar, transcript viewport, task rail, and footer composer.
- If stdout is not a TTY or `--plain` is set: renders a deterministic startup frame in plain text and exits.

**Example (plain mode):**
```bash
$ axiom tui --plain
Axiom — my-project
  Mode:    bootstrap
  Root:    /home/user/my-project
  Budget:  $0.00 / $10.00

  Describe what you want to build.

  Commands: /new  /status  /help
```

**One-shot prompt submission:** `axiom tui --prompt "<prompt>"` starts a
run from a non-interactive context (e.g. CI scripts, composition-root
tests) and writes the result to stdout. The clean-tree contract still
applies — the command exits non-zero on a dirty working tree with no
`--allow-dirty` bypass.

The TUI supports slash commands for the full operator surface:
`/status`, `/tasks`, `/budget`, `/srs`, `/approve`, `/reject "<fb>"`,
`/eco`, `/diff`, `/new`, `/resume`, `/pause`, `/cancel`, `/clear`,
`/help`. Regular text in bootstrap mode calls `Engine.StartRun`
directly. Shell mode (`!` prefix) is reserved; command execution is not
yet routed. See [Session & TUI Reference](session-tui.md) for full
details.

### `axiom session list`

List all resumable sessions for the current project.

```bash
$ axiom session list
Sessions for project:
  a1b2c3d4  (unnamed)  mode:bootstrap  last:2026-04-06 01:27
  e5f6g7h8  (unnamed)  mode:execution  run:i9j0k1l2  last:2026-04-06 02:15
```

### `axiom session resume <session-id>`

Resume a persisted interactive session by its full UUID.

```bash
$ axiom session resume a1b2c3d4-e5f6-7890-abcd-ef1234567890
Resumed session a1b2c3d4 (mode: bootstrap)
```

The session's mode is refreshed from the current run state on resume.

### `axiom session export <session-id>`

Export a session's transcript and compaction summaries to stdout.

```bash
$ axiom session export a1b2c3d4-e5f6-7890-abcd-ef1234567890
Session Export: a1b2c3d4
  Project:  my-project
  Mode:     bootstrap
  Created:  2026-04-06 01:27:20

--- Transcript ---
[01:27:20] > Build me a REST API
[01:27:21]   [system_card] Starting SRS generation...
```

## API Server Commands (Phase 16)

### `axiom api start`

Start the REST + WebSocket API server on the configured port.

```bash
axiom api start
# Starting API server on port 3000...
```

The server runs in the foreground. Stop it with Ctrl+C (SIGINT). Configuration is read from `.axiom/config.toml`:

```toml
[api]
port = 3000
rate_limit_rpm = 120
allowed_ips = []  # empty = allow all
```

### `axiom api stop`

Informational: the API server runs as a foreground process and is stopped via SIGINT.

### `axiom api token generate`

Generate a new API authentication token.

```bash
axiom api token generate [--scope <scope>] [--expires <duration>]
```

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--scope` | `full-control` | Token scope: `read-only` or `full-control` |
| `--expires` | `24h` | Expiration duration (Go duration format: `8h`, `72h`, `168h`) |

**Example:**
```bash
$ axiom api token generate --scope read-only --expires 8h
axm_sk_dG9rZW4tcmFuZG9tLWJhc2U2NC1lbmNvZGVk
Token ID: tok_a1b2c3d4e5f6g7h8
Scope: read-only
Expires: 2026-04-06T09:00:00Z
```

### `axiom api token list`

List all API tokens with their status.

```bash
$ axiom api token list
ID                   PREFIX             SCOPE          EXPIRES                   STATUS
tok_a1b2c3d4e5f6g7h8 axm_sk_dG9rZW...  read-only      2026-04-06T09:00:00Z      active
tok_i9j0k1l2m3n4o5p6 axm_sk_YW5vdG...  full-control   2026-04-07T01:00:00Z      active
```

### `axiom api token revoke <token-id>`

Revoke a specific API token immediately.

```bash
$ axiom api token revoke tok_a1b2c3d4e5f6g7h8
Token tok_a1b2c3d4e5f6g7h8 revoked.
```

### `axiom tunnel start`

Start a Cloudflare Tunnel for remote Claw access. Requires `cloudflared` to be installed.

```bash
$ axiom tunnel start
Tunnel started for localhost:3000
Public URL: https://<random>.trycloudflare.com
```

### `axiom tunnel stop`

Informational: the tunnel runs as a child process of `axiom tunnel start` and is stopped via SIGINT.

See [API Server Reference](api-server.md) for the full endpoint and WebSocket documentation.

## Runtime Skill Commands

### `axiom skill generate --runtime <rt>`

Generate deterministic runtime instruction artifacts for the specified orchestrator runtime:

| Runtime | Generated artifacts |
|---------|---------------------|
| `claw` | `axiom-skill.md` |
| `claude-code` | `.claude/CLAUDE.md`, `.claude/settings.json`, `.claude/hooks/axiom-guard.py`, shared `AGENTS.md`, and repo skill files |
| `codex` | `AGENTS.md`, `codex-instructions.md`, and repo skill files |
| `opencode` | `AGENTS.md`, `opencode-instructions.md`, `opencode.json`, and repo skill files |

Generated content includes the Axiom workflow, trust boundaries, request types, TaskSpec and ReviewSpec rules, budget policy, ECO flow, communication model, and test-separation requirements.

See [Runtime Skill System Reference](runtime-skills.md) for the full artifact layout, regeneration rules, and runtime-specific enforcement behavior.

## Diagnostics Commands

### `axiom doctor`

Run a local diagnostics pass covering:

- Docker daemon availability
- BitNet status and launch configuration
- provider/network reachability
- CPU pressure vs configured concurrency
- project cache/runtime directories and Docker image presence
- secret-scanner policy initialization

The command works outside a project. In that case, project-specific cache checks are skipped.

**Status labels:**

- `PASS` - check succeeded
- `WARN` - configuration works but should be improved
- `FAIL` - action required
- `SKIP` - check not applicable in the current context

**Example:**

```text
Phase 19 Doctor Report
[PASS] docker: Docker daemon reachable
[WARN] bitnet: BitNet is configured but not currently running
[PASS] network: Provider endpoint reachable
[PASS] resources: Configured resource pressure is within local CPU capacity
[PASS] cache: Project cache directories and image baseline are ready
[PASS] security: Secret scanner patterns loaded successfully
```

If BitNet is disabled by layered config, the expected line is:

```text
[SKIP] bitnet: BitNet disabled in config
```

The current implementation always prints the full report; for scripting, inspect the per-line status labels.

See [Operations & Diagnostics Reference](operations-diagnostics.md) for recovery, prompt logs, and BitNet managed-mode behavior.

## Release Packaging

Phase 20 release packaging is currently a library/build-tool capability, not a CLI command. See [Release Packaging Reference](release-packaging.md) for `internal/release.BuildBundle`, fixture repositories, test-matrix packaging, and manifest output.
