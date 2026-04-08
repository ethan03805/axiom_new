# Issue 07 — P1: Inference, budget, prompt-safety, and prompt-logging plane is not wired into the app

**Status:** Open
**Severity:** P1
**Date opened:** 2026-04-08
**Source:** `issues.md` §7
**Base commit:** `main` @ `fd68479`

---

## 1. Issue (as reported)

> The inference, budget, prompt-safety, and prompt-logging plane is not wired into the app.
>
> - `internal/app/app.go:83-92` creates the engine without setting `Inference`.
> - `internal/engine/engine.go:75-85` stores `opts.Inference`, but that dependency is nil in the normal app composition.
> - A non-test search found no call site for `internal/inference.NewBroker`.

The architecture's core controls around provider routing, budget enforcement, local-only secret handling, and prompt logging are therefore absent from the runtime that the user actually launches. Even if execution were wired later, the current composition root would still bypass the inference control plane.

**Architecture sections affected:** §4 (Trusted Engine vs. Untrusted Planes), §19.5 (Inference Broker Specification), §21 (Budget & Cost Management), §29.4 (Secret-Aware Context Routing), §31 (Observability & Prompt Logging).

**Implementation plan phases affected:** Phase 6 (Inference Broker, Provider Routing, and Cost Enforcement), Phase 7 (Model Registry and BitNet Operations), Phase 18 (Security, Secret Handling, and Prompt Safety), Phase 19 (Crash Recovery, Observability, and Operational Hardening).

**Docs affected:** `docs/inference-broker.md`, `docs/security-prompt-safety.md`, `docs/operations-diagnostics.md`.

---

## 2. Recreation

No special harness is needed — the defect is fully observable from the composition root. The chain is:

### 2.1 The app composition root

`internal/app/app.go:100-112`:

```go
eng, err := engine.New(engine.Options{
    Config:     cfg,
    DB:         db,
    RootDir:    root,
    Log:        log,
    Git:        gitSvc,
    Container:  containerSvc,
    Index:      indexer,
    Models:     modelService,
    Validation: validation.NewEngineAdapter(validationSvc),
    Review:     review.NewEngineAdapter(reviewSvc),
    Tasks:      task.NewEngineAdapter(taskSvc),
})
```

The `Inference` field of `engine.Options` is never set. Go zero-values the interface to `nil`.

### 2.2 The engine stores the nil interface

`internal/engine/engine.go:82-96`:

```go
e := &Engine{
    ...
    inference:  opts.Inference,   // ← nil in production
    ...
}
```

### 2.3 The IPC monitor hits the nil guard for every real meeseeks run

`internal/engine/ipcmonitor.go:113-117`:

```go
response := inferenceResponsePayload{}
if e.inference == nil || !e.inference.Available() {
    response.Error = "inference broker unavailable"
    return writeIPCResponse(req.Dirs.Input, ipc.MsgInferenceResponse, req.Task.ID, response)
}
```

Meaning: the *first* `MsgInferenceRequest` any meeseeks container sends over IPC is answered with `{"error": "inference broker unavailable"}`. The container then has no way to make progress.

### 2.4 No production call site constructs a broker

`rg --no-filename 'inference\.NewBroker|engine\.New\('` shows:

- `engine.New(...)` — 7 call sites total: 1 in `internal/app/app.go` (production) and 6 in `_test.go` files (`internal/api`, `internal/cli`, `internal/session`, `internal/tui`).
- `inference.NewBroker(...)` — **0 production call sites**. Only `internal/inference/broker_test.go`, `broker_phase19_test.go`, and the package's own `docs/inference-broker.md` example reference it.

So the inference broker is fully implemented, fully tested, and fully unreachable from the binary.

### 2.5 Supporting plane components that are also orphaned

The same walk confirms the following are built but never constructed at startup:

| Component | File | Constructor | Production call sites |
|---|---|---|---|
| Inference broker | `internal/inference/broker.go:61` | `NewBroker` | **0** |
| OpenRouter provider | `internal/inference/openrouter.go:24` | `NewOpenRouterProvider` | **0** |
| BitNet provider | `internal/inference/bitnet_provider.go:22` | `NewBitNetProvider` | **0** |
| Prompt logger | `internal/observability/promptlog.go:47` | `NewPromptLogger` | **0** |
| Security policy (for broker) | `internal/security/...` `NewPolicy` | (called inside `NewBroker`) | only through `NewBroker` |
| Registry `BrokerMaps()` | `internal/models/registry.go:115` | — | **0** (only tests call it) |

`Registry.RefreshShipped()` *is* called in `app.Open` at line 71, so the registry is populated at startup — its `BrokerMaps()` output is simply never consumed.

### 2.6 Why no existing test caught this

The engine unit tests in `internal/engine/engine_test.go:307` inject a `noopInferenceService{available: true}`, so they exercise the in-memory path, not the composition root. No test asserts that `app.Open` returns an engine with `e.inference != nil` or with `Available() == true` for a production-shaped config.

---

## 3. Root cause

**The inference broker and its supporting plane (OpenRouter provider, BitNet provider, prompt logger) were built and tested in isolation, but never added to `app.Open()`. The composition root was last updated to wire Phase 7 (model registry + BitNet service process management), and the broker wiring that was supposed to consume those artifacts was skipped.**

Concretely, three independent omissions compound:

1. **No broker construction.** `app.Open` builds `registry` and `bitnetSvc` but never calls `inference.NewBroker`. The registry's `BrokerMaps()` output — the very reason that method exists — is discarded.
2. **No provider construction.** Neither `NewOpenRouterProvider` nor `NewBitNetProvider` is instantiated in `app.Open`. Their constructors take config values (`cfg.Inference.OpenRouterBase`, `cfg.BitNet.Host/Port`) that are already loaded and validated, so the inputs exist — they are simply not threaded through.
3. **No broker injection.** `engine.New` accepts `Inference engine.InferenceService` in its `Options`, but the `app.Open` literal omits the key. Because Go silently zero-values missing interface fields, there is no compile-time or startup-time signal that the broker is missing.

The result is a runtime where:
- Budget enforcement is off (no `BudgetEnforcer` instance exists in the process).
- Model allowlist / tier checks do not fire (no `modelTiers` map is consulted).
- Secret-bearing prompts are **not** forced to local inference (no `security.Policy` inspection in the request path).
- Prompt logging is inert regardless of `observability.log_prompts`.
- Every real meeseeks attempt immediately fails with `"inference broker unavailable"` as soon as it issues its first IPC `inference_request`.

This is a composition-root bug, not a design or subsystem bug. Every piece the fix needs already exists and is unit-tested.

---

## 4. Plan to fix

The fix is scoped to `internal/app/app.go` plus a health-check helper and regression tests. No changes are required to the broker, providers, registry, or engine APIs.

### 4.1 Build the broker inside `app.Open`

Insert a new composition block **after** the registry is created and `RefreshShipped()` runs (so `BrokerMaps()` has data), and **before** `engine.New(...)`.

Pseudocode:

```go
// Phase 6 + 18 + 19: Wire the inference control plane.
promptLogger := observability.NewPromptLogger(
    root,
    cfg.Observability.LogPrompts,
    security.NewPolicy(cfg.Security),
)

var cloudProvider inference.Provider
if cfg.Inference.OpenRouterAPIKey != "" {
    cloudProvider = inference.NewOpenRouterProvider(
        cfg.Inference.OpenRouterBase,
        cfg.Inference.OpenRouterAPIKey,
        inference.WithTimeout(time.Duration(cfg.Inference.TimeoutSeconds)*time.Second),
    )
}

var localProvider inference.Provider
if cfg.BitNet.Enabled {
    localProvider = inference.NewBitNetProvider(
        fmt.Sprintf("http://%s:%d", cfg.BitNet.Host, cfg.BitNet.Port),
    )
}

pricing, tiers := registry.BrokerMaps()

broker := inference.NewBroker(inference.BrokerConfig{
    Config:        cfg,
    DB:            db,
    Bus:           nil, // resolved below — see §4.2
    Log:           log,
    CloudProvider: cloudProvider,
    LocalProvider: localProvider,
    ModelPricing:  pricing,
    ModelTiers:    tiers,
    PromptLogger:  promptLogger,
})
```

Then thread it into the engine:

```go
eng, err := engine.New(engine.Options{
    ...
    Inference: broker,
    ...
})
```

### 4.2 Resolve the event-bus ordering (broker ↔ engine)

`inference.Broker` needs `*events.Bus` to publish `inference_requested`, `inference_completed`, `budget_warning`, `security_redaction`, etc. But `*events.Bus` is currently created **inside** `engine.New` (`internal/engine/engine.go:80`). This creates an ordering problem: the broker depends on a bus that only exists after the engine is constructed.

Two acceptable resolutions. Pick one in the implementation:

**Option A (preferred): construct the bus in `app.Open` and inject it into both.**
- Add `Bus *events.Bus` to `engine.Options`.
- In `engine.New`, honor `opts.Bus` if non-nil, otherwise fall back to `events.New(opts.DB, opts.Log)` for backward compatibility with the existing tests.
- In `app.Open`, create the bus once, pass it to `inference.NewBroker`, and pass the *same* instance as `Options.Bus` to `engine.New`.
- Pros: both sides see the same bus; subscribers get every event; the broker can emit at construction time if needed.
- Cons: one new `Options` field. All existing tests keep working because they pass `Options` literals without the new field.

**Option B: late-bind the bus on the broker.**
- Add a `SetBus(*events.Bus)` method on `Broker`, call `engine.New(...)` first, then `broker.SetBus(eng.Bus())`, then call an `eng.SetInference(broker)` method that was also added.
- Pros: smaller surface change at the engine call site.
- Cons: two new methods, mutable post-construction state, risk of racing with the first IPC request, and an awkward "engine without inference, then engine with inference" window.

**Recommendation:** Option A. It matches how `Git`, `Container`, and `Models` are already injected and avoids any partially-initialized window. The `engine.New` fallback keeps the 6 existing test call sites compiling unchanged.

### 4.3 Add a startup health check (fail loud, not silent)

Per the issue's own "Potential solutions", add a health check immediately after `engine.New` returns. It must:

1. Call `broker.Available()`. If both providers are nil or both report `Available(ctx) == false`, log a `WARN` and emit a `ProviderUnavailable` event, **but do not abort startup** — the user may legitimately be offline and running local-only tasks later, or may want to open the TUI to configure credentials.
2. Cross-check the configured orchestration mode (`cfg.Orchestrator.Runtime`) against available providers. Specifically:
   - If `runtime == "claw"` or any mode that implies cloud meeseeks, require `cloudProvider != nil` — otherwise return a startup error `no inference provider available for configured orchestrator runtime: openrouter API key not set`.
   - If `runtime == "local-only"` (if/when that mode exists), require `localProvider != nil` and `cfg.BitNet.Enabled == true`.
3. Log a one-line summary at `INFO`: `inference plane ready providers=[openrouter,bitnet] budget_max=$10.00 log_prompts=false`.

The health check lives as a private helper in `internal/app/app.go` (or a new `internal/app/inference.go` if `app.go` grows unwieldy) — not on the broker, because the broker should not know about orchestrator runtime names.

### 4.4 Security policy wiring

`inference.NewBroker` already constructs `security.NewPolicy(bc.Config.Security)` internally (`internal/inference/broker.go:73`), so the security plane is automatically wired the moment the broker is constructed. **No additional change is needed for §4 of the secret-aware routing contract** — wiring the broker *is* wiring the secret router.

One caveat: the `PromptLogger` also needs a `*security.Policy` for its own sanitization pass. Construct a single `securityPolicy := security.NewPolicy(cfg.Security)` in `app.Open` and pass it to both `observability.NewPromptLogger(...)` and (implicitly via `BrokerConfig.Config`) the broker. Do **not** create two separate policy instances — even though `security.Policy` is currently stateless, sharing one instance future-proofs against caching or metrics being added to it later.

### 4.5 Prompt logging wiring

Already covered by §4.1. The key correctness points:

- Pass the **project root**, not the working directory, so logs land under `.axiom/logs/prompts/` inside the project (matches the `docs/inference-broker.md` §"Cost & Prompt Logging" contract).
- Honor `cfg.Observability.LogPrompts` — when false, the broker's `writePromptLog` short-circuits on `p.Enabled()` and no files are written, which is the correct default.

### 4.6 `App` struct surface

Add the broker to the `App` struct so tests and the TUI/API layer can inspect it:

```go
type App struct {
    ...
    Broker *inference.Broker
    ...
}
```

This is optional but makes §4.7 regression tests much cleaner, and aligns with how `Registry` and `BitNet` are already exposed.

### 4.7 Regression tests

Three tests are required; all can live under `internal/app/app_test.go` (new file or existing).

1. **`TestOpen_WiresInferenceBroker`** — open a temp project directory with a minimal valid `axiom.toml` that sets a fake `openrouter_api_key`, call `app.Open(log)`, assert:
   - `app.Broker != nil`
   - `app.Engine.Inference() != nil` (may need a trivial getter added to `Engine`)
   - `app.Broker.Available()` path is reachable (no panic on nil providers).

2. **`TestOpen_FailsFastWhenNoProviderConfigured`** — open with an `axiom.toml` that sets `runtime = "claw"` but no `openrouter_api_key` and `bitnet.enabled = false`, assert `app.Open` returns an error containing `"no inference provider available"`.

3. **`TestEngine_IPCMonitorUsesRealBroker`** — extend the existing ipcmonitor test (or add a new one) to verify that when the engine is built with a real broker backed by a mock `Provider`, a synthetic `MsgInferenceRequest` is routed through the broker (cost logged, event emitted) rather than responding with `"inference broker unavailable"`. This is the end-to-end regression guard for §2.3.

Additionally, add a trivial getter if §4.7(1) needs it:

```go
// internal/engine/engine.go
func (e *Engine) Inference() InferenceService { return e.inference }
```

### 4.8 Docs updates

- `docs/inference-broker.md` — add a short "Composition root" section that points at `internal/app/app.go` as the single place where the broker is instantiated in production, and notes the startup health check. Remove any stale language suggesting the wiring is "deferred".
- `docs/security-prompt-safety.md` — confirm that the secret-aware routing is live by pointing at the same line range. Remove any TODO/NOTYETWIRED markers.
- `docs/operations-diagnostics.md` — document the new startup health check, its INFO/WARN log lines, and the `ProviderUnavailable` startup event so operators know what to grep for.

### 4.9 Out of scope (explicitly deferred)

Per the existing "Known Deferred Items" in `docs/inference-broker.md`, the following are **not** addressed by this fix and should remain deferred:

- Streaming via chunked IPC output files (`ProviderRequest.Stream` stays `false`).
- Queue-until-connectivity retry behavior (belongs in the scheduler, Phase 10).
- Dynamic / hot-reloaded model pricing (broker still gets static maps at construction time; a `RefreshOpenRouter` + rebuild cycle is a follow-up).
- Wiring an inference runtime for the orchestrator itself in external-client mode, which per §21.1 is intentionally outside the budget ceiling.

---

## 5. Files expected to change

| File | Change |
|---|---|
| `internal/app/app.go` | Construct `securityPolicy`, `promptLogger`, `cloudProvider`, `localProvider`, `broker`; pass `broker` and (per §4.2 Option A) the shared `*events.Bus` into `engine.New(...)`; add `Broker` field to `App`; add startup health check helper |
| `internal/engine/engine.go` | Add `Bus *events.Bus` to `Options` (optional field with fallback to `events.New(...)`); add `Inference() InferenceService` getter |
| `internal/app/app_test.go` | **New file** — the three regression tests in §4.7 |
| `internal/engine/ipcmonitor_test.go` | Extend with real-broker path test (or add new file) |
| `docs/inference-broker.md` | Add "Composition root" section; remove stale deferral language |
| `docs/security-prompt-safety.md` | Confirm live wiring |
| `docs/operations-diagnostics.md` | Document the new health check log lines and startup event |

No changes are required to `internal/inference/*`, `internal/models/*`, `internal/observability/*`, or `internal/security/*`. Every dependency the broker needs already exists.

---

## 6. Notes, risks, and open questions

1. **Risk — existing engine tests.** All 6 non-app `engine.New` call sites are test harnesses. After §4.2 Option A, they keep compiling because `Bus` is an additive optional field and `Inference` remains optional in those harnesses. Verify by running `go test ./internal/...` before merging.

2. **Risk — offline startup.** Users may legitimately start Axiom without network connectivity, especially for BitNet-only runs. The health check in §4.3 must not hard-fail in that case; it must distinguish "no provider is reachable right now" (warn) from "no provider is configured at all" (error).

3. **Open question — where does the orchestrator-runtime check live?** The §4.3 cross-check between orchestrator runtime and provider availability could alternatively live in `cfg.Validate()`. Keeping it in `app.Open` is preferred because it depends on runtime provider availability, not just config shape — but this is a judgement call worth confirming in review.

4. **Open question — multiple OpenRouter/BitNet instances.** Phase 7 created a `bitnetSvc` service manager in `app.Open` that may lazily start a BitNet subprocess. The broker's `BitNetProvider` assumes the server is already running at `Host:Port`. Confirm whether `bitnetSvc.Start(ctx)` should be called during `app.Open` (before the health check) or on-demand by the first local-tier request. The simplest wiring is eager-start when `cfg.BitNet.Enabled` is true; this matches the user's mental model of "if I enabled it, it should be up".

5. **Root-cause escalation guard.** Per the Issue 06 fix pattern (binary-level acceptance tests guarding against silent drift), consider adding a `cmd/axiom/` acceptance test that boots the app against a fixture project and asserts the INFO log line "inference plane ready" is emitted. This catches future `app.Open` rewrites that accidentally drop the wiring again.

6. **Interaction with Issue 02 / 03.** Issues 02 and 03 closed the engine-start and execution-path gaps. Those fixes are what make this issue *reachable* in the first place — before them, no meeseeks ever ran, so the nil broker never mattered. This fix is the third leg of that tripod: start workers, run tasks, **and** route their model calls through the trusted plane.

7. **No secrets in code or logs.** The fix threads `cfg.Inference.OpenRouterAPIKey` into `NewOpenRouterProvider` directly. The key must never appear in the "inference plane ready" INFO line or in any error returned from the health check. Write the log so it prints provider *names*, not credentials.

---

## 7. Acceptance criteria

- [ ] `app.Open` constructs a non-nil `*inference.Broker` and injects it into `engine.New(...)`.
- [ ] `engine.Options.Inference` is populated in the sole production call site.
- [ ] A startup health check runs after `engine.New` returns and emits a single INFO line naming available providers, budget ceiling, and prompt-logging state.
- [ ] Starting Axiom with `cfg.Orchestrator.Runtime = "claw"` and no OpenRouter key returns a clear startup error, not a silent nil.
- [ ] A meeseeks `MsgInferenceRequest` is answered by the broker (cost logged, `inference_completed` event on the bus) rather than `"inference broker unavailable"`.
- [ ] `observability.log_prompts = true` causes a sanitized JSON file to appear under `.axiom/logs/prompts/` after a meeseeks attempt.
- [ ] All existing tests pass unchanged; the three new regression tests in §4.7 pass.
- [ ] Docs in §4.8 updated; no remaining "deferred" / "not yet wired" language for the broker composition.
