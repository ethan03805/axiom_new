# Issue 03 Fix Report

## Summary

Issue 03 was implemented by adding the missing execution worker between scheduler dispatch and merge processing. The engine now picks up dispatched attempts, builds a TaskSpec, creates task IPC directories, starts a Meeseeks container, monitors IPC output, validates the emitted manifest, runs sandbox validation, runs reviewer evaluation, performs the orchestrator gate, and enqueues approved work into the merge queue.

The merge path was also completed so attempts now advance through `queued_for_merge`, `merging`, and `succeeded` / `failed`, and task IPC / staging directories are cleaned after merge completion or merge failure.

## What Changed

- Added `internal/engine/executor.go` for the attempt executor loop and failure handling.
- Added `internal/engine/taskspec.go` for TaskSpec construction from task metadata, target files, SRS refs, repo context, and prior feedback.
- Added `internal/engine/ipcmonitor.go` for `task_output`, `inference_request`, `request_scope_expansion`, and `action_request` IPC handling.
- Extended `internal/engine/interfaces.go` so engine services can depend on validation, review, and task lifecycle abstractions without package cycles.
- Wired the executor into `internal/engine/engine.go` and passed validation / review / task services from `internal/app/app.go`.
- Replaced the merge-queue stub validator path with validation-service-backed integration checks and added attempt phase/status updates during merge processing.
- Added engine/review/validation/task/model adapters needed for composition-root wiring.
- Added fallback validation/review runners that fail closed rather than silently passing unconfigured runtimes.
- Hardened the BitNet test that was environment-sensitive so `go test ./...` is stable in environments where an unmanaged local BitNet server is already running.

## Validation

The implementation was validated with:

- New executor tests in `internal/engine/executor_test.go`
  - direct success path through executor + merge queue
  - validation failure path with task requeue and cleanup
  - scheduler -> executor -> merge queue worker integration
- Existing package test suites across engine, merge queue, scheduler, validation, review, task, container, app, and state
- Full repository test run:

```bash
go test ./...
```

Result: all tests passed.
