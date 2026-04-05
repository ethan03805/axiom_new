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

**Example:**
```bash
$ axiom status
Axiom project: my-app
  Root:   /home/user/my-app
  Budget: $10.00
  Status: idle
```

## Planned Commands (Not Yet Implemented)

These commands are defined in the architecture and will be implemented in later phases:

### Project Commands
| Command | Phase | Description |
|---------|-------|-------------|
| `axiom run "<prompt>"` | 9 | Start a new project run |
| `axiom run --budget <usd> "<prompt>"` | 9 | Start with specific budget |
| `axiom pause` | 3 | Pause execution |
| `axiom resume` | 3 | Resume paused execution |
| `axiom cancel` | 3 | Cancel execution |
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

### Model Commands
| Command | Phase | Description |
|---------|-------|-------------|
| `axiom models refresh` | 7 | Update model registry |
| `axiom models list` | 7 | List all models |
| `axiom models list --tier <tier>` | 7 | Filter by tier |
| `axiom models info <model-id>` | 7 | Show model details |

### BitNet Commands
| Command | Phase | Description |
|---------|-------|-------------|
| `axiom bitnet start` | 7 | Start local inference |
| `axiom bitnet stop` | 7 | Stop local inference |
| `axiom bitnet status` | 7 | Show server status |
| `axiom bitnet models` | 7 | List local models |

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

### Utility Commands
| Command | Phase | Description |
|---------|-------|-------------|
| `axiom doctor` | 19 | System health check |
| `axiom skill generate --runtime <rt>` | 17 | Generate skill file |
| `axiom index refresh` | 8 | Force re-index |
| `axiom index query --type <type>` | 8 | Query semantic index |
