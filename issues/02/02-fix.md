# Issue 02 Fix - Engine Background Workers Started in app.Open()

## Status

- Fixed: 2026-04-06
- Verified: All 31 packages pass (`go test ./...`)

## Summary

`Engine.Start(ctx)` was never called in any production code path. The engine was constructed and recovered but its background worker loops (scheduler, merge queue) were never started. This fix adds the missing `Start()` call in the composition root and cleans up a duplicate `Recover()` invocation.

## Changes

### 1. Removed duplicate `Recover()` from `Engine.Start()` (`internal/engine/engine.go`)

`Engine.Start()` previously called `Recover()` internally (line 128-131). Since `app.Open()` already calls `eng.Recover()` before `Start()`, this was redundant. Removed the `Recover()` call from `Start()` so it is purely responsible for starting background workers.

**Before:**
```go
func (e *Engine) Start(ctx context.Context) error {
    // ...
    e.ctx, e.cancel = context.WithCancel(ctx)
    e.workers = NewWorkerPool(e.ctx, e.log)

    if _, err := e.Recover(e.ctx); err != nil {
        e.cancel()
        return err
    }

    e.workers.Register("scheduler", e.schedulerLoop, 500*time.Millisecond)
    // ...
}
```

**After:**
```go
func (e *Engine) Start(ctx context.Context) error {
    // ...
    e.ctx, e.cancel = context.WithCancel(ctx)
    e.workers = NewWorkerPool(e.ctx, e.log)

    e.workers.Register("scheduler", e.schedulerLoop, 500*time.Millisecond)
    // ...
}
```

### 2. Added `Engine.Start()` call in `app.Open()` (`internal/app/app.go`)

The core fix. After creating the engine and running recovery, `app.Open()` now calls `eng.Start(context.Background())` before returning the `App` struct. This guarantees all command surfaces (CLI, TUI, API) get a running engine with active scheduler and merge queue workers.

```go
if err := eng.Start(context.Background()); err != nil {
    db.Close()
    return nil, fmt.Errorf("starting engine: %w", err)
}
```

On failure, the database is closed before returning the error, matching the existing error-handling pattern in `Open()`.

### 3. Added API server pre-flight engine check (`internal/api/server.go`)

`Server.Start()` now verifies `eng.Running()` before binding the listener. If someone wires the API server manually without going through `app.Open()`, they get a clear error message instead of silent misbehavior.

```go
func (s *Server) Start(ctx context.Context) error {
    if !s.eng.Running() {
        return fmt.Errorf("engine is not running; call Engine.Start() before starting the API server")
    }
    // ...
}
```

### 4. Updated API test helper (`internal/api/handlers_test.go`)

The `testEngine()` helper now calls `eng.Start()` and registers a cleanup to call `eng.Stop()`, so all API tests operate against a running engine (matching production behavior). Added a separate `testEngineNotStarted()` helper for testing the pre-flight rejection.

### 5. New tests added

| Test | File | Verifies |
|------|------|----------|
| `TestEngine_StartWithoutRecover` | `engine_test.go` | `Start()` works without a prior `Recover()` call |
| `TestEngine_StartIdempotent` | `engine_test.go` | Calling `Start()` twice is a no-op |
| `TestApp_Close_StopsEngine` | `app_test.go` | `Close()` stops the engine (`Running()` returns false) |
| `TestServer_RejectsStartWithoutEngine` | `server_test.go` | API server refuses to start when engine is not running |

The existing `TestOpenDiscoversProjectFromSubdirectoryAndRunsRecovery` test was updated to also assert `Engine.Running() == true` after `Open()`.

## Verification

1. `go build ./...` compiles with zero errors.
2. `go test ./internal/engine/` -- all pass, including new `StartWithoutRecover` and `StartIdempotent` tests.
3. `go test ./internal/app/` -- both tests pass; logs confirm `engine started` and `engine stopped`.
4. `go test ./internal/api/` -- all pass, including `TestServer_RejectsStartWithoutEngine`.
5. `go test ./...` -- all 31 packages pass with zero failures.

## Design rationale

**Start in the composition root (Option A from the issue plan):** Every command surface goes through `app.Open()`, so a single `Start()` call there guarantees workers are always running. The overhead is negligible for short-lived commands (workers are cancelled by `App.Close()` before they tick more than once). This eliminates the risk of a new command surface forgetting to start the engine.

**Lifecycle symmetry:** `Open()` now calls `Start()`, and `Close()` calls `Stop()`. The engine has a clean three-phase lifecycle: `New()` -> `Recover()` -> `Start()`, with `Stop()` on teardown.
