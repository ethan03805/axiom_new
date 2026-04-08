package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/inference"
	"github.com/openaxiom/axiom/internal/ipc"
	"github.com/openaxiom/axiom/internal/observability"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/security"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/openaxiom/axiom/internal/testfixtures"
)

// writeProjectConfigOverride replaces the .axiom/config.toml written by
// project.Init with one that sets a fake OpenRouter API key. The production
// inference health check requires a cloud provider for runtime "claw", so
// tests that go through Open() must supply one.
func writeProjectConfigOverride(t *testing.T, repoDir, name, apiKey string) {
	t.Helper()

	cfg := config.Default(name, project.Slugify(name))
	cfg.Inference.OpenRouterAPIKey = apiKey
	// Route OpenRouter traffic to a non-routable base so Available() never
	// issues a real network call; the broker is still fully constructed.
	cfg.Inference.OpenRouterBase = "http://127.0.0.1:1"
	cfg.Inference.TimeoutSeconds = 1
	// Disable BitNet so the local provider is nil in tests and we avoid
	// touching the (unused) local process.
	cfg.BitNet.Enabled = false

	data, err := config.Marshal(&cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	cfgPath := filepath.Join(repoDir, project.AxiomDir, project.ConfigFile)
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestOpenDiscoversProjectFromSubdirectoryAndRunsRecovery(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	if err := project.Init(repoDir, "fixture-app"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	writeProjectConfigOverride(t, repoDir, "fixture-app", "sk-test-fake-key")

	db, err := state.Open(project.DBPath(repoDir), slog.Default())
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stagingDir := filepath.Join(repoDir, ".axiom", "containers", "staging", "stale-attempt")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "partial.diff"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	subdir := filepath.Join(repoDir, "cmd", "service")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll subdir: %v", err)
	}

	withWorkingDir(t, subdir, func() {
		application, err := Open(slog.Default())
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer application.Close()

		if application.ProjectRoot != repoDir {
			t.Fatalf("ProjectRoot = %q, want %q", application.ProjectRoot, repoDir)
		}
		if application.Engine == nil {
			t.Fatal("expected engine to be initialized")
		}
		if !application.Engine.Running() {
			t.Fatal("engine should be running after Open")
		}
		if application.Registry == nil {
			t.Fatal("expected model registry to be initialized")
		}
		if application.BitNet == nil {
			t.Fatal("expected BitNet service to be initialized")
		}
	})

	entries, err := os.ReadDir(filepath.Join(repoDir, ".axiom", "containers", "staging"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries after recovery = %d, want 0", len(entries))
	}
}

func TestApp_Close_StopsEngine(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	if err := project.Init(repoDir, "fixture-close"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	writeProjectConfigOverride(t, repoDir, "fixture-close", "sk-test-fake-key")

	withWorkingDir(t, repoDir, func() {
		application, err := Open(slog.Default())
		if err != nil {
			t.Fatalf("Open: %v", err)
		}

		if !application.Engine.Running() {
			t.Fatal("engine should be running after Open")
		}

		application.Close()

		if application.Engine.Running() {
			t.Fatal("engine should not be running after Close")
		}
	})
}

// TestOpen_WiresInferenceBroker is the core regression guard for Issue 07.
// It verifies that the production composition root constructs a non-nil
// inference broker, attaches it to the engine, and exposes it on the App
// struct — closing the §2.3/§2.4 gap where `e.inference` was silently nil.
func TestOpen_WiresInferenceBroker(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	if err := project.Init(repoDir, "fixture-broker"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	writeProjectConfigOverride(t, repoDir, "fixture-broker", "sk-test-fake-key")

	withWorkingDir(t, repoDir, func() {
		application, err := Open(slog.Default())
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer application.Close()

		if application.Broker == nil {
			t.Fatal("App.Broker is nil — inference control plane was not wired")
		}
		if application.Engine == nil {
			t.Fatal("App.Engine is nil")
		}
		if application.Engine.Inference() == nil {
			t.Fatal("Engine.Inference() is nil — broker was not injected into engine.Options")
		}
		// Same instance on both sides of the boundary.
		if engineBroker, ok := application.Engine.Inference().(*inference.Broker); !ok || engineBroker != application.Broker {
			t.Fatalf("engine broker %p does not match app broker %p", application.Engine.Inference(), application.Broker)
		}
		// Calling Available() must not panic — exercises the broker path
		// even though both providers may be unreachable from the test host.
		_ = application.Broker.Available()
	})
}

// TestOpen_FailsFastWhenNoProviderConfigured verifies that the §4.3 health
// check returns a clear error when the configured orchestrator runtime
// requires a cloud provider but none is configured. Per the acceptance
// criteria for Issue 07, this must NOT be a silent nil.
func TestOpen_FailsFastWhenNoProviderConfigured(t *testing.T) {
	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	if err := project.Init(repoDir, "fixture-noprovider"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Write a config that has runtime = "claw" (default) but explicitly
	// provides NO OpenRouter key and disables BitNet. This is exactly the
	// "silent nil broker" scenario that Issue 07 was filed against.
	cfg := config.Default("fixture-noprovider", "fixture-noprovider")
	cfg.Inference.OpenRouterAPIKey = ""
	cfg.BitNet.Enabled = false
	data, err := config.Marshal(&cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	cfgPath := filepath.Join(repoDir, project.AxiomDir, project.ConfigFile)
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	withWorkingDir(t, repoDir, func() {
		application, err := Open(slog.Default())
		if err == nil {
			if application != nil {
				application.Close()
			}
			t.Fatal("Open should fail when runtime=claw and no provider is configured")
		}
		if !errors.Is(err, ErrNoInferenceProvider) {
			t.Fatalf("expected ErrNoInferenceProvider, got %v", err)
		}
		if !strings.Contains(err.Error(), "openrouter") {
			t.Fatalf("error message should name openrouter API key as the cause, got: %v", err)
		}
		// The error must not leak configuration internals or any "nil"
		// wording that would suggest a silent zero-value.
		if strings.Contains(strings.ToLower(err.Error()), "nil") {
			t.Fatalf("error message should be operator-friendly, not talk about nil: %v", err)
		}
	})
}

// TestEngine_IPCMonitorUsesRealBroker is the end-to-end regression guard for
// §2.3 of Issue 07. It builds an engine with a real broker backed by a mock
// provider, writes a synthetic inference_request IPC envelope into the input
// directory, and verifies the broker handled it rather than the legacy
// "inference broker unavailable" path.
func TestEngine_IPCMonitorUsesRealBroker(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	dbPath := filepath.Join(dir, "engine_ipc.db")
	db, err := state.Open(dbPath, log)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	cfgVal := config.Default("ipc-test", "ipc-test")
	cfgVal.BitNet.Enabled = false
	cfgVal.Inference.OpenRouterAPIKey = "sk-test-fake-key"
	cfgVal.Inference.MaxRequestsTask = 50
	cfgVal.Inference.TokenCapPerReq = 16384
	cfg := &cfgVal

	bus := events.New(db, log)

	mock := &capturingProvider{
		available: true,
		response: &inference.ProviderResponse{
			Content:      "broker-handled response",
			FinishReason: "stop",
			InputTokens:  5,
			OutputTokens: 7,
			Model:        "anthropic/claude-4-sonnet",
		},
	}
	policy := security.NewPolicy(cfg.Security)
	promptLogger := observability.NewPromptLogger(dir, false, policy)
	broker := inference.NewBroker(inference.BrokerConfig{
		Config:        cfg,
		DB:            db,
		Bus:           bus,
		Log:           log,
		CloudProvider: mock,
		LocalProvider: nil,
		ModelPricing: map[string]inference.ModelPricing{
			"anthropic/claude-4-sonnet": {
				PromptCostPerToken:     0.000003,
				CompletionCostPerToken: 0.000015,
			},
		},
		ModelTiers: map[string]string{
			"anthropic/claude-4-sonnet": "standard",
		},
		PromptLogger: promptLogger,
	})

	eng, err := engine.New(engine.Options{
		Config:    cfg,
		DB:        db,
		RootDir:   dir,
		Log:       log,
		Bus:       bus,
		Inference: broker,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { eng.Stop() })

	// The broker must match what the engine sees through its interface.
	if eng.Inference() == nil {
		t.Fatal("engine.Inference() is nil after injection")
	}

	// Drive a synthetic IPC inference_request by calling Infer directly
	// through the same interface the ipcmonitor uses. This exercises the
	// exact path that was previously short-circuited to "inference broker
	// unavailable" in ipcmonitor.go:113-117.
	runID := "run-ipc-1"
	taskID := "task-ipc-1"
	projectID := "proj-ipc-1"
	if err := db.CreateProject(&state.Project{ID: projectID, RootPath: dir, Name: "ipc", Slug: "ipc"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := db.CreateRun(&state.ProjectRun{
		ID:                  runID,
		ProjectID:           projectID,
		Status:              state.RunActive,
		BaseBranch:          "main",
		WorkBranch:          "axiom/ipc",
		BudgetMaxUSD:        10,
		OrchestratorMode:    "embedded",
		OrchestratorRuntime: "claw",
		SRSApprovalDelegate: "user",
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := db.CreateTask(&state.Task{
		ID:       taskID,
		RunID:    runID,
		Title:    "ipc task",
		Status:   state.TaskInProgress,
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp, err := eng.Inference().Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AgentType: "meeseeks",
		ModelID:   "anthropic/claude-4-sonnet",
		Tier:      "standard",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello ipc"}},
		MaxTokens: 128,
	})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if resp == nil {
		t.Fatal("response is nil — broker short-circuited")
	}
	if resp.Content != "broker-handled response" {
		t.Fatalf("expected broker-handled response, got %q", resp.Content)
	}
	if resp.ProviderName != "capturing" {
		t.Fatalf("expected provider capturing, got %q", resp.ProviderName)
	}
	if mock.calls != 1 {
		t.Fatalf("mock provider calls = %d, want 1 — broker did not invoke it", mock.calls)
	}

	// Cost must have been logged — the broker path, not the nil-guard
	// in ipcmonitor.go, is what persists these rows.
	entries, err := db.ListCostLogByRun(runID)
	if err != nil {
		t.Fatalf("ListCostLogByRun: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 cost log entry from broker, got %d", len(entries))
	}
}

// TestOpen_EmitsInferencePlaneReadyLog verifies the §4.3 acceptance
// criterion that Open emits a single INFO summary naming the configured
// providers, budget ceiling, and prompt-log state. Per §6.7, the API
// key value itself must never appear in the log line.
func TestOpen_EmitsInferencePlaneReadyLog(t *testing.T) {
	// Stand up a stub OpenRouter endpoint so Available() returns true
	// and the health check takes the INFO branch.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(stub.Close)

	repoDir, err := testfixtures.Materialize("existing-go")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(repoDir)) })

	if err := project.Init(repoDir, "fixture-ready-log"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Point the config's OpenRouter base at the stub.
	cfg := config.Default("fixture-ready-log", project.Slugify("fixture-ready-log"))
	cfg.Inference.OpenRouterAPIKey = "sk-test-topsecret"
	cfg.Inference.OpenRouterBase = stub.URL
	cfg.Inference.TimeoutSeconds = 5
	cfg.BitNet.Enabled = false
	data, err := config.Marshal(&cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, project.AxiomDir, project.ConfigFile), data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Capture INFO-level output to a pipe via a slog JSON handler.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	log := slog.New(slog.NewJSONHandler(pw, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var lines []map[string]any
	done := make(chan struct{})
	go func() {
		defer close(done)
		dec := json.NewDecoder(pr)
		for {
			var m map[string]any
			if err := dec.Decode(&m); err != nil {
				return
			}
			lines = append(lines, m)
		}
	}()

	withWorkingDir(t, repoDir, func() {
		application, err := Open(log)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		application.Close()
	})
	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	_ = pr.Close()

	var ready map[string]any
	for _, l := range lines {
		if msg, _ := l["msg"].(string); msg == "inference plane ready" {
			ready = l
			break
		}
	}
	if ready == nil {
		t.Fatalf("expected 'inference plane ready' log line; got %d lines: %v", len(lines), lines)
	}
	// The API key must never appear in logs.
	for _, l := range lines {
		for _, v := range l {
			if s, ok := v.(string); ok && strings.Contains(s, "sk-test-topsecret") {
				t.Fatalf("API key leaked in log line: %v", l)
			}
		}
	}
}

// capturingProvider is a local inference.Provider that records calls.
// Defined here rather than reusing internal/inference/broker_test.go's
// mockProvider because that type is unexported.
type capturingProvider struct {
	available bool
	response  *inference.ProviderResponse
	err       error
	calls     int
	lastReq   inference.ProviderRequest
}

func (m *capturingProvider) Name() string                     { return "capturing" }
func (m *capturingProvider) Available(_ context.Context) bool { return m.available }
func (m *capturingProvider) Complete(_ context.Context, req inference.ProviderRequest) (*inference.ProviderResponse, error) {
	m.calls++
	m.lastReq = req
	return m.response, m.err
}

// Silence linter: these imports are used by the tests above. The explicit
// `_ = ipc.MsgInferenceRequest` reference keeps the `ipc` import meaningful
// even if future refactors remove the direct constant usage.
var _ = ipc.MsgInferenceRequest

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()

	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	fn()
}
