package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"reflect"

	"github.com/openaxiom/axiom/internal/bitnet"
	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/container"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/gitops"
	"github.com/openaxiom/axiom/internal/index"
	"github.com/openaxiom/axiom/internal/models"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/review"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/openaxiom/axiom/internal/task"
	"github.com/openaxiom/axiom/internal/validation"
)

// App is the Axiom application composition root.
// It wires together config, state, engine, and services.
type App struct {
	Config      *config.Config
	DB          *state.DB
	Engine      *engine.Engine
	Registry    *models.Registry
	BitNet      *bitnet.Service
	ProjectRoot string
	Log         *slog.Logger
}

// Open discovers the project, loads config, opens the database, runs migrations,
// and creates the engine runtime.
func Open(log *slog.Logger) (*App, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	root, err := project.Discover(cwd)
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(root)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	dbPath := project.DBPath(root)
	db, err := state.Open(dbPath, log)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := db.Migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	// Phase 7: Create model registry and load shipped models.
	registry := models.NewRegistry(db, log)
	if err := registry.RefreshShipped(); err != nil {
		log.Warn("failed to load shipped models", "error", err)
	}

	// Phase 7: Create BitNet service manager.
	bitnetSvc := bitnet.NewService(cfg)
	gitSvc := gitops.New(log)
	indexer := index.NewIndexerAdapter(index.NewIndexer(db, log))
	modelService := models.NewRegistryAdapter(registry)
	containerSvc := container.New(container.Options{
		ProjectRoot: root,
		Config:      &cfg.Docker,
		DB:          db,
		Log:         log,
		Exec:        container.CLIExecutor{},
	})
	validationSvc := validation.NewService(validation.ServiceOptions{
		Containers: containerSvc,
		Log:        log,
		Runner:     buildValidationRunner(cfg, containerSvc, log),
	})
	reviewSvc := review.NewService(review.ServiceOptions{
		Containers: containerSvc,
		Models:     models.NewSelector(modelService, log),
		Runner:     review.FallbackRunner{},
		Log:        log,
	})
	taskSvc := task.New(db, log)

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
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("creating engine: %w", err)
	}

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

	return &App{
		Config:      cfg,
		DB:          db,
		Engine:      eng,
		Registry:    registry,
		BitNet:      bitnetSvc,
		ProjectRoot: root,
		Log:         log,
	}, nil
}

// Close shuts down the application and engine.
func (a *App) Close() error {
	if a.Engine != nil {
		a.Engine.Stop()
	}
	if a.DB != nil {
		return a.DB.Close()
	}
	return nil
}

// buildValidationRunner selects the validation CheckRunner used by the engine.
//
// By default (when a validation image is configured and the operator has not
// opted out), it returns the real DockerCheckRunner so Stage 2 / Stage 5
// checks actually run language profile commands via docker exec. When no
// image is configured (e.g. in CI/test environments without Docker) or the
// AXIOM_VALIDATION_DISABLED escape hatch is set, it falls back to the
// fail-closed FallbackRunner so unconfigured runtimes never silently pass.
//
// Per Architecture Section 23.3, the merge queue must never commit without
// real build/test/lint checks — this function is where that promise is kept.
func buildValidationRunner(cfg *config.Config, containerSvc engine.ContainerService, log *slog.Logger) validation.CheckRunner {
	if os.Getenv("AXIOM_VALIDATION_DISABLED") == "1" {
		log.Warn("validation runner disabled via AXIOM_VALIDATION_DISABLED; merges will fail closed")
		return validation.FallbackRunner{}
	}
	if cfg == nil || cfg.Docker.Image == "" {
		log.Warn("no validation image configured; using fail-closed fallback runner")
		return validation.FallbackRunner{}
	}
	return validation.NewDockerCheckRunner(containerSvc, log)
}

// defaultValidationRunnerType returns the reflect.Type of the CheckRunner
// that a production Open() would wire given a normal config. It exists so
// tests can assert the default runner without standing up a real Docker
// daemon or full composition root.
func defaultValidationRunnerType() reflect.Type {
	cfg := &config.Config{Docker: config.DockerConfig{Image: "axiom-meeseeks-multi:latest"}}
	// Ensure the env override is not set in test environments.
	prev, hadPrev := os.LookupEnv("AXIOM_VALIDATION_DISABLED")
	_ = os.Unsetenv("AXIOM_VALIDATION_DISABLED")
	defer func() {
		if hadPrev {
			_ = os.Setenv("AXIOM_VALIDATION_DISABLED", prev)
		}
	}()
	return reflect.TypeOf(buildValidationRunner(cfg, nil, slog.Default()))
}

// NewLogger creates a structured logger for Axiom.
// Human-readable for local output, machine-readable fields for internal use.
func NewLogger(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))
}
