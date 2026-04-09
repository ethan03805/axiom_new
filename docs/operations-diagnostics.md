# Operations & Diagnostics Reference

Phase 19 adds the operator-facing runtime layer for startup recovery, local diagnostics, prompt logging, and managed BitNet lifecycle control.

## `axiom doctor`

Run the local diagnostics report:

```bash
axiom doctor
```

Example output:

```text
Phase 19 Doctor Report
[PASS] docker: Docker daemon reachable
[WARN] bitnet: BitNet is configured but not currently running
[PASS] network: Provider endpoint reachable
[PASS] resources: Configured resource pressure is within local CPU capacity
[PASS] cache: Project cache directories and image baseline are ready
[PASS] security: Secret scanner patterns loaded successfully
```

When BitNet is intentionally disabled by layered config, the expected
output is a skip rather than a warning or failure:

```text
Phase 19 Doctor Report
[PASS] docker: Docker daemon reachable
[SKIP] bitnet: BitNet disabled in config
...
```

### Checks

The current implementation runs these checks in order:

1. `docker` - Docker daemon availability
2. `bitnet` - BitNet health and launch configuration
3. `network` - provider URL reachability
4. `resources` - configured CPU pressure vs local CPU capacity
5. `cache` - project runtime directories plus Docker image presence
6. `security` - secret-scanner policy initialization

### Status Meanings

- `PASS` - check succeeded
- `WARN` - usable, but degraded or not ideal
- `FAIL` - action is required
- `SKIP` - check is not applicable in the current context

### Project vs Non-Project Mode

`axiom doctor` works both inside and outside an initialized Axiom project.

- Inside a project: all checks can run, including cache directory validation.
- Outside a project: config falls back to global/default values and the cache check is reported as `SKIP`.
- A freshly initialized project inherits omitted machine-local settings from `~/.axiom/config.toml`. If global config disables BitNet and the project keeps the default sparse template, the BitNet check remains `SKIP`.

### Automation Note

The command currently prints a report even when one or more checks fail. If you automate around it, inspect the status labels in stdout rather than relying on the process exit code alone.

## Inference Plane Startup Health Check

As of Issue 07, `app.Open` runs a single-pass health check over the
inference control plane immediately after constructing the broker and
before `engine.New(...)` returns. The check has three visible outputs an
operator can grep for:

### INFO: `inference plane ready`

Emitted once per successful `app.Open` when at least one provider is
currently reachable. Example (structured log line):

```text
INFO inference plane ready providers=[openrouter] budget_max_usd=10 log_prompts=false runtime=claw
```

Fields:

- `providers` — deterministic, sorted list of configured provider names
  (`openrouter`, `bitnet`, or both). Credentials never appear here.
- `budget_max_usd` — the value of `[budget].max_usd` from config.
- `log_prompts` — whether `[observability].log_prompts` is enabled.
- `runtime` — the active `[orchestrator].runtime` (`claw`,
  `claude-code`, `codex`, or `opencode`).

### WARN: `inference plane providers unreachable at startup; continuing`

Emitted when at least one provider is **configured** (e.g. an OpenRouter
API key is set) but none currently reports
`Available(ctx) == true`. This is the intended offline-startup path:
the engine still starts, but every cloud inference request will fail
with `ErrProviderDown` until the network comes back.

```text
WARN inference plane providers unreachable at startup; continuing providers=[openrouter] runtime=claw
```

### Startup error: `no inference provider available for configured orchestrator runtime`

Emitted when the configured runtime requires a cloud provider (any of
`claw`, `claude-code`, `codex`, `opencode`) but no OpenRouter API key is
set in config. `app.Open` returns `ErrNoInferenceProvider` (wrapped)
and the process exits non-zero — **this is a fail-loud path, not a
warning**. Example operator-visible message:

```text
no inference provider available for configured orchestrator runtime: runtime "claw" requires an openrouter API key
```

Fix: set `[inference].openrouter_api_key` in the global config at
`~/.axiom/config.toml` (keep secrets out of the project config per
Architecture §29.4), then restart Axiom.

### `provider_unavailable` runtime events

The broker also emits a `provider_unavailable` event at runtime
(distinct from the startup check) whenever the selected tier has no
reachable provider for an active inference request. Subscribe to this
event to be paged when previously-healthy providers flap.

## Startup Recovery

Startup recovery runs before background workers begin processing.

Current recovery behavior:

1. clean up orphaned `axiom-*` Docker containers
2. mark any previously active container sessions as stopped with `recovered_startup`
3. find `in_progress` tasks whose latest attempt is still in a non-terminal phase
4. mark those attempts failed with `failure_reason = "recovered after engine restart"`
5. return the tasks to `queued`
6. release stale write-set locks
7. rebuild `task_lock_waits` and requeue tasks whose requested resources are now free
8. remove leftover `.axiom/containers/staging/*` entries
9. verify `.axiom/srs.md` against `.axiom/srs.md.sha256` and the latest stored `project_runs.srs_hash`

If SRS verification fails, startup recovery fails and the engine does not continue.

## Prompt Logs

Prompt logs are opt-in:

```toml
[observability]
log_prompts = true
```

When enabled, the broker writes sanitized JSON files to:

```text
.axiom/logs/prompts/<task-id>-<attempt>.json
```

Each log entry includes:

- run ID, task ID, attempt ID
- model ID and provider name
- redacted request messages
- redacted response text
- finish reason
- input/output tokens
- actual cost
- latency
- timestamp

The prompt logger re-applies the security policy before writing, so raw secrets are not persisted even if they appeared in the original request or provider response.

If prompt-log persistence fails, inference still succeeds; the broker emits a `diagnostic_warning` event instead of failing the request.

## Managed BitNet Lifecycle

BitNet can be managed directly by Axiom when a launch command is configured:

```toml
[bitnet]
enabled = true
host = "localhost"
port = 3002
command = "python"
args = ["server.py", "--port", "3002"]
working_dir = "/path/to/bitnet"
startup_timeout_seconds = 30
```

Behavior:

- `axiom bitnet start` starts the configured process and waits for `GET /health` to succeed
- `axiom bitnet stop` only stops processes tracked as Axiom-managed
- `axiom bitnet status` reports endpoint, running state, and loaded model count
- `axiom bitnet models` lists currently loaded models from `/v1/models`

Managed state is recorded at:

```text
~/.axiom/bitnet/service.json
```

If `[bitnet].command` is not configured, `axiom bitnet start` returns an explicit manual-setup error. If the server is running but was started manually, `axiom bitnet stop` reports that it must be stopped manually.

BitNet enablement and managed-process settings are layered config, not
required project metadata. The default `axiom init` template omits the
`[bitnet]` table, so a global disable remains in effect until a project
adds its own explicit override.

## Relevant Events

Phase 19 adds or uses these runtime/observability events:

| Event | Scope | Meaning |
|------|-------|---------|
| `recovery_started` | startup | recovery pass began |
| `recovery_completed` | startup | recovery pass finished |
| `prompt_logged` | run/task | sanitized prompt log written successfully |
| `diagnostic_warning` | startup or run/task | non-fatal operational issue such as prompt-log write failure |
| `resource_warning` | diagnostics | reserved event type for resource-pressure warnings introduced in Phase 19 |

Notes:

- `prompt_logged` is persisted when it has run/task context.
- startup-only recovery/diagnostic events without a `run_id` are fanned out to subscribers but are not written to the `events` table.
- `resource_warning` is defined in the event catalog, but the current `axiom doctor` command reports CPU pressure through `WARN` output lines rather than publishing events.

## Validation Runner Failures

Per Architecture Section 23.3 the merge queue refuses to commit unless `validation.DockerCheckRunner` (`internal/validation/runner_docker.go`) returns no failing results. Common failure modes and fixes:

| Symptom (in attempt feedback / engine log) | Cause | Fix |
|---|---|---|
| `infrastructure error running compile: docker exec ...` | `engine.ContainerService.Exec` returned an error — typically the Docker daemon is unreachable or the sandbox container crashed before the command ran. | Run `docker ps` to confirm the daemon is up, check `docker logs axiom-validator-<task-id>`, and retry. |
| `dependency_cache_miss: ...` in a compile/test result | The sandbox's prepared dependency cache did not cover the current lockfile hash — per Architecture Section 13.5 the validator fails closed rather than reaching out to the network. | Re-run `axiom preflight` (or the equivalent cache-refresh step) to rebuild the cache for the current lockfile, then requeue the task. |
| Every task stuck in `failed` with feedback `validation runner is not configured` | The process is running with `AXIOM_VALIDATION_DISABLED=1`, or no `docker.image` is set in `.axiom/config.toml` — so `app.Open()` fell back to the fail-closed `FallbackRunner`. | Remove the env var (`unset AXIOM_VALIDATION_DISABLED`) and ensure `[docker].image` is set in config. Restart the engine and the real `DockerCheckRunner` will be used. |
| `go: expected 'package'` (or equivalent) in compile feedback, but the code looks fine | The runner's `cd /workspace/project && <cmd>` wrapper is running from the mounted project root. A real compile error exists — the staged output from the Meeseeks is broken. | Inspect the feedback, fix the task's spec or let the retry/escalation flow pick a different model. |

## Related References

- [Getting Started](getting-started.md)
- [CLI Reference](cli-reference.md)
- [Configuration Reference](configuration.md)
- [Inference Broker Reference](inference-broker.md)
- [Model Registry Reference](model-registry.md)
