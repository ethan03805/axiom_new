# IPC & Container Lifecycle Reference

Axiom uses filesystem-based IPC and Docker containers to isolate all untrusted agent execution. The engine communicates with containers exclusively through JSON message files — containers have no network access, no project filesystem mount, and no direct inference API access.

## IPC Protocol

### Message Types

All 14 message types from Architecture Section 20.4:

| Type | Direction | Purpose |
|------|-----------|---------|
| `task_spec` | Engine → Meeseeks | Deliver TaskSpec for execution |
| `review_spec` | Engine → Reviewer | Deliver ReviewSpec for evaluation |
| `revision_request` | Engine → Meeseeks | Return feedback for revision |
| `task_output` | Meeseeks → Engine | Submit completed work + manifest |
| `review_result` | Reviewer → Engine | Submit review verdict |
| `inference_request` | Any Agent → Engine | Request model inference |
| `inference_response` | Engine → Any Agent | Return inference result |
| `lateral_message` | Engine ↔ Meeseeks | Brokered lateral communication |
| `action_request` | Agent → Engine | Request privileged action |
| `action_response` | Engine → Agent | Return action result |
| `request_scope_expansion` | Meeseeks → Engine | Request additional files outside declared scope |
| `scope_expansion_response` | Engine → Meeseeks | Approval or denial of scope expansion |
| `context_invalidation_warning` | Engine → Meeseeks | Warning that referenced symbols have changed |
| `shutdown` | Engine → Container | Request graceful container shutdown |

### Message Envelope

Every IPC message is wrapped in a JSON envelope:

```json
{
  "type": "inference_request",
  "task_id": "task-042",
  "timestamp": "2026-04-05T12:00:00Z",
  "payload": { ... }
}
```

Messages are written as sequentially-named JSON files (`msg-000001.json`, `msg-000002.json`, ...) to ensure ordering.

### IPC Directories

Per Architecture Section 28.1, each task gets four directories:

| Directory | Container Mount | Mode | Purpose |
|-----------|----------------|------|---------|
| `.axiom/containers/specs/<task-id>/` | `/workspace/spec/` | read-only | TaskSpec or ReviewSpec input |
| `.axiom/containers/staging/<task-id>/` | `/workspace/staging/` | read-write | Meeseeks output staging |
| `.axiom/containers/ipc/<task-id>/input/` | `/workspace/ipc/input/` | read-write | Engine → Container messages |
| `.axiom/containers/ipc/<task-id>/output/` | `/workspace/ipc/output/` | read-write | Container → Engine messages |

### Go API

```go
import "github.com/openaxiom/axiom/internal/ipc"

// Create per-task directories
dirs, err := ipc.CreateTaskDirs(projectRoot, "task-042")

// Write a message to the container's input directory
env, _ := ipc.NewEnvelope(ipc.MsgShutdown, "task-042", nil)
path, err := ipc.WriteMessage(dirs.Input, env)

// Read all messages from a directory (ordered by filename)
messages, err := ipc.ReadMessages(dirs.Output)

// Get Docker volume mount strings (spec=ro, staging=rw, ipc=rw)
mounts := dirs.VolumeMounts()

// Cleanup when done
err = ipc.CleanupTaskDirs(projectRoot, "task-042")
```

## Spec Writers

### TaskSpec

`WriteTaskSpec` produces a Markdown file at `<spec-dir>/spec.md` matching Architecture Section 10.3:

```go
spec := ipc.TaskSpec{
    TaskID:       "task-042",
    BaseSnapshot: "abc123def",
    Objective:    "Implement user authentication handler",
    ContextBlocks: []ipc.ContextBlock{
        {
            Label:      "Symbol Context (tier: symbol)",
            SourcePath: "internal/auth/service.go",
            StartLine:  12,
            Content:    "func Authenticate(...)",
        },
    },
    InterfaceContract: "func Authenticate(token string) (*User, error)",
    Constraints: ipc.TaskConstraints{
        Language:      "Go 1.25",
        Style:         "standard library conventions",
        Dependencies:  "stdlib only",
        MaxFileLength: 500,
    },
    AcceptanceCriteria: []string{
        "Handler validates JWT tokens",
        "Returns 401 for invalid tokens",
    },
}
err := ipc.WriteTaskSpec(dirs.Spec, spec)
```

The generated spec includes:

- a mandatory output format section directing Meeseeks to write all output files to `/workspace/staging/` with a `manifest.json`
- prompt-safety wrapping for repo-derived context using `<untrusted_repo_content ...>` blocks
- source provenance and line ranges for structured context blocks
- sanitization of instruction-like comments before the spec is written

Legacy `Context string` is still supported, but it now passes through the same prompt-safety wrapper.

### ReviewSpec

`WriteReviewSpec` produces a Markdown file at `<spec-dir>/spec.md` matching Architecture Section 11.7:

```go
spec := ipc.ReviewSpec{
    TaskID:                "task-042",
    OriginalTaskSpec:      "...",
    MeeseeksOutput:        "...",
    MeeseeksOutputSource:  "internal/auth/service.go",
    AutomatedCheckResults: "✅ Compilation: PASS\n✅ Tests: PASS (12/12)",
    ReviewInstructions:    "Evaluate output against TaskSpec.",
}
err := ipc.WriteReviewSpec(dirs.Spec, spec)
```

The review spec includes the standard verdict/criterion/feedback template, and the Meeseeks output is now wrapped as untrusted repository content before being handed to the reviewer.

### Prompt Safety

Phase 18 adds a shared prompt-safety layer to TaskSpec and ReviewSpec generation:

- repo-derived content is wrapped in `<untrusted_repo_content>` blocks
- every wrapped block includes `source="..."` provenance
- line ranges are included when known
- instruction-like comments are replaced with `[COMMENT SANITIZED: instruction-like content removed]`
- excluded or high-secret-density content is replaced with an explicit exclusion marker instead of being copied into the prompt

See [Security, Secret Handling, and Prompt Safety](security-prompt-safety.md) for the full packaging and redaction rules.

## Docker Container Service

### Overview

`DockerService` in `internal/container/` implements the `engine.ContainerService` interface. All Docker execution is abstracted behind a `CommandExecutor` interface for testability.

```go
import "github.com/openaxiom/axiom/internal/container"

svc := container.New(container.Options{
    ProjectRoot: projectRoot,
    Config:      &cfg.Docker,
    DB:          db,
    Bus:         bus,
    Exec:        &realDockerExecutor{},
})
```

### Container Naming

Containers follow the pattern `axiom-<task-id>-<timestamp>-<seq>` per Architecture Section 12.6:

```go
name := container.ContainerName("task-042")
// "axiom-task-042-1712345678000000000-1"
```

### Hardening Flags

Every container is spawned with all hardening flags from Architecture Section 12.6.1:

| Flag | Purpose |
|------|---------|
| `--rm` | Automatic cleanup on exit |
| `--read-only` | Read-only root filesystem |
| `--cap-drop=ALL` | Drop all Linux capabilities |
| `--security-opt=no-new-privileges` | Prevent privilege escalation via setuid/setgid |
| `--pids-limit=256` | PID limit (prevents fork bombs) |
| `--tmpfs /tmp:rw,noexec,size=256m` | Writable scratch via tmpfs |
| `--network=none` | No outbound network access |
| `--user 1000:1000` | Non-root execution |
| `--cpus=<limit>` | CPU limit from config |
| `--memory=<limit>` | Memory limit from config |

```go
args := container.BuildArgs(engine.ContainerSpec{
    Name:     "axiom-task-042-1234-1",
    Image:    "axiom-meeseeks-multi:latest",
    CPULimit: 0.5,
    MemLimit: "2g",
    Network:  "none",
    Mounts:   dirs.VolumeMounts(),
})
// Returns: ["run", "--rm", "--name=...", "--read-only", "--cap-drop=ALL", ...]
```

### Container Images

Per Architecture Section 12.2:

| Image | Contents | Use Case |
|-------|----------|----------|
| `axiom-meeseeks-multi:latest` | Shipped default image from `docker/meeseeks-multi.Dockerfile`; includes the Go toolchain, Node.js/npm, and Python 3/pip | Multi-language projects |
| `axiom-meeseeks-go:latest` | Optional future or custom single-language variant | Go projects |
| `axiom-meeseeks-node:latest` | Optional future or custom single-language variant | JavaScript/TypeScript projects |
| `axiom-meeseeks-python:latest` | Optional future or custom single-language variant | Python projects |

The default image is configured in `.axiom/config.toml`:

```toml
[docker]
image = "axiom-meeseeks-multi:latest"
```

Build the shipped default image from the repo or release bundle root
with:

```bash
docker build -t axiom-meeseeks-multi:latest -f docker/meeseeks-multi.Dockerfile docker
```

Current source-controlled Docker assets cover only the default
multi-language image.

### Lifecycle Methods

| Method | Description |
|--------|-------------|
| `Start(ctx, spec) (string, error)` | Spawns a container, persists ContainerSession to SQLite, emits ContainerStarted event |
| `Stop(ctx, id) error` | Stops a container (`docker stop` with `docker rm -f` fallback), marks as stopped in SQLite, emits ContainerStopped event |
| `ListRunning(ctx) ([]string, error)` | Lists all running `axiom-*` containers via `docker ps` |
| `Cleanup(ctx) error` | Removes orphaned `axiom-*` containers from prior crashed sessions |

### Orphan Cleanup

On engine startup, `Cleanup()` kills any leftover `axiom-*` containers from prior sessions. This handles the crash recovery case per Architecture Section 12.6:

```go
// Called during engine start
if err := svc.Cleanup(ctx); err != nil {
    log.Error("orphan cleanup failed", "error", err)
}
```

### SQLite Tracking

Every container lifecycle is tracked in the `container_sessions` table:

```go
// Start() automatically creates:
ContainerSession{
    ID:            "axiom-task-042-1234-1",
    RunID:         runID,
    TaskID:        taskID,
    ContainerType: state.ContainerMeeseeks,
    Image:         "axiom-meeseeks-multi:latest",
    CPULimit:      0.5,
    MemLimit:      "2g",
    // StartedAt set automatically
}

// Stop() automatically updates:
// StoppedAt = now, ExitReason = "stopped"
```

### Event Emission

Container lifecycle events are emitted to the event bus:

| Event | When |
|-------|------|
| `container_started` | After successful `docker run` |
| `container_stopped` | After container is stopped or destroyed |

Details include `container_name`, `image`, and `exit_reason`.

### Container Types

Four container types are tracked via `state.ContainerType`:

| Type | Used By | Purpose |
|------|---------|---------|
| `meeseeks` | Task execution | Produces code output + manifest.json |
| `validator` | Validation sandbox (Phase 11) | Runs compile/lint/test checks in isolation |
| `reviewer` | Review pipeline (Phase 11) | Evaluates Meeseeks output against TaskSpec |
| `sub_orchestrator` | Sub-orchestrator | Manages sub-task decomposition |

The validation sandbox and reviewer containers are built by `validation.BuildSandboxSpec` and `review.BuildReviewContainerSpec` respectively. Both use the same `engine.ContainerService` interface and Docker hardening flags as Meeseeks containers.

See [Approval Pipeline Reference](approval-pipeline.md) for details on how these containers are orchestrated.

## Security Model

The container isolation enforces these security boundaries from Architecture Section 12.7:

| Layer | Mechanism |
|-------|-----------|
| OS isolation | Docker container (separate filesystem, network, process namespace) |
| Non-root execution | `--user 1000:1000` |
| No project access | Project filesystem is never mounted |
| No network access | `--network=none` |
| Engine-mediated inference | All model calls go through filesystem IPC |
| Staging boundary | Meeseeks can only write to `/workspace/staging/` |
| IPC boundary | Communication only through JSON files |
| Resource limits | CPU, memory, PID caps per container |
| Read-only rootfs | `--read-only` prevents writes outside designated volumes |
| Capability drop | `--cap-drop=ALL` removes all Linux capabilities |
| No privilege escalation | `--security-opt=no-new-privileges` |
| Orphan cleanup | Stale containers destroyed on engine startup |

## Integration with Engine

Pass the Docker service as the `Container` field in `engine.Options`:

```go
eng, err := engine.New(engine.Options{
    Config:    cfg,
    DB:        db,
    RootDir:   root,
    Log:       logger,
    Git:       gitops.New(logger),
    Container: container.New(container.Options{
        ProjectRoot: root,
        Config:      &cfg.Docker,
        DB:          db,
        Bus:         bus,
        Exec:        &dockerExecutor{},
    }),
})
```

At runtime, the engine's attempt executor uses these primitives in a fixed sequence:

1. `ipc.CreateTaskDirs(...)` creates per-task spec, staging, and IPC directories.
2. `ipc.WriteTaskSpec(...)` writes the Meeseeks-facing `spec.md`.
3. `dirs.VolumeMounts()` is passed into the container spec so the Meeseeks sees `/workspace/spec`, `/workspace/staging`, and `/workspace/ipc`.
4. The executor polls `/workspace/ipc/output` for `task_output`, `inference_request`, `request_scope_expansion`, and `action_request` messages.
5. Successful attempts feed the staged output into the approval pipeline and then the merge queue; failed attempts clean the task directories before retry / escalation.

## Test Coverage

| Package | Tests | Coverage |
|---------|-------|---------|
| `internal/ipc` | 24 | Message types (6), envelope serialization (4), directory management (6), spec writers (5), message read/write (3) |
| `internal/container` | 17 | Container naming (2), hardening flags (7), start/stop lifecycle (4), list/cleanup (3), interface compliance (1) |

Container tests use a `mockExecutor` that records Docker commands instead of executing them. IPC tests verify filesystem operations against real temp directories.
