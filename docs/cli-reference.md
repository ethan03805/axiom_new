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

The TUI supports slash commands (`/status`, `/tasks`, `/help`, etc.), shell mode (`!` prefix), and input history (up/down arrows). See [Session & TUI Reference](session-tui.md) for full details.

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

## Stub Commands (Planned for Later Phases)

These commands exist in the CLI surface but delegate to subsystems not yet implemented:

### Skill Commands (Phase 17)
| Command | Description |
|---------|-------------|
| `axiom skill generate --runtime <rt>` | Generate skill file |

### Utility Commands (Phase 19)
| Command | Description |
|---------|-------------|
| `axiom doctor` | System health check |
