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

1. **Token cap check** — `max_tokens` must not exceed `inference.token_cap_per_request` (default 16384). Returns `ErrTokenCapExceeded`.

2. **Model allowlist + tier check** — The requested `model_id` must be registered in the broker's model-tier map. The model's tier must be at or below the task's tier. Returns `ErrModelNotAllowed`.

3. **Budget pre-authorization** — Calculates worst-case cost (`max_tokens * completion_cost_per_token`) and checks against remaining budget. Zero-cost models (BitNet) bypass this check. Returns `ErrBudgetExceeded`.

4. **Rate limit check** — Increments the per-task request counter and rejects if it exceeds `inference.max_requests_per_task` (default 50). Returns `ErrRateLimitExceeded`.

5. **Emit `inference_requested` event** — Logged to the event bus with model ID and max tokens.

6. **Provider selection** — Local-tier tasks route to BitNet. Other tiers route to OpenRouter. If the selected provider is unavailable, emits `provider_unavailable` and returns `ErrProviderDown`.

7. **Execute request** — Sends the chat completion request to the provider. Measures latency.

8. **Calculate actual cost** — `(input_tokens * prompt_cost) + (output_tokens * completion_cost)`.

9. **Record cost** — Updates the budget enforcer's running total.

10. **Log to database** — Inserts a `cost_log` entry with run ID, task ID, attempt ID, agent type, model ID, token counts, and cost.

11. **Emit `inference_completed` event** — Includes model, provider, tokens, cost, finish reason, and latency.

12. **Budget threshold check** — Emits `budget_exceeded` if spend > max, or `budget_warning` if spend >= warn threshold.

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
})
```

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
| `inference_requested` | Before provider call | `model_id`, `max_tokens` |
| `inference_completed` | After successful response | `model_id`, `provider`, `input_tokens`, `output_tokens`, `cost_usd`, `finish_reason`, `latency_ms` |
| `inference_failed` | On provider error or validation rejection | `model_id`, `error` |
| `provider_unavailable` | When no provider can serve the request | `tier`, `model_id` |
| `budget_warning` | When spend reaches warn threshold | `spent`, `max` |
| `budget_exceeded` | When spend exceeds budget ceiling | `spent`, `max` |

## Cost Logging

Every completed inference request is logged to the `cost_log` table:

```sql
INSERT INTO cost_log
    (run_id, task_id, attempt_id, agent_type, model_id, input_tokens, output_tokens, cost_usd)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
```

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

All errors are checked via `errors.Is()`.

## Test Coverage

| Component | Tests | Coverage |
|-----------|-------|----------|
| Budget enforcer | 11 | Authorization, recording, thresholds, zero budget, zero-cost models, concurrency |
| Rate limiter | 6 | Under/over limit, independent tasks, count, reset, concurrency |
| OpenRouter provider | 11 | Success, API errors (402/429/500), invalid JSON, empty choices, context cancellation, availability |
| BitNet provider | 7 | Success, grammar constraints, no-grammar case, availability, errors, empty choices |
| Broker integration | 16 | Cloud/local routing, allowlist, tier mismatch, budget rejection, rate limit, token cap, cost logging, event emission, fallback, both-down, zero-cost, budget tracking, prompt fallback, provider errors |

Total: **51 tests**. All use mock HTTP servers (`httptest.NewServer`) or mock provider implementations. Broker tests create real SQLite databases for cost log verification.

## Known Deferred Items

- **Streaming via chunked IPC output files** — The `Stream` field exists in `ProviderRequest` but is hardcoded to `false`. Streaming requires the IPC chunk writer and integrates with Phase 10 (task execution).
- **Queue-until-connectivity for non-local tasks** — When the cloud provider is down, non-local tasks receive `ErrProviderDown` immediately. The queue-and-retry behavior belongs in the Phase 10 scheduler.
- **Dynamic model pricing** — Currently passed as a static map at broker construction. Phase 7 (Model Registry) will load and refresh pricing from OpenRouter's model API.
