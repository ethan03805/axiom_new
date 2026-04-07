// Package container implements Docker container lifecycle management for the
// Axiom engine per Architecture Sections 12 and 20. All containers run with
// hardening flags (Section 12.6.1) and communicate via filesystem IPC.
package container

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

// nameSeq ensures container names are unique even when generated at the same nanosecond.
var nameSeq atomic.Int64

// CommandExecutor abstracts Docker CLI execution for testability.
type CommandExecutor interface {
	Run(ctx context.Context, args ...string) (string, error)
	// RunWithExit runs docker with the given args and returns stdout, stderr,
	// and the raw exit code separately. A non-zero exit code is a normal
	// result — only infrastructure failures return a non-nil error.
	RunWithExit(ctx context.Context, args ...string) (stdout, stderr string, exitCode int, err error)
}

// Options configures a new DockerService.
type Options struct {
	ProjectRoot string
	Config      *config.DockerConfig
	DB          *state.DB
	Bus         *events.Bus
	Log         *slog.Logger
	Exec        CommandExecutor
}

// DockerService implements engine.ContainerService using Docker.
// Per Architecture Section 12.6, all container spawning is performed by the engine.
type DockerService struct {
	projectRoot string
	cfg         *config.DockerConfig
	db          *state.DB
	bus         *events.Bus
	log         *slog.Logger
	exec        CommandExecutor
}

// New creates a new DockerService.
func New(opts Options) *DockerService {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &DockerService{
		projectRoot: opts.ProjectRoot,
		cfg:         opts.Config,
		db:          opts.DB,
		bus:         opts.Bus,
		log:         log,
		exec:        opts.Exec,
	}
}

// ContainerName generates a container name following the architecture pattern:
// axiom-<task-id>-<timestamp>
// Per Architecture Section 12.6.
func ContainerName(taskID string) string {
	ts := time.Now().UnixNano()
	seq := nameSeq.Add(1)
	return fmt.Sprintf("axiom-%s-%d-%d", taskID, ts, seq)
}

// Start creates and runs a Docker container per the spec.
// It persists a ContainerSession to SQLite and emits a ContainerStarted event.
// Returns the container session ID (which is the container name).
func (d *DockerService) Start(ctx context.Context, spec engine.ContainerSpec) (string, error) {
	args := BuildArgs(spec)

	// Persist container session before starting
	cs := &state.ContainerSession{
		ID:            spec.Name,
		RunID:         spec.Env["AXIOM_RUN_ID"],
		TaskID:        spec.Env["AXIOM_TASK_ID"],
		ContainerType: state.ContainerType(spec.Env["AXIOM_CONTAINER_TYPE"]),
		Image:         spec.Image,
		CPULimit:      &spec.CPULimit,
		MemLimit:      &spec.MemLimit,
	}

	// Default container type if not set
	if cs.ContainerType == "" {
		cs.ContainerType = state.ContainerMeeseeks
	}

	if err := d.db.CreateContainerSession(cs); err != nil {
		return "", fmt.Errorf("persisting container session: %w", err)
	}

	// Execute docker run
	if _, err := d.exec.Run(ctx, args...); err != nil {
		// Mark as stopped on failure
		d.db.MarkContainerStopped(spec.Name, fmt.Sprintf("start_failed: %v", err))
		return "", fmt.Errorf("starting container %s: %w", spec.Name, err)
	}

	d.log.Info("container started",
		"name", spec.Name,
		"image", spec.Image,
	)

	// Emit event
	if d.bus != nil {
		d.bus.Publish(events.EngineEvent{
			Type:    events.ContainerStarted,
			RunID:   cs.RunID,
			TaskID:  cs.TaskID,
			Details: map[string]any{"container_name": spec.Name, "image": spec.Image},
		})
	}

	return spec.Name, nil
}

// Stop stops a running container and records the stop in SQLite.
func (d *DockerService) Stop(ctx context.Context, id string) error {
	// Issue docker stop
	if _, err := d.exec.Run(ctx, "stop", id); err != nil {
		d.log.Warn("docker stop failed, attempting force remove",
			"container", id, "error", err)
		// Fall back to force remove
		if _, err2 := d.exec.Run(ctx, "rm", "-f", id); err2 != nil {
			return fmt.Errorf("stopping container %s: %w", id, err2)
		}
	}

	// Update database
	if err := d.db.MarkContainerStopped(id, "stopped"); err != nil {
		d.log.Error("failed to mark container stopped in db",
			"container", id, "error", err)
	}

	d.log.Info("container stopped", "name", id)

	// Emit event
	if d.bus != nil {
		cs, _ := d.db.GetContainerSession(id)
		runID := ""
		taskID := ""
		if cs != nil {
			runID = cs.RunID
			taskID = cs.TaskID
		}
		d.bus.Publish(events.EngineEvent{
			Type:    events.ContainerStopped,
			RunID:   runID,
			TaskID:  taskID,
			Details: map[string]any{"container_name": id, "exit_reason": "stopped"},
		})
	}

	return nil
}

// ListRunning returns the names of all running axiom-* containers.
// Per Architecture Section 12.6, containers are named axiom-<task-id>-<timestamp>.
func (d *DockerService) ListRunning(ctx context.Context) ([]string, error) {
	out, err := d.exec.Run(ctx, "ps", "--filter", "name=axiom-", "--format", "{{.Names}}")
	if err != nil {
		return nil, fmt.Errorf("listing running containers: %w", err)
	}

	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}

	names := strings.Split(out, "\n")
	var result []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n != "" {
			result = append(result, n)
		}
	}
	return result, nil
}

// Exec runs a command inside a running container via `docker exec` and
// returns the exit code plus captured stdout/stderr. Per Architecture
// Section 13.5, the validation runner uses this to execute language profile
// commands (go build, npm test, …) against the sandbox. A non-zero exit
// code is a normal result; err is non-nil only on infrastructure failures.
func (d *DockerService) Exec(ctx context.Context, containerID string, cmd []string) (engine.ExecResult, error) {
	if len(cmd) == 0 {
		return engine.ExecResult{}, fmt.Errorf("docker exec %s: empty command", containerID)
	}
	args := append([]string{"exec", containerID}, cmd...)
	start := time.Now()
	stdout, stderr, code, err := d.exec.RunWithExit(ctx, args...)
	duration := time.Since(start)
	if err != nil {
		return engine.ExecResult{}, fmt.Errorf("docker exec %s: %w", containerID, err)
	}
	return engine.ExecResult{
		ExitCode: code,
		Stdout:   stdout,
		Stderr:   stderr,
		Duration: duration,
	}, nil
}

// Cleanup removes orphaned axiom-* containers from prior crashed sessions.
// Per Architecture Section 12.6: run orphan cleanup on startup.
func (d *DockerService) Cleanup(ctx context.Context) error {
	orphans, err := d.ListRunning(ctx)
	if err != nil {
		return fmt.Errorf("listing orphans: %w", err)
	}

	if len(orphans) == 0 {
		d.log.Info("no orphaned containers found")
		return nil
	}

	d.log.Info("cleaning up orphaned containers", "count", len(orphans))

	for _, name := range orphans {
		if _, err := d.exec.Run(ctx, "rm", "-f", name); err != nil {
			d.log.Warn("failed to remove orphan container",
				"container", name, "error", err)
			continue
		}
		d.log.Info("removed orphaned container", "name", name)
	}

	return nil
}

// BuildArgs constructs the docker run arguments with all hardening flags
// per Architecture Section 12.6.1.
func BuildArgs(spec engine.ContainerSpec) []string {
	args := []string{"run"}

	// Automatic cleanup on exit (Section 12.6)
	args = append(args, "--rm")

	// Container name (Section 12.6)
	args = append(args, fmt.Sprintf("--name=%s", spec.Name))

	// --- Hardening flags (Section 12.6.1) ---

	// Read-only root filesystem
	args = append(args, "--read-only")

	// Drop all Linux capabilities
	args = append(args, "--cap-drop=ALL")

	// Prevent privilege escalation via setuid/setgid
	args = append(args, "--security-opt=no-new-privileges")

	// PID limit per container (prevents fork bombs)
	args = append(args, "--pids-limit=256")

	// Writable scratch via tmpfs (noexec)
	args = append(args, "--tmpfs", "/tmp:rw,noexec,size=256m")

	// Non-root execution (Section 12.6.1)
	args = append(args, "--user", "1000:1000")

	// Network isolation
	network := spec.Network
	if network == "" {
		network = "none"
	}
	args = append(args, fmt.Sprintf("--network=%s", network))

	// --- Resource limits ---

	if spec.CPULimit > 0 {
		args = append(args, fmt.Sprintf("--cpus=%g", spec.CPULimit))
	}
	if spec.MemLimit != "" {
		args = append(args, fmt.Sprintf("--memory=%s", spec.MemLimit))
	}

	// --- Volume mounts ---
	for _, mount := range spec.Mounts {
		args = append(args, "-v", mount)
	}

	// --- Environment variables ---
	for k, v := range spec.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	// Detached mode for background execution
	args = append(args, "-d")

	// Image must be last
	args = append(args, spec.Image)

	return args
}
