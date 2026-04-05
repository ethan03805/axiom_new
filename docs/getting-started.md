# Getting Started with Axiom

## Prerequisites

- **Go 1.22+** (tested with Go 1.26)
- **Git** (any recent version)
- **Docker** (required for later phases — not needed for basic project setup)

## Installation

### From Source

```bash
git clone https://github.com/openaxiom/axiom.git
cd axiom
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
│   │   ├── specs/
│   │   ├── staging/
│   │   └─�� ipc/
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

## What's Next

The following features are implemented in later phases:

- `axiom run "<prompt>"` — start autonomous execution (Phase 9+)
- `axiom tui` — interactive terminal UI (Phase 15)
- `axiom api start` — external orchestration API (Phase 16)
- `axiom doctor` — system health checks (Phase 19)

See the [Architecture Document](../ARCHITECTURE.md) and [Implementation Plan](../IMPLEMENTATION_PLAN.md) for the full roadmap.
