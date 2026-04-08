package engine

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/mergequeue"
	"github.com/openaxiom/axiom/internal/scheduler"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/openaxiom/axiom/internal/testgen"
)

// Options configures a new Engine instance.
type Options struct {
	Config     *config.Config
	DB         *state.DB
	RootDir    string
	Log        *slog.Logger
	// Bus is an optional shared event bus. When non-nil, the engine uses
	// it instead of constructing its own; this lets the composition root
	// (internal/app) hand the same bus to sibling components such as the
	// inference broker so subscribers observe every event. When nil, the
	// engine falls back to events.New(DB, Log) to preserve test call sites.
	Bus        *events.Bus
	Git        GitService
	Container  ContainerService
	Inference  InferenceService
	Index      IndexService
	Models     ModelService
	Validation ValidationService
	Review     ReviewService
	Tasks      TaskService
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

	git        GitService
	container  ContainerService
	inference  InferenceService
	index      IndexService
	models     ModelService
	validation ValidationService
	review     ReviewService
	tasks      TaskService

	sched          *scheduler.Scheduler
	mergeQueue     *mergequeue.Queue
	testGen        *testgen.Service
	workers        *WorkerPool
	activeAttempts sync.Map

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

	bus := opts.Bus
	if bus == nil {
		bus = events.New(opts.DB, opts.Log)
	}

	e := &Engine{
		cfg:        opts.Config,
		db:         opts.DB,
		bus:        bus,
		rootDir:    opts.RootDir,
		log:        opts.Log,
		git:        opts.Git,
		container:  opts.Container,
		inference:  opts.Inference,
		index:      opts.Index,
		models:     opts.Models,
		validation: opts.Validation,
		review:     opts.Review,
		tasks:      opts.Tasks,
	}

	// Create testgen service for test-generation separation (Section 11.5)
	e.testGen = testgen.New(opts.DB, bus, opts.Log)

	// Create scheduler with engine-provided adapters
	e.sched = scheduler.New(scheduler.Options{
		DB:               opts.DB,
		Log:              opts.Log,
		MaxMeeseeks:      opts.Config.Concurrency.MaxMeeseeks,
		ModelSelector:    &engineModelSelector{models: opts.Models, log: opts.Log},
		SnapshotProvider: &engineSnapshotProvider{git: opts.Git, rootDir: opts.RootDir},
		FamilyExcluder:   &engineFamilyExcluder{testGen: e.testGen},
	})

	// Create merge queue with engine-provided adapters
	e.mergeQueue = mergequeue.New(mergequeue.Options{
		ProjectDir: opts.RootDir,
		Log:        opts.Log,
		Git:        &mergeQueueGitAdapter{git: opts.Git, rootDir: opts.RootDir},
		Validator:  &mergeQueueValidatorAdapter{validation: opts.Validation, cfg: opts.Config, log: opts.Log},
		Indexer:    &mergeQueueIndexAdapter{index: opts.Index},
		Locks:      &mergeQueueLockAdapter{sched: e.sched},
		Tasks:      &mergeQueueTaskAdapter{db: opts.DB, sched: e.sched, testGen: e.testGen, log: opts.Log},
		Events:     &mergeQueueEventAdapter{bus: bus},
		Attempts:   &mergeQueueAttemptAdapter{db: opts.DB, rootDir: opts.RootDir, log: opts.Log},
	})

	return e, nil
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

	e.workers.Register("scheduler", e.schedulerLoop, 500*time.Millisecond)
	e.workers.Register("executor", e.executorLoop, 500*time.Millisecond)
	e.workers.Register("merge-queue", e.mergeQueueLoop, 500*time.Millisecond)
	// Future phases will register additional workers:
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

// TestGen returns the engine's test-generation service.
func (e *Engine) TestGen() *testgen.Service { return e.testGen }

// Inference returns the engine's inference service, or nil if none was wired.
// Exposed so the composition root and regression tests can verify that the
// inference control plane is present in the running engine.
func (e *Engine) Inference() InferenceService { return e.inference }

// Git returns the engine's git service. Exposed so the TUI `/diff` slash
// command can compute `git diff <base>...<head>` without re-plumbing a
// separate git dependency into the session layer.
func (e *Engine) Git() GitService { return e.git }

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
