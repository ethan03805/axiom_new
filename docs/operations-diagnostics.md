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

### Automation Note

The command currently prints a report even when one or more checks fail. If you automate around it, inspect the status labels in stdout rather than relying on the process exit code alone.

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

## Related References

- [Getting Started](getting-started.md)
- [CLI Reference](cli-reference.md)
- [Configuration Reference](configuration.md)
- [Inference Broker Reference](inference-broker.md)
- [Model Registry Reference](model-registry.md)
