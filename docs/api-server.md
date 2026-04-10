# API Server

The Axiom API server exposes REST endpoints and WebSocket channels for external orchestrator integration (Architecture Section 24).

## Starting the Server

```bash
axiom api start
```

The server listens on the configured port (default: 3000). Configuration is in `.axiom/config.toml`:

```toml
[api]
port = 3000
rate_limit_rpm = 120
allowed_ips = []  # empty = allow all
```

## External Runtime Setup

If the orchestrator runtime is Claude Code, Codex, or OpenCode, generate the matching runtime instruction artifacts before connecting the runtime to the API server:

```bash
axiom skill generate --runtime codex
```

This ensures the external runtime treats the REST API and control WebSocket as the authoritative Axiom control plane instead of directly implementing the user's request outside Axiom.

Current operating model: this external runtime is required for now. Axiom does not auto-launch an embedded orchestrator or auto-bootstrap the first SRS draft in live app flows.

See [Runtime Skill System Reference](runtime-skills.md) for the generated file set.

## Authentication

All API requests require a bearer token:

```bash
# Generate a token
axiom api token generate
# Output: axm_sk_<random>

# Generate with options
axiom api token generate --scope read-only --expires 8h

# List tokens
axiom api token list

# Revoke a token
axiom api token revoke <token-id>
```

Tokens are included in requests via the `Authorization` header:

```
Authorization: Bearer axm_sk_<token>
```

### Token Scopes

- `full-control` (default): All endpoints and control WebSocket
- `read-only`: GET endpoints and event WebSocket only

## REST Endpoints

| Method | Endpoint | Scope | Purpose |
|--------|----------|-------|---------|
| `POST` | `/api/v1/projects` | full-control | Create a new project |
| `POST` | `/api/v1/projects/:id/run` | full-control | Create a run for external-orchestrator handoff |
| `POST` | `/api/v1/projects/:id/srs/submit` | full-control | Submit an SRS draft from an external orchestrator |
| `POST` | `/api/v1/projects/:id/srs/approve` | full-control | Approve the generated SRS |
| `POST` | `/api/v1/projects/:id/srs/reject` | full-control | Reject SRS with feedback |
| `POST` | `/api/v1/projects/:id/eco/approve` | full-control | Approve an ECO |
| `POST` | `/api/v1/projects/:id/eco/reject` | full-control | Reject an ECO |
| `GET` | `/api/v1/projects/:id/status` | read-only | Get project status, task tree, budget |
| `POST` | `/api/v1/projects/:id/pause` | full-control | Pause execution |
| `POST` | `/api/v1/projects/:id/resume` | full-control | Resume execution |
| `POST` | `/api/v1/projects/:id/cancel` | full-control | Cancel execution |
| `GET` | `/api/v1/projects/:id/tasks` | read-only | Get task tree with statuses |
| `GET` | `/api/v1/projects/:id/tasks/:tid/attempts` | read-only | Get attempt history for a task |
| `GET` | `/api/v1/projects/:id/costs` | read-only | Get cost breakdown |
| `GET` | `/api/v1/projects/:id/events` | read-only | Get event log |
| `GET` | `/api/v1/models` | read-only | Get model registry |
| `POST` | `/api/v1/index/query` | read-only | Query semantic index (structured JSON body) |
| `GET` | `/api/v1/tokens` | full-control | List API tokens |
| `POST` | `/api/v1/tokens/:id/revoke` | full-control | Revoke a specific token |
| `GET` | `/health` | none | Health check (no auth required) |

Current runtime note: `POST /api/v1/projects/:id/run` creates run metadata only. Clients should not expect automatic SRS generation from the server; the appointed external orchestrator must handle the first draft.

### Submit SRS Draft

```
POST /api/v1/projects/:id/srs/submit
```

Used by an external orchestrator to hand a finished SRS draft back to Axiom for human approval. Pairs with `srs/approve` and `srs/reject`. The active run for the project must already be in a state that accepts an SRS submission (typically the initial `draft_srs` phase). `axiom srs show` directs runtimes here when they need to materialize the draft.

- **Auth:** Bearer token, `full-control` scope
- **Request body:**

  ```json
  { "content": "<full SRS markdown>" }
  ```

- **Success response (`200 OK`):**

  ```json
  {
      "status": "awaiting_srs_approval",
      "run_id": "<run-id>"
  }
  ```

- **Error responses:**
  - `400 Bad Request` — body is malformed JSON, `content` is empty, or the engine rejects the draft (wrong run phase, validation failure)
  - `404 Not Found` — no active run exists for the project

## WebSocket Endpoints

### Event Stream

```
ws://localhost:3000/ws/projects/:id
```

Streams real-time project events (task completions, reviews, errors, budget warnings, ECO proposals, and security/observability events such as prompt redactions, local rerouting, and `prompt_logged`). Requires `read-only` scope or higher.

### Control Channel

```
ws://localhost:3000/ws/projects/:id/control
```

Authenticated control channel for external orchestrator action requests. Requires `full-control` scope.

#### Control Request Envelope

```json
{
    "request_id": "req-123",
    "idempotency_key": "run-001:spawn_meeseeks:task-042",
    "type": "spawn_meeseeks",
    "payload": { "task_id": "task-042" }
}
```

#### Control Response Envelope

```json
{
    "request_id": "req-123",
    "status": "accepted",
    "result": null,
    "error": null
}
```

Supported request types:

| Verb | Status | Payload | Result |
|------|--------|---------|--------|
| `query_status` | wired | — | Run status projection (with secrets redacted). |
| `query_budget` | wired | — | `{max_usd, spent_usd, remaining_usd}` |
| `query_index` | wired | `{name, kind}` | Semantic index lookup results. |
| `submit_srs` | wired | `{content: "<markdown>"}` | `{status: "awaiting_srs_approval", run_id}` |
| `submit_eco` | wired | `{category, affected_refs, description, proposed_change}` (also accepts the legacy `{reason, rationale, proposed_changes}` shape) | `{eco_id, status: "proposed"}` |
| `create_task` | wired | `{spec: {objective, context_tier, files, constraints, acceptance_criteria, interface_contract, output_format}}` | `{task_id, status: "queued"}` |
| `create_task_batch` | wired | `{tasks: [spec, …]}` (each element is a bare spec or `{"spec": {…}}` envelope) | `{task_ids, count}` |
| `approve_output` | wired | `{task_id, attempt_id}` | `{status: "enqueued"}` — attempt enters the merge queue. |
| `reject_output` | wired | `{task_id, attempt_id, reason}` | `{status: "rejected"}` — attempt is marked failed. |
| `spawn_meeseeks`, `spawn_reviewer`, `spawn_sub_orchestrator`, `request_inference` | **not yet implemented** | — | Returns `status: "rejected"` with a clear "not yet implemented on the engine" error. Spawning is driven by the scheduler/executor loop today; direct inference is served via the REST `/api/v1/inference/*` surface. |

**Response redaction.** The `query_status` result is passed through the same secret-redaction helper used by the REST `/status` endpoint — any field matching `api_key`, `apikey`, `_secret`, `_token`, `password`, or `passphrase`, and any value matching `^sk-...` / `^axm_sk_...`, is replaced with `"[REDACTED]"` before serialization. Read-only tokens cannot exfiltrate the user's OpenRouter key through this endpoint.

**Unknown verbs** return `{status: "rejected", error: "unknown request type: <verb>"}`.

## Tunnel

For remote Claw instances, Axiom supports Cloudflare Tunnel:

```bash
axiom tunnel start
# Output: https://<random>.trycloudflare.com

axiom tunnel stop
```

Requires `cloudflared` to be installed.

## Rate Limiting

The API server enforces per-token sliding-window rate limits. Requests exceeding the configured RPM limit receive `429 Too Many Requests` with a `Retry-After: 60` header. Rate limiting is keyed by the `Authorization` header value, so each token has its own independent budget.

Configuration:
```toml
[api]
rate_limit_rpm = 120    # requests per minute per token (0 = disabled)
```

## IP Allowlist

Optionally restrict API access to specific IPs or CIDR ranges:

```toml
[api]
allowed_ips = ["127.0.0.1", "192.168.1.0/24"]  # empty = allow all
```

Non-matching IPs receive `403 Forbidden`.

## Audit Logging

All API requests are logged to the `api_audit_log` table with:
- Token ID (not the raw token value)
- HTTP method and path
- Response status code
- Source IP address
- Timestamp

Requests against project endpoints (where an active run exists) are additionally logged to the `events` table with `event_type = "api_request"` and a JSON details payload.

Failed authentication attempts (invalid, expired, or revoked tokens) are captured in the audit log with the source IP for security monitoring.

## Middleware Chain

The server applies middleware in this order:
1. **Rate limiting** — per-token RPM enforcement
2. **IP allowlist** — optional network-level restriction
3. **Authentication** — bearer token validation
4. **Audit logging** — request/response recording
5. **Scope enforcement** — `read-only` vs `full-control` check
6. **Handler** — endpoint logic

The health endpoint (`/health`) bypasses the entire middleware chain.

## Implementation

The API server is implemented in `internal/api/` with these components:

| File | Purpose |
|------|---------|
| `types.go` | Request/response types, control envelope, valid request types |
| `auth.go` | Bearer token middleware, scope enforcement, token generation |
| `ratelimit.go` | Per-token rate limiter, IP allowlist middleware |
| `handlers.go` | REST endpoint handlers |
| `websocket.go` | Event stream and control channel WebSocket handlers |
| `server.go` | Server lifecycle, route registration, audit logging |
| `tunnel.go` | Cloudflare Tunnel management |

Database tables: `api_tokens` (token storage) and `api_audit_log` (request audit trail). See [Database Schema Reference](database-schema.md) for details.
