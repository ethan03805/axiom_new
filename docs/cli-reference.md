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
2. Generates `config.toml` with architecture defaults
3. Writes `.gitignore` for ephemeral runtime state
4. Creates an empty `models.json`
5. Creates and migrates the SQLite database
6. Validates the generated configuration

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

Next: run 'axiom run "<prompt>"' to start a project.
```

**Errors:**
- Fails if `.axiom/` already exists (use a fresh directory)
- Fails if the generated config is somehow invalid (should not happen with defaults)

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
| Cancel run | `active`/`paused` -> `cancelled` | `run_cancelled` |
| Complete run | `active` -> `completed` | `run_completed` |
| Fail run | `active` -> `error` | `run_error` |

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

Start a new project run: generate SRS, await approval, execute.

```bash
axiom run "<prompt>" [--budget <usd>]
```

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--budget` | config value | Budget in USD |

**Example:**
```bash
$ axiom run "Build a REST API with user auth"
Run created: a1b2c3d4-...
  Status: draft_srs
  Branch: axiom/my-project
  Budget: $10.00

Next: approve the SRS to begin execution.
```

### `axiom pause`

Pause an active execution. Only works when a run is in `active` status.

### `axiom resume`

Resume a paused execution. Only works when a run is in `paused` status.

### `axiom cancel`

Cancel execution, kill containers, revert uncommitted changes. Works from `active` or `paused` status.

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

Start the local BitNet inference server.

### `axiom bitnet stop`

Stop the local BitNet inference server.

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

## Stub Commands (Planned for Later Phases)

These commands exist in the CLI surface but delegate to subsystems not yet implemented:

### Interactive Session Commands (Phase 15)
| Command | Description |
|---------|-------------|
| `axiom tui` | Launch interactive TUI |
| `axiom tui --plain` | Plain text renderer |
| `axiom session list` | List resumable sessions |
| `axiom session resume <id>` | Resume a session |
| `axiom session export <id>` | Export session transcript |

### API & Tunnel Commands (Phase 16)
| Command | Description |
|---------|-------------|
| `axiom api start` | Start API server |
| `axiom api stop` | Stop API server |
| `axiom api token generate [--scope <scope>]` | Generate auth token |
| `axiom api token list` | List active tokens |
| `axiom api token revoke <id>` | Revoke a token |
| `axiom tunnel start` | Start Cloudflare tunnel |
| `axiom tunnel stop` | Stop tunnel |

### Skill Commands (Phase 17)
| Command | Description |
|---------|-------------|
| `axiom skill generate --runtime <rt>` | Generate skill file |

### Utility Commands (Phase 19)
| Command | Description |
|---------|-------------|
| `axiom doctor` | System health check |
