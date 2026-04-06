# Model Registry & BitNet Reference

The model registry (`internal/models/`) and BitNet service (`internal/bitnet/`) implement Architecture Sections 18 and 19. The registry provides model-aware scheduling data; the BitNet service manages the local inference server lifecycle.

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                    Model Registry                         │
│                                                           │
│  ┌─────────────────┐  ┌─────────────────┐                │
│  │  Shipped Index   │  │  OpenRouter     │                │
│  │  models.json     │  │  /api/v1/models │                │
│  │  (31 models)     │  │  (live pricing) │                │
│  └─────────────────┘  └─────────────────┘                │
│                                                           │
│  ┌─────────────────┐  ┌─────────────────┐                │
│  │  BitNet Server   │  │  SQLite         │                │
│  │  /v1/models      │  │  model_registry │                │
│  │  (local models)  │  │  (persistence)  │                │
│  └─────────────────┘  └─────────────────┘                │
│                                                           │
│  ┌─────────────────┐  ┌─────────────────┐                │
│  │  Registry        │  │  Engine Adapter │                │
│  │  Service         │  │  → ModelService │                │
│  └─────────────────┘  └─────────────────┘                │
└──────────────────────────────────────────────────────────┘
```

## Three-Source Aggregation

The registry merges models from three independent sources per Section 18.2:

| Source | Method | When | Data |
|--------|--------|------|------|
| **Shipped** | `RefreshShipped()` | On startup (automatic) | Curated capability index (strengths, weaknesses, recommendations, tiers) |
| **OpenRouter** | `RefreshOpenRouter(ctx, baseURL)` | On `axiom models refresh` | Live pricing, context windows, max output from API |
| **BitNet** | `RefreshBitNet(ctx, baseURL)` | On `axiom models refresh` | Currently loaded local models |

### Merge Strategy

- All sources use `UPSERT` (INSERT ON CONFLICT UPDATE) into the same `model_registry` table.
- **OpenRouter refresh enriches from shipped:** When a fetched model ID matches a shipped model, capability data (strengths, weaknesses, tools, vision, grammar, recommended_for, not_recommended_for, tier) is copied from shipped. Pricing comes from OpenRouter (live data).
- **Performance history survives refresh:** `historical_success_rate` and `avg_cost_per_task` use `COALESCE(excluded.*, current.*)` so user-accrued data is never overwritten by a refresh.
- Each source can be refreshed independently in any order.

## Shipped Models (`models.json`)

The shipped capability index is embedded in the binary via `embed.FS`. It contains 31 models:

### Premium Tier (8 models)
| Model ID | Family | Context | Prompt $/M | Completion $/M |
|----------|--------|---------|------------|----------------|
| `anthropic/claude-opus-4.6` | anthropic | 1M | $5.00 | $25.00 |
| `openai/gpt-5.4` | openai | 1.05M | $2.50 | $15.00 |
| `openai/gpt-5.4-pro` | openai | 1.05M | $30.00 | $180.00 |
| `google/gemini-3.1-pro-preview` | google | 1M | $2.00 | $12.00 |
| `x-ai/grok-4.20` | xai | 2M | $2.00 | $6.00 |
| `x-ai/grok-4` | xai | 256K | $3.00 | $15.00 |
| `openai/o3-pro` | openai | 200K | $20.00 | $80.00 |
| `xiaomi/mimo-v2-pro` | xiaomi | 1M | $1.00 | $3.00 |

### Standard Tier (12 models)
| Model ID | Family | Context | Prompt $/M | Completion $/M |
|----------|--------|---------|------------|----------------|
| `anthropic/claude-sonnet-4.6` | anthropic | 1M | $3.00 | $15.00 |
| `openai/gpt-5.3-codex` | openai | 400K | $1.75 | $14.00 |
| `openai/o3` | openai | 200K | $2.00 | $8.00 |
| `moonshotai/kimi-k2.5` | moonshot | 262K | $0.38 | $1.72 |
| `google/gemini-2.5-pro` | google | 1M | $1.25 | $10.00 |
| `mistralai/devstral-2512` | mistral | 262K | $0.40 | $2.00 |
| `mistralai/mistral-large-2512` | mistral | 262K | $0.50 | $1.50 |
| `deepseek/deepseek-v3.2` | deepseek | 164K | $0.26 | $0.38 |
| `qwen/qwen3-coder-plus` | qwen | 1M | $0.65 | $3.25 |
| `qwen/qwen3-coder-next` | qwen | 262K | $0.12 | $0.75 |
| `meta-llama/llama-4-maverick` | meta | 1M | $0.15 | $0.60 |
| `arcee-ai/trinity-large-thinking` | arcee | 262K | $0.22 | $0.85 |

### Cheap Tier (10 models)
| Model ID | Family | Context | Prompt $/M | Completion $/M |
|----------|--------|---------|------------|----------------|
| `openai/gpt-5.4-mini` | openai | 400K | $0.75 | $4.50 |
| `anthropic/claude-haiku-4.5` | anthropic | 200K | $1.00 | $5.00 |
| `openai/gpt-5.4-nano` | openai | 400K | $0.20 | $1.25 |
| `google/gemini-2.5-flash` | google | 1M | $0.30 | $2.50 |
| `google/gemini-2.5-flash-lite` | google | 1M | $0.10 | $0.40 |
| `openai/o4-mini` | openai | 200K | $1.10 | $4.40 |
| `mistralai/devstral-small` | mistral | 131K | $0.10 | $0.30 |
| `xiaomi/mimo-v2-flash` | xiaomi | 262K | $0.09 | $0.29 |
| `deepseek/deepseek-r1-0528` | deepseek | 164K | $0.45 | $2.15 |

### Local Tier (4 models — zero cost)
| Model ID | Family | Context | Grammar |
|----------|--------|---------|---------|
| `bitnet/falcon3-1b-instruct` | falcon | 8K | Yes (GBNF) |
| `bitnet/falcon3-3b-instruct` | falcon | 8K | Yes (GBNF) |
| `bitnet/falcon3-7b-instruct` | falcon | 32K | Yes (GBNF) |
| `bitnet/falcon3-10b-instruct` | falcon | 32K | Yes (GBNF) |

Local models use the Microsoft BitNet framework with Falcon3 1.58-bit quantized weights. They support GBNF grammar-constrained decoding for reliable structured output (Section 19.3).

## Registry Service API

```go
import "github.com/openaxiom/axiom/internal/models"

reg := models.NewRegistry(db, logger)
```

| Method | Description |
|--------|-------------|
| `RefreshShipped() error` | Load embedded models.json into SQLite |
| `RefreshOpenRouter(ctx, baseURL) error` | Fetch + merge from OpenRouter API |
| `RefreshBitNet(ctx, baseURL) error` | Fetch + merge from local BitNet server |
| `List(tier, family) ([]ModelRegistryEntry, error)` | List models with optional tier/family filter (both combinable) |
| `Get(id) (*ModelRegistryEntry, error)` | Get single model by ID |
| `BrokerMaps() (map[string]ModelPricing, map[string]string)` | Extract pricing and tier maps for the inference broker |

### Broker Integration

`BrokerMaps()` produces the `ModelPricing` and `ModelTiers` maps that the inference broker needs:

```go
pricing, tiers := registry.BrokerMaps()

broker := inference.NewBroker(inference.BrokerConfig{
    // ...
    ModelPricing: pricing,  // map[string]ModelPricing
    ModelTiers:   tiers,    // map[string]string
})
```

This replaces the static maps that were hardcoded in Phase 6. The registry provides accurate, refreshable data.

## BitNet Service API

```go
import "github.com/openaxiom/axiom/internal/bitnet"

svc := bitnet.NewService(cfg)
```

| Method | Description |
|--------|-------------|
| `Enabled() bool` | Whether BitNet is enabled in config |
| `BaseURL() string` | Server endpoint (e.g., `http://localhost:3002`) |
| `WeightDir() string` | Model weights directory (`~/.axiom/bitnet/models/`) |
| `Status(ctx) ServiceStatus` | Health check + model count |
| `ListModels(ctx) ([]LocalModel, error)` | Query loaded models from server |
| `Start(ctx) error` | Start the BitNet server (manual-mode stub) |
| `Stop(ctx) error` | Stop the BitNet server (manual-mode stub) |

### ServiceStatus

```go
type ServiceStatus struct {
    Running    bool   // Server is healthy and responding
    Endpoint   string // Base URL of the server
    ModelCount int    // Number of models currently loaded
}
```

### Configuration

```toml
[bitnet]
enabled = true                    # Enable/disable local inference
host = "localhost"                # Server host
port = 3002                       # Server port
max_concurrent_requests = 4       # Max parallel requests
cpu_threads = 4                   # CPU threads for inference
```

### Sentinel Errors

| Error | Meaning |
|-------|---------|
| `ErrDisabled` | BitNet is disabled in config (`enabled = false`) |
| `ErrNotRunning` | Server is not running (Stop called without Start) |
| `ErrNoWeights` | Model weights not found locally |

## Engine Integration

The registry satisfies `engine.ModelService` via the `RegistryAdapter`:

```go
eng, err := engine.New(engine.Options{
    Config:  cfg,
    DB:      db,
    RootDir: root,
    Log:     logger,
    Models:  models.NewRegistryAdapter(registry),
})
```

The `ModelService` interface:

```go
type ModelService interface {
    RefreshShipped() error
    RefreshOpenRouter(ctx context.Context, baseURL string) error
    RefreshBitNet(ctx context.Context, baseURL string) error
    List(tier, family string) ([]ModelInfo, error)
    Get(id string) (*ModelInfo, error)
}
```

## Test Coverage

| Component | Tests | Coverage |
|-----------|-------|----------|
| State layer CRUD | 13 | Upsert, update, not-found, list-all, list-by-tier, list-by-family, delete, delete-by-source, count-by-tier, performance-update, nil-slices, timestamps |
| Shipped loader | 3 | Load + field validation, expected models, tier coverage |
| OpenRouter fetcher | 2 | Mock API success, server-down error |
| BitNet scanner | 2 | Mock API success, server-down error |
| Registry service | 7 | Refresh from each source, list/filter/get, not-found |
| Merge enrichment | 1 | OpenRouter models enriched with shipped capability data |
| Combined filtering | 1 | Tier + family filter together |
| Broker maps | 1 | Pricing + tier extraction |
| Performance preservation | 1 | Refresh does not overwrite accrued performance data |
| BitNet service | 11 | Creation, status, models, enabled, URL, start/stop, weight dir |

Total: **43 tests** across `internal/state/`, `internal/models/`, and `internal/bitnet/`.

## Offline Operation

Per Section 18.6, if OpenRouter or BitNet is unreachable during refresh, the registry falls back to whatever data was previously loaded (shipped + any cached OpenRouter/BitNet data from previous refreshes). A warning is logged but the error is non-fatal at the app level — shipped models are always available since they're embedded in the binary.
