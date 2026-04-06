package engine

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

// Options configures a new Engine instance.
type Options struct {
	Config    *config.Config
	DB        *state.DB
	RootDir   string
	Log       *slog.Logger
	Git       GitService
	Container ContainerService
	Inference InferenceService
	Index     IndexService
	Models    ModelService
}

// Engine is the long-lived trusted control plane runtime.
// It wires config, state, event bus, and service interfaces together.
// All command surfaces (CLI, TUI, API) interact with the engine.
type Engine struct {
	cfg     *config.Config
	db      *state.DB
	bus     *events.Bus
	rootDir string
	log     *slog.Logger

	git       GitService
	container ContainerService
	inference InferenceService
	index     IndexService
	models    ModelService

	workers *WorkerPool

	mu      sync.Mutex
	running bool
	ctx     context.Context
	cancel  context.CancelFunc
}

// New creates a new Engine instance. Call Start() to begin background processing.
func New(opts Options) (*Engine, error) {
	if opts.Config == nil {
		return nil, errors.New("config is required")
	}
	if opts.DB == nil {
		return nil, errors.New("database is required")
	}
	if opts.RootDir == "" {
		return nil, errors.New("root directory is required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}

	bus := events.New(opts.DB, opts.Log)

	return &Engine{
		cfg:       opts.Config,
		db:        opts.DB,
		bus:       bus,
		rootDir:   opts.RootDir,
		log:       opts.Log,
		git:       opts.Git,
		container: opts.Container,
		inference: opts.Inference,
		index:     opts.Index,
		models:    opts.Models,
	}, nil
}

// Start begins background worker loops (scheduler, merge queue, cleanup).
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running {
		return nil
	}

	e.ctx, e.cancel = context.WithCancel(ctx)
	e.workers = NewWorkerPool(e.ctx, e.log)

	// Future phases will register workers here:
	// e.workers.Register("scheduler", e.schedulerLoop, 500*time.Millisecond)
	// e.workers.Register("merge-queue", e.mergeQueueLoop, 500*time.Millisecond)
	// e.workers.Register("cleanup", e.cleanupLoop, 30*time.Second)

	e.workers.Start()
	e.running = true
	e.log.Info("engine started", "root", e.rootDir)
	return nil
}

// Stop shuts down background workers and releases resources.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return
	}

	if e.workers != nil {
		e.workers.Stop()
	}
	if e.cancel != nil {
		e.cancel()
	}
	e.running = false
	e.log.Info("engine stopped")
}

// Running reports whether the engine's background workers are active.
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// Bus returns the engine's event bus for subscribing to events.
func (e *Engine) Bus() *events.Bus { return e.bus }

// DB returns the engine's database handle.
func (e *Engine) DB() *state.DB { return e.db }

// Config returns the engine's configuration.
func (e *Engine) Config() *config.Config { return e.cfg }

// RootDir returns the project root directory.
func (e *Engine) RootDir() string { return e.rootDir }

// emitEvent publishes an event to the bus. If persistence fails,
// the error is logged but does not block the calling operation.
// Per Architecture Section 22.4, events form the audit trail.
func (e *Engine) emitEvent(ev events.EngineEvent) {
	if err := e.bus.Publish(ev); err != nil {
		e.log.Error("failed to emit event",
			"type", ev.Type,
			"run_id", ev.RunID,
			"error", err,
		)
	}
}
