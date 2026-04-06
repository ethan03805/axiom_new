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

The engine provides run lifecycle methods that enforce state machine transitions and emit events. These are available programmatically through the engine API and will be wired to CLI commands in Phase 14:

| Operation | State Transition | Event Emitted |
|-----------|-----------------|---------------|
| Create run | (new) -> `draft_srs` | `run_created` |
| Pause run | `active` -> `paused` | `run_paused` |
| Resume run | `paused` -> `active` | `run_resumed` |
| Cancel run | `active`/`paused` -> `cancelled` | `run_cancelled` |
| Complete run | `active` -> `completed` | `run_completed` |
| Fail run | `active` -> `error` | `run_error` |

## Planned Commands (Not Yet Implemented)

These commands are defined in the architecture and will be implemented in later phases:

### Project Commands
| Command | Phase | Description |
|---------|-------|-------------|
| `axiom run "<prompt>"` | 9 | Start a new project run |
| `axiom run --budget <usd> "<prompt>"` | 9 | Start with specific budget |
| `axiom pause` | 14 | Pause execution (engine method available since Phase 3) |
| `axiom resume` | 14 | Resume paused execution (engine method available since Phase 3) |
| `axiom cancel` | 14 | Cancel execution (engine method available since Phase 3) |
| `axiom export` | 14 | Export project state as JSON |

### Interactive Session Commands
| Command | Phase | Description |
|---------|-------|-------------|
| `axiom` (no subcommand) | 15 | Launch interactive TUI |
| `axiom tui` | 15 | Force TUI mode |
| `axiom tui --plain` | 15 | Plain text renderer |
| `axiom session list` | 15 | List resumable sessions |
| `axiom session resume <id>` | 15 | Resume a session |
| `axiom session export <id>` | 15 | Export session transcript |

### Model Commands (service layer ready — Phase 7; CLI wiring in Phase 14)
| Command | Service Status | Description |
|---------|---------------|-------------|
| `axiom models refresh` | `Registry.RefreshShipped/OpenRouter/BitNet` | Update model registry from all sources |
| `axiom models list` | `Registry.List("", "")` | List all models (31 shipped) |
| `axiom models list --tier <tier>` | `Registry.List(tier, "")` | Filter by tier (local, cheap, standard, premium) |
| `axiom models list --family <family>` | `Registry.List("", family)` | Filter by model family |
| `axiom models info <model-id>` | `Registry.Get(id)` | Show detailed model info |

### BitNet Commands (service layer ready — Phase 7; CLI wiring in Phase 14)
| Command | Service Status | Description |
|---------|---------------|-------------|
| `axiom bitnet start` | `Service.Start` (manual-mode stub) | Start local inference server |
| `axiom bitnet stop` | `Service.Stop` (manual-mode stub) | Stop local inference server |
| `axiom bitnet status` | `Service.Status` | Show server status + loaded model count |
| `axiom bitnet models` | `Service.ListModels` | List models loaded in BitNet server |

### API & Tunnel Commands
| Command | Phase | Description |
|---------|-------|-------------|
| `axiom api start` | 16 | Start API server |
| `axiom api stop` | 16 | Stop API server |
| `axiom api token generate` | 16 | Generate auth token |
| `axiom api token list` | 16 | List active tokens |
| `axiom api token revoke <id>` | 16 | Revoke a token |
| `axiom tunnel start` | 16 | Start Cloudflare tunnel |
| `axiom tunnel stop` | 16 | Stop tunnel |

### Index Commands (service layer ready — Phase 8; CLI wiring in Phase 14)
| Command | Service Status | Description |
|---------|---------------|-------------|
| `axiom index refresh` | `Indexer.Index(ctx, dir)` | Full project re-index |
| `axiom index query --type lookup_symbol --name <name>` | `Indexer.LookupSymbol(ctx, name, kind)` | Find symbols by name |
| `axiom index query --type reverse_dependencies --name <name>` | `Indexer.ReverseDependencies(ctx, name)` | Find references to a symbol |
| `axiom index query --type list_exports --package <path>` | `Indexer.ListExports(ctx, path)` | List package exports |
| `axiom index query --type find_implementations --name <name>` | `Indexer.FindImplementations(ctx, name)` | Find interface implementations |
| `axiom index query --type module_graph` | `Indexer.ModuleGraph(ctx, root)` | Show package dependency graph |

### Utility Commands
| Command | Phase | Description |
|---------|-------|-------------|
| `axiom doctor` | 19 | System health check |
| `axiom skill generate --runtime <rt>` | 17 | Generate skill file |
