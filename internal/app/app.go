package app

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/state"
)

// App is the Axiom application composition root.
// It wires together config, state, engine, and services.
type App struct {
	Config      *config.Config
	DB          *state.DB
	Engine      *engine.Engine
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

	eng, err := engine.New(engine.Options{
		Config:  cfg,
		DB:      db,
		RootDir: root,
		Log:     log,
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("creating engine: %w", err)
	}

	return &App{
		Config:      cfg,
		DB:          db,
		Engine:      eng,
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
