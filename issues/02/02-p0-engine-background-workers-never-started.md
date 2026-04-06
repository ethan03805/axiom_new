# Issue 02 - P0 - Engine background workers are never started in real app flows

## Status

- Severity: P0
- State: Reproduced and root-caused
- Last reviewed: 2026-04-06
- Source: `issues.md` finding 2

## Expected behavior

When Axiom is used through any command surface (CLI, TUI, API server), the engine's background worker loops should be running. Specifically:

1. The **scheduler loop** should tick every 500ms, dispatching queued tasks whose dependencies are satisfied, acquiring write-set locks, and creating attempt records.
2. The **merge queue loop** should tick every 500ms, processing approved task output through integration checks and committing results to git.
3. Any future registered workers (cleanup, health monitoring) should also be running.

Per Architecture Sections 15 (Task System), 16 (Concurrency, Snapshots & Merge Queue), and 22 (State Management & Crash Recovery), the engine is a long-lived runtime with required background workers. The scheduler and merge queue are the core execution drivers — without them, tasks can be created but never dispatched, and approved output can be enqueued but never committed.

## Actual behavior

`Engine.Start(ctx)` is never called in any production code path. The engine is created via `engine.New()` and recovered via `engine.Recover()`, but the background worker pool is never initialized or started. `engine.Running()` returns `false` in all live command surfaces.

This means:
- Tasks created by an external orchestrator will remain in `queued` status forever.
- The merge queue will never process approved output.
- The scheduler's lock acquisition, dependency resolution, and dispatch logic is dead code in production.

## Reproduction

Confirmed on 2026-04-06 with a locally built binary and a clean throwaway git repo.

### Commands used

1. `go build -o /tmp/axiom-issue02.exe ./cmd/axiom`
2. Created temp repo with initial `main` branch commit
3. `/tmp/axiom-issue02.exe init --name repro-issue02`
4. `git add -A && git commit -m "axiom init"`
5. `/tmp/axiom-issue02.exe run "Build a REST API"`
6. `/tmp/axiom-issue02.exe status --verbose`

### Observed results

- Run was created successfully in `draft_srs` status.
- No log line `engine started` was emitted (this is the log line from `Engine.Start()` at `engine.go:140`).
- The engine's `running` flag is `false` throughout the entire CLI session.

### Code-level verification

Searched the entire non-test codebase for calls to `Engine.Start`:

```
grep -rn "Engine\.Start\|eng\.Start\|\.Start(ctx" --include="*.go" | grep -v _test.go
```

Result: **zero non-test call sites** for `Engine.Start()`. The only matches are:
- `Engine.StartRun()` (different method — creates a run record, does not start workers)
- `srv.Start(ctx)` (API server start, not engine start)
- `application.BitNet.Start(ctx)` (BitNet service start, not engine start)
- `cmd.Start()` (exec.Cmd start in BitNet/tunnel, not engine start)

The test file `engine_test.go:212-240` tests `Engine.Start()`/`Stop()` directly and passes, confirming the method itself works correctly when invoked.

## Root cause

### The composition root treats the engine as a stateless method collection

`internal/app/app.go:83-101` is the application composition root. It:

1. Creates the engine: `engine.New(engine.Options{...})` (line 83-96) -- **correct**
2. Runs crash recovery: `eng.Recover(context.Background())` (line 98) -- **correct**
3. Returns the `App` struct -- **missing `eng.Start(ctx)` call**

The engine is designed with a clear two-phase lifecycle:
- `New()` — constructs the engine with all dependencies wired
- `Recover()` — scans for crashed state from previous runs
- `Start(ctx)` — creates the worker pool, registers scheduler + merge queue loops, and begins ticking

Phase 3 (Start) is never called. The composition root returns the engine in a "constructed but not running" state.

### No command surface compensates for the missing Start

Every command surface receives the engine through `app.Open()` and assumes it's ready:

| Command | File | What it does | Calls `Engine.Start`? |
|---------|------|--------------|-----------------------|
| `axiom run` | `cli/run.go:22-33` | Opens app, calls `Engine.StartRun()` | No |
| `axiom api start` | `cli/stubs.go:30-48` | Opens app, starts API server | No |
| `axiom tui` | `cli/session.go:137-171` | Opens app, creates TUI model | No |
| `axiom pause/resume/cancel` | `cli/run.go:66-171` | Opens app, calls Engine lifecycle methods | No |
| `axiom session *` | `cli/session.go:26-134` | Opens app, uses session manager | No |

### The `app.Close()` method calls `Engine.Stop()`, but Start was never called

`app.go:115-123` calls `Engine.Stop()` on close. Since `running` is always `false`, this is a no-op. The symmetry (Close calls Stop but Open never calls Start) further confirms the omission.

### Why `Engine.Start()` exists and works

`internal/engine/engine.go:116-141` implements `Start()`:

```go
func (e *Engine) Start(ctx context.Context) error {
    // ...
    e.ctx, e.cancel = context.WithCancel(ctx)
    e.workers = NewWorkerPool(e.ctx, e.log)

    if _, err := e.Recover(e.ctx); err != nil { ... }

    e.workers.Register("scheduler", e.schedulerLoop, 500*time.Millisecond)
    e.workers.Register("merge-queue", e.mergeQueueLoop, 500*time.Millisecond)

    e.workers.Start()
    e.running = true
    e.log.Info("engine started", "root", e.rootDir)
    return nil
}
```

The `WorkerPool` (`internal/engine/worker.go`) is fully implemented and tested. It launches goroutines that tick at the registered interval and handles clean shutdown via context cancellation.

The `schedulerLoop` and `mergeQueueLoop` are implemented as single-tick methods that delegate to `scheduler.Tick(ctx)` and `mergeQueue.Tick(ctx)` respectively.

Everything is wired. The only missing piece is the single `Start()` call.

## Fix plan

### Design decision: when should the engine be started?

**Option A: Start in the composition root (`app.Open`)** — always start the engine for all commands. Simple, guarantees workers are always available, and eliminates the risk of a new command surface forgetting to start the engine.

**Option B: Start selectively in long-lived command surfaces** — start the engine only in `axiom tui`, `axiom api start`, etc. More precise but requires each command surface to know about engine lifecycle.

**Chosen: Option A (start in composition root).** Reasons:

- Simplicity: one call site, zero chance of a new command forgetting to start the engine.
- `Engine.Start()` is idempotent (returns immediately if already running) and `Engine.Stop()` is already called in `App.Close()`, so the lifecycle is symmetric.
- The overhead of spawning two goroutines that tick every 500ms is negligible, even for short-lived commands that exit immediately — the workers will be cancelled by `App.Close()` before they tick more than once.
- Every command surface already calls `defer application.Close()`, so workers are always cleaned up promptly.
- As the project grows, more commands may need background workers (e.g., `axiom run` becoming a long-lived session). Starting universally avoids per-command lifecycle decisions.

### Step 1: Start the engine in `app.Open()`

**File:** `internal/app/app.go`

After creating the engine and running recovery, call `Engine.Start()` before returning:

```go
eng, err := engine.New(engine.Options{ ... })
if err != nil { ... }

if _, err := eng.Recover(context.Background()); err != nil {
    db.Close()
    return nil, fmt.Errorf("running startup recovery: %w", err)
}

// Start engine background workers (scheduler, merge queue).
// Workers are stopped by App.Close() → Engine.Stop().
if err := eng.Start(context.Background()); err != nil {
    db.Close()
    return nil, fmt.Errorf("starting engine: %w", err)
}

return &App{ ... }, nil
```

This is the single fix that resolves the issue. All command surfaces automatically get a running engine.

### Step 2: Remove duplicate `Recover()` call from `Engine.Start()`

**File:** `internal/engine/engine.go`, lines 128-131

`Engine.Start()` currently calls `Recover()` internally (line 128). But `app.Open()` already calls `Recover()` before returning (line 98). This means recovery runs twice when `StartEngine` is called after `Open`.

Two options:
- **Option A:** Remove the `Recover()` call from `Start()`, since `Open()` already handles it. `Start()` should only concern itself with starting workers.
- **Option B:** Add a guard in `Recover()` to make it idempotent (skip if already recovered).

**Recommended: Option A.** `Start()` should be purely about starting background workers. Recovery is a separate lifecycle phase that the composition root manages. This also matches the test pattern in `engine_test.go` where `Start()` is called directly on a fresh engine (which also calls `Recover()` internally — but in production, Open already did it).

Updated `Start()`:

```go
func (e *Engine) Start(ctx context.Context) error {
    e.mu.Lock()
    defer e.mu.Unlock()

    if e.running {
        return nil
    }

    e.ctx, e.cancel = context.WithCancel(ctx)
    e.workers = NewWorkerPool(e.ctx, e.log)

    e.workers.Register("scheduler", e.schedulerLoop, 500*time.Millisecond)
    e.workers.Register("merge-queue", e.mergeQueueLoop, 500*time.Millisecond)

    e.workers.Start()
    e.running = true
    e.log.Info("engine started", "root", e.rootDir)
    return nil
}
```

Note: the existing tests call `Start()` without a prior `Recover()` call. Since `testEngine(t)` creates a fresh engine with no prior state, the `Recover()` inside `Start()` is a no-op in tests today. After removing it, tests will still pass because recovery is not needed in a clean test environment.

### Step 3: Add a startup health assertion

**File:** `internal/api/server.go`

The API server should verify the engine is running before accepting requests. Add a pre-flight check in `Start()`:

```go
func (s *Server) Start(ctx context.Context) error {
    if !s.eng.Running() {
        return fmt.Errorf("engine is not running; call Engine.Start() before starting the API server")
    }
    // ... rest of existing Start() code
}
```

This provides a clear fail-fast error if someone bypasses `app.Open()` and wires the API server manually without starting the engine.

### Step 4: Add tests

#### 4a. Unit test: `app.Open()` returns a running engine

**File:** `internal/app/app_test.go` (new or existing)

```go
func TestApp_Open_EngineRunning(t *testing.T) {
    app := testApp(t)
    defer app.Close()

    if !app.Engine.Running() {
        t.Fatal("engine should be running after Open")
    }
}

func TestApp_Close_EngineStops(t *testing.T) {
    app := testApp(t)
    app.Close()

    if app.Engine.Running() {
        t.Fatal("engine should not be running after Close")
    }
}
```

#### 4b. Unit test: Engine.Start without prior Recover

Verify `Start()` works cleanly without a prior `Recover()` call (important since we removed it from `Start()`):

**File:** `internal/engine/engine_test.go`

```go
func TestEngine_StartWithoutRecover(t *testing.T) {
    e := testEngineNoRecover(t) // helper that skips Recover()
    if err := e.Start(context.Background()); err != nil {
        t.Fatalf("Start without prior Recover: %v", err)
    }
    defer e.Stop()

    if !e.Running() {
        t.Error("engine should be running")
    }
}
```

#### 4c. Integration test: API server rejects start when engine not running

**File:** `internal/api/server_test.go`

```go
func TestServer_RejectsStartWithoutEngine(t *testing.T) {
    eng := testEngine(t) // engine created but NOT started
    srv := NewServer(eng, eng.DB(), ServerConfig{Port: 0})

    err := srv.Start(context.Background())
    if err == nil {
        t.Fatal("expected error when engine not running")
    }
}
```

#### 4d. Update existing engine Start/Stop tests

Ensure `TestEngine_StartStop` and `TestEngine_StopIdempotent` still pass after removing `Recover()` from `Start()`.

### Step 5: Update documentation

**Files to update:**

1. `docs/task-scheduler.md` — Update the "Engine Integration" section to note that `app.Open()` now starts the engine automatically, so all command surfaces get a running scheduler.

2. `docs/api-server.md` — Update the "Starting the Server" section to note that the engine is started by `app.Open()`, and the pre-flight check guards against manual wiring mistakes.

3. `docs/session-tui.md` — Note that the engine is running when the TUI launches (started by `app.Open()`).

## Implementation order

1. Step 2 — Remove duplicate `Recover()` from `Engine.Start()` (smallest, lowest risk)
2. Step 1 — Add `Engine.Start()` call to `app.Open()` (the core fix)
3. Step 3 — Add API server pre-flight assertion
4. Step 4 — Add tests (can be done alongside each step)
5. Step 5 — Update docs

## Verification

After the fix, these conditions should hold:

1. **Every** command that uses `app.Open()` gets a running engine. `Engine.Running()` returns `true` immediately after `Open()`.
2. `axiom api start`, `axiom tui`, `axiom run`, `axiom status`, etc. all log `engine started`.
3. `App.Close()` stops the engine cleanly — workers are cancelled and drained before the database is closed.
4. `go test ./internal/engine/` passes (including existing Start/Stop tests).
5. `go test ./internal/api/` passes (including new pre-flight assertion test).
6. An end-to-end test (when Issue 3 is addressed) can confirm that a queued task is dispatched by the scheduler within one tick interval (500ms) when the engine is running.

## Notes

- This fix is necessary but not sufficient for the full execution pipeline. Even with the engine running, Issue 3 (execution path from scheduled task to container output) must also be resolved before tasks produce real work.
- `Engine.Start()` currently calls `Recover()` internally, which double-recovers since `app.Open()` also calls `Recover()`. This is safe (recovery is idempotent) but wasteful. Step 2 cleans this up.
- The `WorkerPool` (`worker.go`) handles context cancellation gracefully. When `App.Close()` calls `Engine.Stop()`, workers shut down cleanly via context cancellation and `WaitGroup.Wait()`.
- For short-lived commands (`axiom status`, `axiom export`), workers will typically get at most one tick before `App.Close()` cancels them. The overhead is negligible (two goroutines sleeping on a 500ms ticker, cancelled within milliseconds).
