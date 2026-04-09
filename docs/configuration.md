# Configuration Reference

Axiom uses TOML configuration files with a two-layer system:

1. **Global config** (`~/.axiom/config.toml`) — user-wide defaults
2. **Project config** (`.axiom/config.toml`) — per-project overrides

Project values override global values. If neither file exists, architecture defaults are used.

## Generated Project Template

A freshly initialized project starts with a sparse `.axiom/config.toml`:

```toml
[project]
name = "my-project"
slug = "my-project"
```

This is intentional. `axiom init` writes only committed project-scoped fields by default. User-machine settings and secrets such as `inference.openrouter_api_key` belong in `~/.axiom/config.toml`, and omitted project fields inherit from global config or built-in defaults.

This includes BitNet settings. A fresh project template does not emit a
`[bitnet]` table, so a global choice such as:

```toml
[bitnet]
enabled = false
```

continues to apply after `axiom init` until the project config
explicitly overrides it.

## Full Supported Schema

The schema below shows every supported field after layering. `axiom init` does not emit every field into the project file.

```toml
[project]
name = "my-project"                    # Project display name (required)
slug = "my-project"                    # URL-safe identifier (required)

[budget]
max_usd = 10.00                        # Maximum budget for inference costs
warn_at_percent = 80                   # Warn when budget usage exceeds this %

[concurrency]
max_meeseeks = 10                      # Maximum concurrent worker containers

[orchestrator]
runtime = "claw"                       # claw | claude-code | codex | opencode
srs_approval_delegate = "user"         # user | claw

[inference]
openrouter_api_key = ""                # OpenRouter API key (stored in global config only)
openrouter_base_url = "https://openrouter.ai/api/v1"  # OpenRouter API base URL
max_requests_per_task = 50             # Per-task rate limit (requests per task)
token_cap_per_request = 16384          # Maximum max_tokens value per request
timeout_seconds = 120                  # HTTP timeout for provider requests

[bitnet]
enabled = true                         # Enable local BitNet inference
host = "localhost"                     # BitNet server host
port = 3002                            # BitNet server port
max_concurrent_requests = 4            # Max parallel local inference requests
cpu_threads = 4                        # CPU threads for local inference
command = ""                           # Optional executable for Axiom-managed BitNet
args = []                              # Optional argv for the managed process
working_dir = ""                       # Optional working directory for the managed process
startup_timeout_seconds = 30           # Wait time for /health before startup fails

[docker]
image = "axiom-meeseeks-multi:latest"  # Default Meeseeks container image
timeout_minutes = 30                   # Hard timeout per container
cpu_limit = 0.5                        # CPU cores per container
mem_limit = "2g"                       # Memory limit per container
network_mode = "none"                  # MUST be "none" (security requirement)

[validation]
timeout_minutes = 10                   # Validation sandbox timeout
cpu_limit = 1.0                        # Validation CPU limit
mem_limit = "4g"                       # Validation memory limit
network = "none"                       # MUST be "none" (security requirement)
allow_dependency_install = true        # Allow dependency install from lockfile
security_scan = false                  # Enable optional security scanning
dependency_cache_mode = "prefetch"     # Build immutable caches before validation
fail_on_cache_miss = true              # Never fetch from network during validation
warm_pool_enabled = false              # Pre-warmed validation containers (future)
warm_pool_size = 3                     # Number of warm containers
warm_cold_interval = 10                # Full cold build every N warm runs

[validation.integration]
enabled = false                        # Opt-in integration sandbox
allowed_services = []                  # Explicitly scoped service access
secrets = []                           # Explicitly scoped secrets
network_egress = []                    # Explicitly scoped network ranges

[security]
force_local_for_secret_bearing = true  # Route secret-bearing context to local inference
allow_external_for_redacted_sensitive = true
sensitive_patterns = [                 # File patterns treated as secret-bearing
    "*.env*",
    "*.env",
    ".env.local",
    ".env.production",
    "*credentials*",
    "*secret*",
    "*key*",
    "**/secrets/**"
]
security_critical_patterns = [         # Patterns requiring elevated review
    "**/auth/**",
    "**/crypto/**",
    "**/migrations/**",
    ".github/workflows/**"
]

[git]
auto_commit = true                     # Auto-commit approved changes
branch_prefix = "axiom"               # Work branch prefix (branches: axiom/<slug>)

[api]
port = 3000                            # API server port
rate_limit_rpm = 120                   # Requests per minute per token
allowed_ips = []                       # IP allowlist (empty = allow all)

[cli]
ui_mode = "auto"                       # auto | tui | plain
theme = "axiom"                        # TUI theme
show_task_rail = true                  # Show task list in TUI
prompt_suggestions = true              # Enable prompt suggestions
persist_sessions = true                # Persist interactive sessions
compact_after_messages = 200           # Compact transcript after N messages
editor_mode = "default"                # default | vim
images_enabled = false                 # Image support in TUI

[observability]
log_prompts = false                    # Persist sanitized prompt/response logs under .axiom/logs/prompts/
log_token_counts = true                # Always log token counts
```

## Validation Rules

The following validation rules are enforced when loading configuration:

| Field | Rule |
|-------|------|
| `project.name` | Required, non-empty |
| `project.slug` | Required, non-empty |
| `budget.max_usd` | Must be >= 0 |
| `budget.warn_at_percent` | Must be 0-100 |
| `concurrency.max_meeseeks` | Must be >= 1 |
| `orchestrator.runtime` | Must be: claw, claude-code, codex, or opencode |
| `orchestrator.srs_approval_delegate` | Must be: user or claw |
| `docker.timeout_minutes` | Must be >= 1 |
| `docker.cpu_limit` | Must be > 0 |
| `docker.network_mode` | Must be "none" |
| `validation.network` | Must be "none" |
| `cli.ui_mode` | Must be: auto, tui, or plain |
| `api.port` | Must be 1-65535 |
| `bitnet.startup_timeout_seconds` | Must be >= 0 |

### Inference Plane Startup Requirements

Since the Issue 07 fix, `app.Open` runs an inference-plane health check
immediately after constructing the broker. The check rejects config
combinations that cannot serve any inference request:

| Condition | Behavior |
|---|---|
| `orchestrator.runtime` is one of `claw`, `claude-code`, `codex`, or `opencode` **and** `inference.openrouter_api_key` is empty | `axiom` exits with `no inference provider available for configured orchestrator runtime: runtime "<name>" requires an openrouter API key`. Set the key in `~/.axiom/config.toml` (global) to keep secrets out of the project config, then retry. |
| At least one provider is configured but none currently reports `Available(ctx) == true` | Startup continues, but a WARN line `inference plane providers unreachable at startup; continuing` is logged. Useful for offline startup flows; any real inference request will still fail with `ErrProviderDown` until connectivity returns. |
| At least one provider is reachable | Startup continues and logs a single INFO line `inference plane ready providers=[...] budget_max_usd=... log_prompts=... runtime=...`. The API key value never appears in this log line. |

See [Operations & Diagnostics Reference § Inference Plane Startup Health
Check](operations-diagnostics.md#inference-plane-startup-health-check)
for the exact log lines and
[Inference Broker § Composition Root](inference-broker.md#composition-root)
for the wiring contract.

Invalid configurations produce actionable error messages listing all violations.

## Operations Notes

- Managed BitNet process control is disabled unless `bitnet.command` is configured. When it is set, Axiom stores managed-process state under `~/.axiom/bitnet/service.json`.
- `axiom doctor` can load only the global/default config when run outside a project. In that case, project-specific cache checks are skipped.
- `[orchestrator].runtime` currently names the external runtime you intend to appoint. Axiom does not auto-launch that runtime in live app flows yet.
- Disabling BitNet in `~/.axiom/config.toml` is the normal way to turn it off for newly initialized projects. Add a project-local `[bitnet]` table only when you intentionally want one repository to behave differently from your machine-wide default.

## Security Behavior

Phase 18 uses the `[security]` section during prompt packaging and inference routing:

- `sensitive_patterns` and `security_critical_patterns` are additive. They extend the built-in defaults rather than replacing them.
- Secret-bearing prompt payloads are routed to the local tier by default when `force_local_for_secret_bearing = true`.
- Explicit external use of redacted sensitive content is only allowed per request and only when `allow_external_for_redacted_sensitive = true`.
- `.axiom/`, `.env*`, and log files are excluded from prompt packaging regardless of config.

When `observability.log_prompts = true`, Axiom writes sanitized JSON prompt logs to `.axiom/logs/prompts/<task-id>-<attempt>.json`. The logger re-applies the secret-redaction policy before persistence, so raw secrets are not written even if the provider response contains them.

See [Operations & Diagnostics Reference](operations-diagnostics.md) for startup recovery, `axiom doctor`, managed BitNet lifecycle, and prompt-log behavior.

## Config Layering Example

Global config (`~/.axiom/config.toml`):
```toml
[inference]
openrouter_api_key = "sk-or-v1-..."

[budget]
max_usd = 50.00

[orchestrator]
runtime = "claude-code"
```

Project config (`.axiom/config.toml`):
```toml
[project]
name = "my-project"
slug = "my-project"

[budget]
max_usd = 20.00
```

Result: budget is $20 (project overrides global), runtime is "claude-code" (inherited from global).

The same inheritance rule applies to secrets and machine-local defaults: if the project file does not declare `inference.openrouter_api_key`, the global value remains in effect.

The same rule applies to BitNet. If global config contains:

```toml
[bitnet]
enabled = false
```

and the project config remains the sparse `axiom init` template, BitNet
stays disabled for that project. If you want a specific repository to
opt back in, add an explicit project override:

```toml
[bitnet]
enabled = true
```

## Runtime Skill Regeneration

Phase 17 introduces runtime-specific instruction generation via:

```bash
axiom skill generate --runtime <claw|claude-code|codex|opencode>
```

The generated artifacts include values from `.axiom/config.toml`. Re-run the command after changing any of the following:

- `orchestrator.runtime`
- `api.port`
- `budget.max_usd`
- `budget.warn_at_percent`
- `git.branch_prefix`

See [Runtime Skill System Reference](runtime-skills.md) for the generated file layout.

See [Security, Secret Handling, and Prompt Safety](security-prompt-safety.md) for the full phase-18 behavior.
