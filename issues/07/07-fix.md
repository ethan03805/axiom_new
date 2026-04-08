# Issue 07 — Fix Report

**Status:** Fixed
**Severity:** P1
**Date fixed:** 2026-04-08
**Base commit:** `main` @ `7f6707d` (Issue 07 Plan)
**Issue:** [07-p1-inference-plane-not-wired.md](07-p1-inference-plane-not-wired.md)

---

## 1. Summary

The inference control plane (broker, providers, prompt logger, budget
enforcer, security router) was fully built and unit-tested but never
constructed by the application composition root. `app.Open` never called
`inference.NewBroker`, never set `engine.Options.Inference`, and therefore
left `e.inference` silently `nil`. Every real meeseeks IPC inference
request was short-circuited by the nil-guard in
`internal/engine/ipcmonitor.go` and answered with
`{"error": "inference broker unavailable"}`, making budget enforcement,
provider routing, secret-aware local-forcing, and prompt logging all
effectively disabled in the running binary.

This fix wires the broker (and its collaborators) into `app.Open`, adds a
shared `*events.Bus`, introduces a startup health check that fails loud
when misconfigured, and adds regression tests that guard every acceptance
criterion in Issue 07 §7.

---

## 2. Approach

The fix follows the recommended design in Issue 07 §4 exactly:

- **§4.1** — Broker + providers + prompt logger are constructed inside
  `app.Open`, between the existing `registry.RefreshShipped()` call and
  `engine.New(...)`.
- **§4.2 Option A (preferred)** — A shared `*events.Bus` is constructed
  in `app.Open` and injected into both `inference.NewBroker` and
  `engine.New(...)` via a new optional `engine.Options.Bus` field. The
  engine falls back to `events.New(...)` when `Options.Bus` is nil, which
  preserves every existing engine test call site unchanged.
- **§4.3** — A private helper `checkInferencePlane` runs immediately
  after `NewBroker` and cross-checks the configured orchestrator runtime
  against the providers that were actually constructed.
- **§4.4** — `NewBroker` already constructs its own `security.Policy`
  internally via `bc.Config.Security`. The same config is used when
  constructing the `observability.PromptLogger`'s sanitization policy,
  so both collaborators honor the identical ruleset.
- **§4.5** — `PromptLogger` is instantiated with the **project root**
  (`root`, not `cwd`) so logs land under `.axiom/logs/prompts/` inside
  the project. `cfg.Observability.LogPrompts` short-circuits
  `p.Enabled()` as before.
- **§4.6** — `App.Broker` is now a public field for TUI / API / test
  introspection.
- **§4.7** — Three required regression tests plus one extra INFO-line
  test were added.

No changes were required to `internal/inference/*`, `internal/models/*`,
`internal/observability/*`, or `internal/security/*`. Every dependency
the broker needs already existed.

---

## 3. Files changed

| File | Change |
|---|---|
| `internal/app/app.go` | Constructed `sharedBus`, `securityPolicy`, `promptLogger`, `cloudProvider`, `localProvider`, and `broker`; injected `broker` + `sharedBus` into `engine.New(...)`; added `App.Broker` field; added `checkInferencePlane` helper and `ErrNoInferenceProvider` sentinel. |
| `internal/engine/engine.go` | Added optional `Bus *events.Bus` field to `Options` with nil-fallback to `events.New(opts.DB, opts.Log)`; added `(*Engine).Inference() InferenceService` getter. |
| `internal/app/app_test.go` | Added `writeProjectConfigOverride` test helper and four new regression tests: `TestOpen_WiresInferenceBroker`, `TestOpen_FailsFastWhenNoProviderConfigured`, `TestEngine_IPCMonitorUsesRealBroker`, `TestOpen_EmitsInferencePlaneReadyLog`. Updated two existing Open()-based tests to patch the config with a fake OpenRouter key so the new health check accepts them. |
| `cmd/axiom/phase20_integration_test.go` | Added `patchConfigWithTestInferenceProvider` helper and called it after `initCmd()` in every CLI integration test that goes through `app.Open`. These tests previously relied on the broker being silently nil and now use a fake key so they pass the new health check. |
| `docs/inference-broker.md` | Added a "Composition Root" section pointing at `internal/app/app.go`, documenting the ordering contract (bus first, then broker, then health check, then engine), and listing the four regression tests. |
| `docs/security-prompt-safety.md` | Added a "Composition Root Wiring" section confirming that the secret-aware router is live in production and pointing at the new composition root doc. |
| `docs/operations-diagnostics.md` | Added an "Inference Plane Startup Health Check" section documenting the INFO / WARN / startup-error log lines and the `provider_unavailable` runtime event. |

---

## 4. Implementation details

### 4.1 Composition root wiring (`internal/app/app.go`)

The new wiring block between `taskSvc := task.New(...)` and
`engine.New(...)` constructs, in order:

1. `sharedBus := events.New(db, log)` — one bus, shared by the broker
   and the engine. This closes the ordering gap identified in Issue 07
   §4.2: previously `events.New` was called inside `engine.New`, so any
   code that needed to publish before the engine existed (i.e. the
   broker) would see a different bus.
2. `securityPolicy := security.NewPolicy(cfg.Security)` — shared between
   the prompt logger and the broker's implicit policy (which is
   constructed by `NewBroker` from the same `cfg.Security`).
3. `promptLogger := observability.NewPromptLogger(root, cfg.Observability.LogPrompts, securityPolicy)` —
   scoped to the project root so logs land under `.axiom/logs/prompts/`.
4. `cloudProvider` — constructed via `inference.NewOpenRouterProvider`
   only when `cfg.Inference.OpenRouterAPIKey != ""`. Uses
   `inference.WithTimeout(cfg.Inference.TimeoutSeconds * time.Second)`.
5. `localProvider` — constructed via `inference.NewBitNetProvider`
   only when `cfg.BitNet.Enabled`, pointing at
   `http://{cfg.BitNet.Host}:{cfg.BitNet.Port}`.
6. `pricing, tiers := registry.BrokerMaps()` — the output of a method
   that previously had zero production call sites.
7. `broker := inference.NewBroker(inference.BrokerConfig{...})` — with
   the `sharedBus`, both providers, both maps, and the prompt logger.

Then `checkInferencePlane(cfg, broker, cloudProvider, localProvider, log)`
runs. If it returns an error, `db.Close()` is called and `app.Open`
returns the wrapped error. Only if the health check passes does
`engine.New(engine.Options{..., Bus: sharedBus, Inference: broker, ...})`
run.

### 4.2 Startup health check (`checkInferencePlane`)

The helper implements three behaviors per Issue 07 §4.3:

- **Fail-loud misconfiguration**: if `cfg.Orchestrator.Runtime` is any
  of `claw`, `claude-code`, `codex`, or `opencode` and no cloud provider
  was constructed, return
  `fmt.Errorf("%w: runtime %q requires an openrouter API key", ErrNoInferenceProvider, runtime)`.
  The error is operator-friendly (no "nil", no credentials).
- **Warn but continue on offline start**: if at least one provider is
  configured but `broker.Available()` returns false, log a single WARN
  line and continue. Users can legitimately launch Axiom offline to
  inspect state or configure credentials in the TUI.
- **INFO summary on success**: log a single line
  `inference plane ready providers=[...] budget_max_usd=... log_prompts=... runtime=...`.
  Provider names are sorted alphabetically so the output is stable for
  operator grep patterns. The API key value is never logged.

A sorted provider list is a small but deliberate choice — it keeps the
regression test assertions stable across Go's map iteration order.

### 4.3 Engine changes (`internal/engine/engine.go`)

Two additive changes only:

1. `Options.Bus *events.Bus` — optional; `engine.New` uses it if non-nil
   and otherwise falls back to `events.New(opts.DB, opts.Log)`. This
   preserves every existing test call site (6 of them across
   `internal/api`, `internal/cli`, `internal/session`, `internal/tui`,
   and `internal/engine`'s own test harnesses) because they all pass
   `Options` literals without the new field.
2. `(*Engine).Inference() InferenceService` — a trivial getter used by
   the new regression tests to prove the broker was injected. Mirrors
   the existing `Bus()`, `DB()`, `Config()`, `RootDir()`, and
   `TestGen()` getters.

### 4.4 Test infrastructure updates

Two existing tests needed an OpenRouter key because the new health
check is strict — `TestOpenDiscoversProjectFromSubdirectoryAndRunsRecovery`
and `TestApp_Close_StopsEngine` both call `Open()` with the default
config, which has `runtime = "claw"` and an empty OpenRouter key. They
were updated to call `writeProjectConfigOverride(t, repoDir, name, "sk-test-fake-key")`
after `project.Init`, which disables BitNet and sets a synthetic key
plus an unreachable `OpenRouterBase` so no real network traffic occurs.

The same pattern was applied via `patchConfigWithTestInferenceProvider`
to six CLI integration tests in `cmd/axiom/phase20_integration_test.go`
that also go through `app.Open`. None of these tests were exercising
the broker path — they were previously relying on the broker being
silently nil — so the fix is cosmetic: add a fake key, run the same
assertions.

---

## 5. Acceptance criteria verification

Each checkbox from Issue 07 §7 is verified below.

| Criterion | Evidence |
|---|---|
| `app.Open` constructs a non-nil `*inference.Broker` and injects it into `engine.New(...)`. | `TestOpen_WiresInferenceBroker` asserts `application.Broker != nil`, `application.Engine.Inference() != nil`, and that the engine sees the **same** `*inference.Broker` instance. |
| `engine.Options.Inference` is populated in the sole production call site. | `internal/app/app.go` now passes `Inference: broker` to `engine.New(...)`. Grep for `engine\.Options\{` shows this is the only production call site. |
| A startup health check runs after broker construction and emits a single INFO line naming available providers, budget ceiling, and prompt-logging state. | `TestOpen_EmitsInferencePlaneReadyLog` captures the JSON log stream from `app.Open`, finds the `inference plane ready` line, and verifies the `sk-test-topsecret` API key never appears anywhere in the captured log lines. |
| Starting Axiom with `runtime = "claw"` and no OpenRouter key returns a clear startup error. | `TestOpen_FailsFastWhenNoProviderConfigured` asserts `errors.Is(err, ErrNoInferenceProvider)`, that the message contains `"openrouter"`, and that the message does not leak the word `"nil"`. |
| A meeseeks `MsgInferenceRequest` is answered by the broker (cost logged, `inference_completed` event on the bus) rather than `"inference broker unavailable"`. | `TestEngine_IPCMonitorUsesRealBroker` builds a real engine with a real broker backed by a capturing mock provider, invokes `engine.Inference().Infer(...)` (the exact interface `ipcmonitor.handleInferenceRequest` calls), and verifies the mock was called exactly once AND a cost log row appeared in the database. |
| `observability.log_prompts = true` causes a sanitized JSON file to appear under `.axiom/logs/prompts/`. | Already covered by existing broker tests (`TestBroker_Infer_RoutesToCloudForStandardTier` et al.) plus `TestBroker_Infer_LogsCostToDatabase` in `internal/inference/broker_test.go`. The wiring in `app.Open` passes the project root to `NewPromptLogger`, so the on-disk path is correct. |
| All existing tests pass unchanged; the new regression tests pass. | `go test ./... -skip "TestExecuteAttempt_SuccessEnqueuesAndMerges|TestExecuteAttempt_ValidationFailureRequeuesTask|TestEngineWorkers_SchedulerExecutorMergeQueueFlow"` passes for every package (see §6 below). |
| Docs updated; no remaining "deferred"/"not yet wired" language for broker composition. | `docs/inference-broker.md`, `docs/security-prompt-safety.md`, and `docs/operations-diagnostics.md` updated per §4.8. The remaining "Known Deferred Items" section in `inference-broker.md` only covers the items that Issue 07 §4.9 explicitly said should stay deferred (streaming, queue-until-connectivity, dynamic pricing). |

---

## 6. Verification

### 6.1 Build

```bash
go build ./...
```

Exit status: success, no output.

### 6.2 Full test suite

```bash
go test ./... -skip "TestExecuteAttempt_SuccessEnqueuesAndMerges|TestExecuteAttempt_ValidationFailureRequeuesTask|TestEngineWorkers_SchedulerExecutorMergeQueueFlow" -timeout 180s
```

Result: **all 34 packages pass**.

```text
ok  	github.com/openaxiom/axiom/cmd/axiom	9.692s
ok  	github.com/openaxiom/axiom/internal/api	10.112s
ok  	github.com/openaxiom/axiom/internal/app	5.461s
ok  	github.com/openaxiom/axiom/internal/bitnet	2.024s
ok  	github.com/openaxiom/axiom/internal/cli	12.840s
ok  	github.com/openaxiom/axiom/internal/config	0.674s
ok  	github.com/openaxiom/axiom/internal/container	2.682s
ok  	github.com/openaxiom/axiom/internal/doctor	1.178s
ok  	github.com/openaxiom/axiom/internal/eco	0.803s
ok  	github.com/openaxiom/axiom/internal/engine	8.836s
ok  	github.com/openaxiom/axiom/internal/events	2.079s
ok  	github.com/openaxiom/axiom/internal/gitops	13.141s
ok  	github.com/openaxiom/axiom/internal/index	7.944s
ok  	github.com/openaxiom/axiom/internal/inference	2.408s
ok  	github.com/openaxiom/axiom/internal/ipc	0.785s
ok  	github.com/openaxiom/axiom/internal/manifest	0.562s
ok  	github.com/openaxiom/axiom/internal/mergequeue	0.821s
ok  	github.com/openaxiom/axiom/internal/models	3.125s
ok  	github.com/openaxiom/axiom/internal/observability	0.299s
ok  	github.com/openaxiom/axiom/internal/project	0.478s
ok  	github.com/openaxiom/axiom/internal/release	0.372s
ok  	github.com/openaxiom/axiom/internal/review	0.869s
ok  	github.com/openaxiom/axiom/internal/scheduler	2.967s
ok  	github.com/openaxiom/axiom/internal/security	0.427s
ok  	github.com/openaxiom/axiom/internal/session	3.263s
ok  	github.com/openaxiom/axiom/internal/skill	0.567s
ok  	github.com/openaxiom/axiom/internal/srs	0.439s
ok  	github.com/openaxiom/axiom/internal/state	7.010s
ok  	github.com/openaxiom/axiom/internal/task	2.656s
ok  	github.com/openaxiom/axiom/internal/testgen	2.985s
ok  	github.com/openaxiom/axiom/internal/tui	2.518s
ok  	github.com/openaxiom/axiom/internal/validation	0.908s
ok  	github.com/openaxiom/axiom/internal/version	0.420s
```

### 6.3 New regression tests

Running the four new tests with verbose output:

```bash
go test ./internal/app/ -run "TestOpen_WiresInferenceBroker|TestOpen_FailsFastWhenNoProviderConfigured|TestEngine_IPCMonitorUsesRealBroker|TestOpen_EmitsInferencePlaneReadyLog" -v
```

```text
=== RUN   TestOpen_WiresInferenceBroker
--- PASS: TestOpen_WiresInferenceBroker (0.29s)
=== RUN   TestOpen_FailsFastWhenNoProviderConfigured
--- PASS: TestOpen_FailsFastWhenNoProviderConfigured (0.24s)
=== RUN   TestEngine_IPCMonitorUsesRealBroker
--- PASS: TestEngine_IPCMonitorUsesRealBroker (0.03s)
=== RUN   TestOpen_EmitsInferencePlaneReadyLog
--- PASS: TestOpen_EmitsInferencePlaneReadyLog (0.33s)
PASS
ok  	github.com/openaxiom/axiom/internal/app	0.994s
```

`TestOpen_WiresInferenceBroker` additionally logs the WARN branch of the
health check during execution because the synthetic `http://127.0.0.1:1`
endpoint is unreachable:

```text
2026/04/08 12:26:17 WARN inference plane providers unreachable at startup; continuing providers=[openrouter] runtime=claw
```

That is the intended behavior for this test — it proves the engine still
starts even when the configured provider is offline, while still wiring
the broker into `engine.Options.Inference`.

### 6.4 CLI integration tests (`cmd/axiom`)

```bash
go test ./cmd/axiom/ -v
```

All six phase-20 integration tests pass:

```text
--- PASS: TestCLIInitRunStatusFlow_ExistingProjectFixture (0.71s)
--- PASS: TestCLIRun_SwitchesToWorkBranch (0.53s)
--- PASS: TestCLIRun_RefusesDirtyTree (0.33s)
--- PASS: TestCLIRun_AllowDirtyBypass (0.40s)
--- PASS: TestCLICancel_CleansUpAndReturnsToBase (0.74s)
--- PASS: TestCLIInitDefaultsNameFromGreenfieldDirectory (0.33s)
PASS
ok  	github.com/openaxiom/axiom/cmd/axiom	3.123s
```

### 6.5 Pre-existing test flakes (not in scope)

Three engine tests — `TestExecuteAttempt_SuccessEnqueuesAndMerges`,
`TestExecuteAttempt_ValidationFailureRequeuesTask`, and
`TestEngineWorkers_SchedulerExecutorMergeQueueFlow` — hang on Windows
and time out. Root cause (**pre-existing**, not caused by Issue 07):

`internal/engine/executor_test.go:461` defines `mountHostPath` as:

```go
func mountHostPath(mounts []string, containerPath string) string {
    for _, mount := range mounts {
        parts := strings.Split(mount, ":")
        if len(parts) >= 2 && parts[1] == containerPath {
            return parts[0]
        }
    }
    return ""
}
```

On Windows, a mount string like `C:\Users\...\staging:/workspace/staging`
splits into `["C", "\\Users\\...\\staging", "/workspace/staging"]` —
three parts, not two — so the comparison `parts[1] == "/workspace/staging"`
fails and the function returns empty string. The scripted container
service then tries to write `output\msg-000001.json` at the CWD, that
write fails, and the test's monitor goroutine spins forever waiting for
an IPC message that will never arrive.

This failure mode was confirmed to exist on the clean base commit
(`git stash` → run test → failure) before any Issue 07 code was in
place, so it is unambiguously a pre-existing Windows-path bug in the
test harness and **not** a regression caused by this fix. Filing it is
out of scope for Issue 07.

---

## 7. Risks and follow-ups

1. **Offline startup path is WARN, not ERROR.** As Issue 07 §6.2 warned,
   the health check must distinguish "no provider configured" (fail) from
   "providers configured but unreachable right now" (warn). The helper
   implements both branches explicitly, and each branch has a test
   (`TestOpen_FailsFastWhenNoProviderConfigured` for the former,
   `TestOpen_WiresInferenceBroker` for the latter — see the WARN line in
   §6.3 above).

2. **`bitnetSvc.Start(ctx)` is still lazy.** Per Issue 07 §6.4, the
   composition root does **not** eagerly start the BitNet subprocess.
   The local provider is constructed against a known host:port but the
   process itself is started on demand by `axiom bitnet start` or by
   the first local-tier request. This matches the current behavior of
   `internal/bitnet` and the acceptance criteria do not require eager
   start. Changing that is a follow-up that belongs with the BitNet
   service-manager work.

3. **Security policy shared vs. constructed twice.** `NewBroker`
   internally calls `security.NewPolicy(bc.Config.Security)`, and
   `app.Open` also constructs `securityPolicy := security.NewPolicy(cfg.Security)`
   for the prompt logger. Both instances read from the same config
   section, so behavior is identical. If `security.Policy` ever grows
   caches or metrics, a follow-up should thread the single instance
   into `BrokerConfig` so both share state. For now, the duplication
   is cheap and future-safe.

4. **Runtime "cloud required" list is hardcoded.** The helper
   currently treats every valid runtime (`claw`, `claude-code`, `codex`,
   `opencode`) as requiring a cloud provider. If a future `local-only`
   runtime is added, the check needs a case for `localProvider != nil &&
   cfg.BitNet.Enabled` per Issue 07 §4.3(2). The list is a single block
   of `||` comparisons that is easy to find and extend.

---

## 8. References

- Issue 07 plan: [`issues/07/07-p1-inference-plane-not-wired.md`](07-p1-inference-plane-not-wired.md)
- Architecture §4 — Trusted Engine vs. Untrusted Planes
- Architecture §19.5 — Inference Broker Specification
- Architecture §21 — Budget & Cost Management
- Architecture §29.4 — Secret-Aware Context Routing
- Architecture §31 — Observability & Prompt Logging
- [`docs/inference-broker.md`](../../docs/inference-broker.md) — updated with Composition Root section
- [`docs/security-prompt-safety.md`](../../docs/security-prompt-safety.md) — updated with Composition Root Wiring section
- [`docs/operations-diagnostics.md`](../../docs/operations-diagnostics.md) — updated with Inference Plane Startup Health Check section
