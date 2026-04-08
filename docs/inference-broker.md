# Inference Broker Reference

The inference broker (`internal/inference/`) centralizes all model access behind engine policy per Architecture Section 19.5. No container ever calls a model API directly — all inference requests are mediated by the broker, which validates, routes, executes, and logs every request.

## Architecture

```
┌──────────────────────────────────────────────────────┐
│                  Inference Broker                      │
│                                                        │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────┐   │
│  │  Budget   │  │   Rate   │  │ Model Allowlist   │   │
│  │ Enforcer  │  │ Limiter  │  │ + Tier Hierarchy  │   │
│  └──────────┘  └──────────┘  └───────────────────┘   │
│                                                        │
│  ┌─────────────────┐    ┌─────────────────┐           │
│  │  OpenRouter      │    │  BitNet          │          │
│  │  Provider        │    │  Provider        │          │
│  │  (cloud)         │    │  (local)         │          │
│  └─────────────────┘    └─────────────────┘           │
│                                                        │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────┐   │
│  │ Cost Log │  │  Event   │  │ Token Cap         │   │
│  │ (SQLite) │  │  Bus     │  │ Enforcement       │   │
│  └──────────┘  └──────────┘  └───────────────────┘   │
└──────────────────────────────────────────────────────┘
```

## Provider Interface

```go
type Provider interface {
    Name() string
    Available(ctx context.Context) bool
    Complete(ctx context.Context, req ProviderRequest) (*ProviderResponse, error)
}
```

Two implementations are provided:

| Provider | Target | Cost | Grammar Support |
|----------|--------|------|----------------|
| `OpenRouterProvider` | `https://openrouter.ai/api/v1` | Per-token pricing | No |
| `BitNetProvider` | `localhost:3002` (configurable) | Zero | Yes (GBNF) |

## Request Flow

When `Broker.Infer()` is called, it executes these steps in order:

1. **Token cap check** - `max_tokens` must not exceed `inference.token_cap_per_request` (default 16384). Returns `ErrTokenCapExceeded`.

2. **Prompt safety analysis** - Message content is scanned for secrets before prompt packaging. Matching values are replaced with `[REDACTED]`, instruction-like comments are sanitized, and `security_redaction` events are emitted without storing secret values.

3. **Secret-aware routing** - Secret-bearing requests route to the local tier by default. If `AllowExternalForSensitive` is explicitly set and `security.allow_external_for_redacted_sensitive = true`, the broker keeps the external route but still sends only redacted content. Security-critical paths alone do not force local inference.

4. **Model allowlist + tier check** - The effective `model_id` must be registered in the broker's model-tier map. The model's tier must be at or below the task's tier. Returns `ErrModelNotAllowed`.

5. **Budget pre-authorization** - Calculates worst-case cost (`max_tokens * completion_cost_per_token`) and checks against remaining budget. Zero-cost models (BitNet) bypass this check. Returns `ErrBudgetExceeded`.

6. **Rate limit check** - Increments the per-task request counter and rejects if it exceeds `inference.max_requests_per_task` (default 50). Returns `ErrRateLimitExceeded`.

7. **Emit `inference_requested` event** - Logged to the event bus with the effective model, requested model, and security classification flags.

8. **Provider selection** - Local-tier tasks route to BitNet. Other tiers route to OpenRouter. If the selected provider is unavailable, emits `provider_unavailable` and returns `ErrProviderDown`.

9. **Execute request** - Sends the sanitized chat completion request to the provider. Measures latency.

10. **Calculate actual cost** - `(input_tokens * prompt_cost) + (output_tokens * completion_cost)`.

11. **Record cost** - Updates the budget enforcer's running total.

12. **Log to database** - Inserts a `cost_log` entry and updates `task_attempts.input_tokens`, `output_tokens`, and `cost_usd` for the active attempt.

13. **Persist prompt log (optional)** - When `observability.log_prompts = true`, writes a sanitized prompt/response log under `.axiom/logs/prompts/` and emits `prompt_logged`. If the write fails, emits `diagnostic_warning` but does not fail inference.

14. **Emit `inference_completed` event** - Includes model, provider, tokens, cost, finish reason, and latency.

15. **Budget threshold check** - Emits `budget_exceeded` if spend > max, or `budget_warning` if spend >= warn threshold.

## Creating a Broker

```go
import "github.com/openaxiom/axiom/internal/inference"

broker := inference.NewBroker(inference.BrokerConfig{
    Config:        cfg,
    DB:            db,
    Bus:           bus,
    Log:           logger,
    CloudProvider: inference.NewOpenRouterProvider(
        cfg.Inference.OpenRouterBase,
        cfg.Inference.OpenRouterAPIKey,
    ),
    LocalProvider: inference.NewBitNetProvider(
        fmt.Sprintf("http://%s:%d", cfg.BitNet.Host, cfg.BitNet.Port),
    ),
    ModelPricing: map[string]inference.ModelPricing{
        "anthropic/claude-4-sonnet": {
            PromptCostPerToken:     0.000003,
            CompletionCostPerToken: 0.000015,
        },
        "bitnet/falcon3-1b": {
            PromptCostPerToken:     0,
            CompletionCostPerToken: 0,
        },
    },
    ModelTiers: map[string]string{
        "anthropic/claude-4-sonnet": "standard",
        "bitnet/falcon3-1b":        "local",
    },
    PromptLogger: observability.NewPromptLogger(
        root,
        cfg.Observability.LogPrompts,
        security.NewPolicy(cfg.Security),
    ),
})
```

`PromptLogger` is optional. Pass `nil` to disable file-based prompt logging.

## Composition Root

In production there is exactly **one** place where the broker is constructed:
[`internal/app/app.go`](../internal/app/app.go) inside `app.Open`. The
composition root owns the ordering contract between the broker and its
collaborators:

1. The event bus (`events.New`) is constructed **before** the broker so the
   same `*events.Bus` instance can be handed to both the broker and
   `engine.New(...)` via `engine.Options.Bus`. This avoids any
   partially-initialized window where the broker has been created but the
   engine's IPC monitor is still looking at a different bus.
2. `security.NewPolicy(cfg.Security)` is built once and reused by both the
   broker (via `BrokerConfig.Config`, which `NewBroker` forwards to its
   own `security.NewPolicy` call) and `observability.NewPromptLogger`,
   keeping secret-handling behavior consistent across the two.
3. `cloudProvider` is instantiated only when `cfg.Inference.OpenRouterAPIKey`
   is non-empty; `localProvider` only when `cfg.BitNet.Enabled` is true.
   The broker accepts `nil` for either — the check below catches the
   degenerate combinations.
4. `registry.BrokerMaps()` is consumed immediately after
   `registry.RefreshShipped()` so the pricing and tier maps reflect the
   current shipped catalog.
5. After `NewBroker`, a **startup health check** (`checkInferencePlane`)
   runs. It:
   - Returns `ErrNoInferenceProvider` (fail loud, not silent) when the
     configured `cfg.Orchestrator.Runtime` requires a cloud provider and
     none is configured. For example, `runtime = "claw"` with no
     `openrouter_api_key` set is rejected at startup with a message that
     names the missing key.
   - Logs a WARN (`inference plane providers unreachable at startup; continuing`)
     when providers are configured but none currently reports
     `Available(ctx) == true`. This is the intentional offline-startup
     path — operators may launch Axiom without network access and
     configure local-only runs later.
   - Logs a single INFO (`inference plane ready`) summary naming the
     available providers, the budget ceiling, the configured runtime,
     and whether prompt logging is enabled. **The API key value itself
     never appears in any log line.**
6. Only then is `engine.New(...)` called, with `Bus`, `Inference: broker`,
   and the rest of the service graph.

The broker is also exposed on the `App` struct (`App.Broker`) so the TUI,
API, and diagnostics surfaces can observe provider availability and
budget state without reaching through the engine.

Regression tests for this wiring live in
[`internal/app/app_test.go`](../internal/app/app_test.go):

- `TestOpen_WiresInferenceBroker` — guards against the broker silently
  going missing again (the original Issue 07 defect).
- `TestOpen_FailsFastWhenNoProviderConfigured` — guards the startup
  health check error path.
- `TestOpen_EmitsInferencePlaneReadyLog` — guards the INFO summary and
  verifies the API key never leaks into logs.
- `TestEngine_IPCMonitorUsesRealBroker` — end-to-end guard that a real
  inference request routed through the engine reaches the broker rather
  than the legacy `"inference broker unavailable"` short-circuit.

## Integration with Engine

The broker implements `engine.InferenceService`:

```go
eng, err := engine.New(engine.Options{
    Config:    cfg,
    DB:        db,
    RootDir:   root,
    Log:       logger,
    Git:       gitService,
    Container: containerService,
    Inference: broker,  // satisfies engine.InferenceService
    Index:     indexService,
})
```

Phase 18 adds two optional fields to `engine.InferenceRequest`:

- `ContextFiles []string` - repo paths represented in the prompt, used for sensitive/security-critical classification
- `AllowExternalForSensitive bool` - explicit per-request override allowing external inference with redacted sensitive content

## OpenRouter Provider

Calls the [OpenRouter chat completions API](https://openrouter.ai/docs/api/api-reference/chat/send-chat-completion-request):

```
POST {base_url}/chat/completions
Authorization: Bearer {api_key}
Content-Type: application/json
```

Request body follows the OpenAI-compatible format:

```json
{
  "model": "anthropic/claude-4-sonnet",
  "messages": [
    {"role": "system", "content": "..."},
    {"role": "user", "content": "..."}
  ],
  "max_tokens": 8192,
  "temperature": 0.2,
  "stream": false
}
```

Response parsing extracts:
- `choices[0].message.content` — the generated text
- `choices[0].finish_reason` — stop, length, tool_calls, content_filter, error
- `usage.prompt_tokens` — input token count
- `usage.completion_tokens` — output token count

Error handling maps HTTP status codes to descriptive errors:
- `402` — insufficient credits
- `429` — rate limited
- `500+` — server errors

### Configuration

```toml
# ~/.axiom/config.toml (global — keeps secrets out of project config)
[inference]
openrouter_api_key = "sk-or-v1-..."
openrouter_base_url = "https://openrouter.ai/api/v1"
timeout_seconds = 120
```

## BitNet Provider

Calls a local BitNet server using the same OpenAI-compatible API format:

```
POST {base_url}/v1/chat/completions
Content-Type: application/json
```

Adds an optional `grammar` field for GBNF-constrained decoding (Architecture Section 19.3):

```json
{
  "model": "bitnet/falcon3-1b",
  "messages": [{"role": "user", "content": "Generate JSON for a user profile"}],
  "max_tokens": 512,
  "temperature": 0.1,
  "grammar": "root ::= \"{\" ws \\\"name\\\" ... \"}\""
}
```

The grammar field is only present when `GrammarConstraints` is non-nil.

### Configuration

```toml
# .axiom/config.toml
[bitnet]
enabled = true
host = "localhost"
port = 3002
max_concurrent_requests = 4
cpu_threads = 4
```

## Budget Enforcer

Thread-safe budget tracker per Architecture Section 21.3.

| Method | Description |
|--------|-------------|
| `Authorize(maxTokens, pricing) error` | Pre-authorization: rejects if worst-case cost exceeds remaining budget |
| `Record(costUSD)` | Records actual spend after a completed request |
| `Remaining() float64` | Returns remaining budget in USD |
| `Spent() float64` | Returns total spend so far |
| `WarnReached() bool` | True if spend >= warn threshold (default 80%) |
| `Exceeded() bool` | True if spend > max budget |

**Zero-cost bypass:** When both `PromptCostPerToken` and `CompletionCostPerToken` are zero (BitNet), `Authorize()` always returns nil, even with a zero budget.

## Rate Limiter

Per-task request counter per Architecture Section 19.5.

| Method | Description |
|--------|-------------|
| `Allow(taskID) error` | Increments count and rejects if over limit |
| `Count(taskID) int` | Returns current count for a task |
| `Reset(taskID)` | Clears the count (used on retry with fresh container) |

Default limit: 50 requests per task. Configured via `inference.max_requests_per_task`.

## Model Tier Hierarchy

Tasks are restricted to models at their tier or below:

```
local (0) < cheap (1) < standard (2) < premium (3)
```

| Task Tier | May Use |
|-----------|---------|
| `local` | local models only |
| `cheap` | local + cheap models |
| `standard` | local + cheap + standard models |
| `premium` | all models |

The broker validates this on every request. A local-tier task requesting a standard model receives `ErrModelNotAllowed`.

## Events Emitted

| Event | When | Details |
|-------|------|---------|
| `inference_requested` | Before provider call | `model_id`, `requested_model_id`, `max_tokens`, `secret_bearing`, `security_critical` |
| `inference_completed` | After successful response | `model_id`, `provider`, `input_tokens`, `output_tokens`, `cost_usd`, `finish_reason`, `latency_ms` |
| `inference_failed` | On provider error or validation rejection | `model_id`, `error` |
| `provider_unavailable` | When no provider can serve the request | `tier`, `model_id` |
| `security_redaction` | For each redacted secret match | `file`, `line`, `pattern` |
| `security_local_routed` | When a secret-bearing request is forced local | `requested_model_id`, `local_model_id`, `security_critical` |
| `security_override_approved` | When redacted sensitive content is allowed externally | `requested_model_id`, `security_critical` |
| `prompt_logged` | After a prompt log is written | `path` |
| `diagnostic_warning` | On non-fatal observability issues | `code`, `message` |
| `budget_warning` | When spend reaches warn threshold | `spent`, `max` |
| `budget_exceeded` | When spend exceeds budget ceiling | `spent`, `max` |

## Cost & Prompt Logging

Every completed inference request is logged to the `cost_log` table:

```sql
INSERT INTO cost_log
    (run_id, task_id, attempt_id, agent_type, model_id, input_tokens, output_tokens, cost_usd)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
```

The broker also updates the active `task_attempts` row with:

- `input_tokens`
- `output_tokens`
- `cost_usd`

When prompt logging is enabled, the broker writes a sanitized JSON file to `.axiom/logs/prompts/<task-id>-<attempt>.json`. Request and response content are re-redacted before persistence. Prompt-log write failures emit `diagnostic_warning` and do not fail the inference call.

The `TotalCostByRun()` method sums all cost entries for a run and is used by:
- The budget enforcer (loaded at broker creation)
- `engine.GetRunStatus()` for the `BudgetSummary` projection
- The `axiom status` CLI command

## Sentinel Errors

| Error | Meaning |
|-------|---------|
| `ErrBudgetExceeded` | Worst-case request cost exceeds remaining budget |
| `ErrRateLimitExceeded` | Task has exhausted its per-task request allowance |
| `ErrModelNotAllowed` | Model not in allowlist or tier mismatch |
| `ErrTokenCapExceeded` | `max_tokens` exceeds configured cap |
| `ErrProviderDown` | No provider available for the requested tier |
| `ErrNoProvider` | No provider configured for the request |
| `ErrSecretBearingRequiresLocal` | Secret-bearing context requires local inference but no local route is available |

All errors are checked via `errors.Is()`.

## Test Coverage

| Component | Tests | Coverage |
|-----------|-------|----------|
| Budget enforcer | 11 | Authorization, recording, thresholds, zero budget, zero-cost models, concurrency |
| Rate limiter | 6 | Under/over limit, independent tasks, count, reset, concurrency |
| OpenRouter provider | 11 | Success, API errors (402/429/500), invalid JSON, empty choices, context cancellation, availability |
| BitNet provider | 7 | Success, grammar constraints, no-grammar case, availability, errors, empty choices |
| Broker integration | 24 | Cloud/local routing, allowlist, tier mismatch, budget rejection, rate limit, token cap, cost logging, attempt metrics, prompt logging, event emission, fallback, both-down, zero-cost, budget tracking, prompt fallback, provider errors, secret redaction, override routing, security-critical separation |

Total: **59 tests**. All use mock HTTP servers (`httptest.NewServer`) or mock provider implementations. Broker tests create real SQLite databases for cost log and prompt-log verification.

## Known Deferred Items

- **Streaming via chunked IPC output files** — The `Stream` field exists in `ProviderRequest` but is hardcoded to `false`. Streaming requires the IPC chunk writer and integrates with Phase 10 (task execution).
- **Queue-until-connectivity for non-local tasks** — When the cloud provider is down, non-local tasks receive `ErrProviderDown` immediately. The queue-and-retry behavior belongs in the Phase 10 scheduler.
- **Dynamic model pricing at runtime** — Phase 7 added the Model Registry which loads and refreshes pricing from OpenRouter's model API. The `Registry.BrokerMaps()` method extracts `ModelPricing` and tier maps suitable for broker construction. However, the broker still receives these maps statically at construction time. Hot-reloading pricing without restarting the broker is deferred.

See [Security, Secret Handling, and Prompt Safety](security-prompt-safety.md) for the shared phase-18 redaction, prompt-wrapping, and secret-routing rules used by the broker.
