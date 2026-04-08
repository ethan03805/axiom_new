package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"sort"
	"time"

	"github.com/openaxiom/axiom/internal/bitnet"
	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/container"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/gitops"
	"github.com/openaxiom/axiom/internal/index"
	"github.com/openaxiom/axiom/internal/inference"
	"github.com/openaxiom/axiom/internal/models"
	"github.com/openaxiom/axiom/internal/observability"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/review"
	"github.com/openaxiom/axiom/internal/security"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/openaxiom/axiom/internal/task"
	"github.com/openaxiom/axiom/internal/validation"
)

// ErrNoInferenceProvider is returned by Open when the configured orchestrator
// runtime requires a provider that has not been configured. See Issue 07 and
// Architecture §19.5.
var ErrNoInferenceProvider = errors.New("no inference provider available for configured orchestrator runtime")

// App is the Axiom application composition root.
// It wires together config, state, engine, and services.
type App struct {
	Config      *config.Config
	DB          *state.DB
	Engine      *engine.Engine
	Registry    *models.Registry
	BitNet      *bitnet.Service
	Broker      *inference.Broker
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

	// Phases 6, 7, 18, 19 — wire the inference control plane.
	//
	// The broker owns provider routing, budget enforcement, model-tier
	// allowlists, secret-aware local-forcing, rate limiting, cost logging,
	// and sanitized prompt persistence. It depends on:
	//   - a shared *events.Bus so subscribers see every emitted event,
	//   - the registry's BrokerMaps() snapshot for pricing + tier data,
	//   - a PromptLogger scoped to the project root,
	//   - zero or more concrete providers (OpenRouter, BitNet).
	//
	// The bus is constructed here and shared with engine.New so both sides
	// publish to the same subscribers. Per Issue 07 §4.2 Option A, this
	// avoids any partially-initialized window between the broker and the
	// engine's IPC monitor.
	sharedBus := events.New(db, log)
	securityPolicy := security.NewPolicy(cfg.Security)
	promptLogger := observability.NewPromptLogger(
		root,
		cfg.Observability.LogPrompts,
		securityPolicy,
	)

	var cloudProvider inference.Provider
	if cfg.Inference.OpenRouterAPIKey != "" {
		timeout := time.Duration(cfg.Inference.TimeoutSeconds) * time.Second
		cloudProvider = inference.NewOpenRouterProvider(
			cfg.Inference.OpenRouterBase,
			cfg.Inference.OpenRouterAPIKey,
			inference.WithTimeout(timeout),
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
		Bus:           sharedBus,
		Log:           log,
		CloudProvider: cloudProvider,
		LocalProvider: localProvider,
		ModelPricing:  pricing,
		ModelTiers:    tiers,
		PromptLogger:  promptLogger,
	})

	// Cross-check runtime configuration against available providers before
	// building the engine. Fail fast on misconfiguration; warn (do not abort)
	// when providers simply cannot be reached right now, since offline
	// startup for later local-only runs is a legitimate flow.
	if err := checkInferencePlane(cfg, broker, cloudProvider, localProvider, log); err != nil {
		db.Close()
		return nil, err
	}

	eng, err := engine.New(engine.Options{
		Config:     cfg,
		DB:         db,
		RootDir:    root,
		Log:        log,
		Bus:        sharedBus,
		Git:        gitSvc,
		Container:  containerSvc,
		Inference:  broker,
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
		Broker:      broker,
		ProjectRoot: root,
		Log:         log,
	}, nil
}

// checkInferencePlane cross-checks the configured orchestrator runtime
// against the providers that were actually constructed, and emits a
// single INFO / WARN startup summary.
//
// Policy:
//   - If the runtime requires a cloud provider and none is configured,
//     return ErrNoInferenceProvider (fail loud, not silent).
//   - If at least one provider is configured but none currently reports
//     Available(), log a WARN and emit a ProviderUnavailable event but do
//     not abort — the user may be offline and intend to configure later.
//   - Otherwise, log a single INFO line naming available providers, the
//     budget ceiling, and whether prompt logging is enabled. The API key
//     itself is never logged.
func checkInferencePlane(
	cfg *config.Config,
	broker *inference.Broker,
	cloud inference.Provider,
	local inference.Provider,
	log *slog.Logger,
) error {
	if broker == nil {
		return errors.New("inference broker is nil")
	}

	// Collect configured provider names (deterministic order for tests
	// and operator-friendly log grepping).
	var providers []string
	if cloud != nil {
		providers = append(providers, cloud.Name())
	}
	if local != nil {
		providers = append(providers, local.Name())
	}
	sort.Strings(providers)

	// Orchestrator runtime cross-check. The set of "cloud-required"
	// runtimes is currently every runtime except explicit local-only
	// modes (none of which exist yet), so any configured runtime that
	// implies cloud meeseeks needs an OpenRouter key.
	runtime := cfg.Orchestrator.Runtime
	cloudRequired := runtime == "claw" ||
		runtime == "claude-code" ||
		runtime == "codex" ||
		runtime == "opencode"
	if cloudRequired && cloud == nil {
		return fmt.Errorf("%w: runtime %q requires an openrouter API key", ErrNoInferenceProvider, runtime)
	}

	// Degenerate case: no providers at all. This should be unreachable
	// for a valid config (Default enables BitNet and Validate rejects
	// unknown runtimes), but we still guard it explicitly.
	if len(providers) == 0 {
		return fmt.Errorf("%w: no providers configured", ErrNoInferenceProvider)
	}

	if !broker.Available() {
		// Providers are configured but none is currently reachable.
		// Do not hard-fail; operators may legitimately boot offline.
		log.Warn("inference plane providers unreachable at startup; continuing",
			"providers", providers,
			"runtime", runtime,
		)
	} else {
		log.Info("inference plane ready",
			"providers", providers,
			"budget_max_usd", cfg.Budget.MaxUSD,
			"log_prompts", cfg.Observability.LogPrompts,
			"runtime", runtime,
		)
	}

	return nil
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
