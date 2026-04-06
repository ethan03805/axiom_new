package engine

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/state"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func testDB(t *testing.T) *state.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := state.Open(dbPath, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testConfig() *config.Config {
	cfg := config.Default("test-project", "test-project")
	return &cfg
}

func testEngine(t *testing.T) *Engine {
	t.Helper()
	db := testDB(t)
	cfg := testConfig()
	log := testLogger()
	dir := t.TempDir()

	e, err := New(Options{
		Config:  cfg,
		DB:      db,
		RootDir: dir,
		Log:     log,
		Git:     &noopGitService{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Stop() })
	return e
}

// --- noop service implementations for testing ---

type noopGitService struct {
	currentBranch string
	headSHA       string
	dirty         bool
}

func (n *noopGitService) CurrentBranch(dir string) (string, error) {
	if n.currentBranch == "" {
		return "main", nil
	}
	return n.currentBranch, nil
}

func (n *noopGitService) CreateBranch(dir, name string) error { return nil }

func (n *noopGitService) CurrentHEAD(dir string) (string, error) {
	if n.headSHA == "" {
		return "abc123def456", nil
	}
	return n.headSHA, nil
}

func (n *noopGitService) IsDirty(dir string) (bool, error) {
	return n.dirty, nil
}

type noopContainerService struct{}

func (n *noopContainerService) Start(ctx context.Context, spec ContainerSpec) (string, error) {
	return "container-1", nil
}
func (n *noopContainerService) Stop(ctx context.Context, id string) error { return nil }
func (n *noopContainerService) ListRunning(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (n *noopContainerService) Cleanup(ctx context.Context) error { return nil }

type noopInferenceService struct{ available bool }

func (n *noopInferenceService) Available() bool { return n.available }
func (n *noopInferenceService) Infer(ctx context.Context, req InferenceRequest) (*InferenceResponse, error) {
	return &InferenceResponse{Content: "mock response"}, nil
}

type noopIndexService struct{}

func (n *noopIndexService) Index(ctx context.Context, dir string) error          { return nil }
func (n *noopIndexService) IndexFiles(ctx context.Context, dir string, paths []string) error {
	return nil
}
func (n *noopIndexService) LookupSymbol(ctx context.Context, name, kind string) ([]SymbolResult, error) {
	return nil, nil
}
func (n *noopIndexService) ReverseDependencies(ctx context.Context, symbolName string) ([]ReferenceResult, error) {
	return nil, nil
}
func (n *noopIndexService) ListExports(ctx context.Context, packagePath string) ([]SymbolResult, error) {
	return nil, nil
}
func (n *noopIndexService) FindImplementations(ctx context.Context, interfaceName string) ([]ReferenceResult, error) {
	return nil, nil
}
func (n *noopIndexService) ModuleGraph(ctx context.Context, rootPackage string) (*ModuleGraphResult, error) {
	return nil, nil
}

// --- Engine constructor tests ---

func TestNew(t *testing.T) {
	db := testDB(t)
	cfg := testConfig()
	log := testLogger()
	dir := t.TempDir()

	e, err := New(Options{
		Config:  cfg,
		DB:      db,
		RootDir: dir,
		Log:     log,
		Git:     &noopGitService{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Stop()

	if e.Bus() == nil {
		t.Error("expected non-nil event bus")
	}
}

func TestNew_RequiresConfig(t *testing.T) {
	db := testDB(t)
	log := testLogger()
	dir := t.TempDir()

	_, err := New(Options{
		DB:      db,
		RootDir: dir,
		Log:     log,
	})
	if err == nil {
		t.Fatal("expected error when config is nil")
	}
}

func TestNew_RequiresDB(t *testing.T) {
	cfg := testConfig()
	log := testLogger()
	dir := t.TempDir()

	_, err := New(Options{
		Config:  cfg,
		RootDir: dir,
		Log:     log,
	})
	if err == nil {
		t.Fatal("expected error when DB is nil")
	}
}

func TestNew_RequiresRootDir(t *testing.T) {
	db := testDB(t)
	cfg := testConfig()
	log := testLogger()

	_, err := New(Options{
		Config: cfg,
		DB:     db,
		Log:    log,
	})
	if err == nil {
		t.Fatal("expected error when RootDir is empty")
	}
}

func TestEngine_StartStop(t *testing.T) {
	e := testEngine(t)

	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !e.Running() {
		t.Error("engine should be running after Start")
	}

	e.Stop()

	if e.Running() {
		t.Error("engine should not be running after Stop")
	}
}

func TestEngine_StopIdempotent(t *testing.T) {
	e := testEngine(t)

	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Stop twice should not panic
	e.Stop()
	e.Stop()
}

func TestEngine_EventBusWired(t *testing.T) {
	e := testEngine(t)

	// Subscribe to events via the engine's bus
	ch, subID := e.Bus().Subscribe(nil)
	defer e.Bus().Unsubscribe(subID)

	// The bus should be functional
	if e.Bus() == nil {
		t.Fatal("bus is nil")
	}

	_ = ch // channel exists
}

func TestEngine_ServiceAccess(t *testing.T) {
	db := testDB(t)
	cfg := testConfig()
	log := testLogger()
	dir := t.TempDir()

	git := &noopGitService{}
	container := &noopContainerService{}
	inference := &noopInferenceService{available: true}
	index := &noopIndexService{}

	e, err := New(Options{
		Config:    cfg,
		DB:        db,
		RootDir:   dir,
		Log:       log,
		Git:       git,
		Container: container,
		Inference: inference,
		Index:     index,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Stop()

	// Verify services are accessible
	if e.DB() != db {
		t.Error("DB() should return the provided database")
	}
	if e.Config() != cfg {
		t.Error("Config() should return the provided config")
	}
	if e.RootDir() != dir {
		t.Error("RootDir() should return the provided directory")
	}
}
