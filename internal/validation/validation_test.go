package validation

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// --- Language detection ---

func TestDetectLanguage_Go(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/go.mod", []byte("module test"), 0o644); err != nil {
		t.Fatal(err)
	}

	langs := DetectLanguages(dir)
	if !containsLang(langs, "go") {
		t.Errorf("expected go in %v", langs)
	}
}

func TestDetectLanguage_Node(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/package.json", []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	langs := DetectLanguages(dir)
	if !containsLang(langs, "node") {
		t.Errorf("expected node in %v", langs)
	}
}

func TestDetectLanguage_Python(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/requirements.txt", []byte("flask"), 0o644); err != nil {
		t.Fatal(err)
	}

	langs := DetectLanguages(dir)
	if !containsLang(langs, "python") {
		t.Errorf("expected python in %v", langs)
	}
}

func TestDetectLanguage_Rust(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/Cargo.toml", []byte("[package]"), 0o644); err != nil {
		t.Fatal(err)
	}

	langs := DetectLanguages(dir)
	if !containsLang(langs, "rust") {
		t.Errorf("expected rust in %v", langs)
	}
}

func TestDetectLanguage_Multi(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/go.mod", []byte("module test"), 0o644)
	os.WriteFile(dir+"/package.json", []byte("{}"), 0o644)

	langs := DetectLanguages(dir)
	if len(langs) < 2 {
		t.Errorf("expected at least 2 languages, got %v", langs)
	}
}

func TestDetectLanguage_Empty(t *testing.T) {
	dir := t.TempDir()
	langs := DetectLanguages(dir)
	if len(langs) != 0 {
		t.Errorf("expected empty, got %v", langs)
	}
}

// --- Validation profiles ---

func TestGetProfile_Go(t *testing.T) {
	p := GetProfile("go")
	if p.CompileCmd == "" {
		t.Error("Go profile CompileCmd is empty")
	}
	if p.LintCmd == "" {
		t.Error("Go profile LintCmd is empty")
	}
	if p.TestCmd == "" {
		t.Error("Go profile TestCmd is empty")
	}
}

func TestGetProfile_Node(t *testing.T) {
	p := GetProfile("node")
	if p.CompileCmd == "" {
		t.Error("Node profile CompileCmd is empty")
	}
}

func TestGetProfile_Python(t *testing.T) {
	p := GetProfile("python")
	if p.LintCmd == "" {
		t.Error("Python profile LintCmd is empty")
	}
}

func TestGetProfile_Rust(t *testing.T) {
	p := GetProfile("rust")
	if p.CompileCmd == "" {
		t.Error("Rust profile CompileCmd is empty")
	}
}

func TestGetProfile_Unknown(t *testing.T) {
	p := GetProfile("cobol")
	if p.CompileCmd != "" || p.LintCmd != "" || p.TestCmd != "" {
		t.Error("unknown language should return empty profile")
	}
}

// --- Sandbox container spec ---

func TestBuildSandboxSpec(t *testing.T) {
	cfg := &config.ValidationConfig{
		TimeoutMinutes: 10,
		CPULimit:       1.0,
		MemLimit:       "4g",
		Network:        "none",
	}

	spec := BuildSandboxSpec(SandboxParams{
		TaskID:      "task-001",
		RunID:       "run-001",
		Image:       "axiom-meeseeks-go:latest",
		StagingDir:  "/tmp/staging/task-001",
		ProjectDir:  "/tmp/project",
		Config:      cfg,
	})

	if spec.Network != "none" {
		t.Errorf("Network = %q, want %q", spec.Network, "none")
	}
	if spec.CPULimit != 1.0 {
		t.Errorf("CPULimit = %g, want 1.0", spec.CPULimit)
	}
	if spec.MemLimit != "4g" {
		t.Errorf("MemLimit = %q, want %q", spec.MemLimit, "4g")
	}
	if spec.Image != "axiom-meeseeks-go:latest" {
		t.Errorf("Image = %q, want %q", spec.Image, "axiom-meeseeks-go:latest")
	}

	// Container type env should be validator
	if spec.Env["AXIOM_CONTAINER_TYPE"] != "validator" {
		t.Errorf("AXIOM_CONTAINER_TYPE = %q, want %q", spec.Env["AXIOM_CONTAINER_TYPE"], "validator")
	}
	if spec.Env["AXIOM_TASK_ID"] != "task-001" {
		t.Errorf("AXIOM_TASK_ID = %q, want %q", spec.Env["AXIOM_TASK_ID"], "task-001")
	}
	if spec.Env["AXIOM_RUN_ID"] != "run-001" {
		t.Errorf("AXIOM_RUN_ID = %q, want %q", spec.Env["AXIOM_RUN_ID"], "run-001")
	}

	// Must have mounts for project (ro) and staging (rw)
	if len(spec.Mounts) < 2 {
		t.Errorf("expected at least 2 mounts, got %d", len(spec.Mounts))
	}
}

func TestBuildSandboxSpec_NetworkAlwaysNone(t *testing.T) {
	cfg := &config.ValidationConfig{
		Network: "bridge", // try to set non-none — should be overridden
	}

	spec := BuildSandboxSpec(SandboxParams{
		TaskID: "task-001",
		RunID:  "run-001",
		Image:  "test:latest",
		Config: cfg,
	})

	if spec.Network != "none" {
		t.Errorf("Network = %q, want %q (sandbox MUST have no network)", spec.Network, "none")
	}
}

// --- Check result aggregation ---

func TestAggregateResults_AllPass(t *testing.T) {
	results := []CheckResult{
		{CheckType: state.CheckCompile, Status: state.ValidationPass},
		{CheckType: state.CheckLint, Status: state.ValidationPass},
		{CheckType: state.CheckTest, Status: state.ValidationPass},
	}

	if !AllPassed(results) {
		t.Error("expected AllPassed = true")
	}
}

func TestAggregateResults_OneFail(t *testing.T) {
	results := []CheckResult{
		{CheckType: state.CheckCompile, Status: state.ValidationPass},
		{CheckType: state.CheckLint, Status: state.ValidationFail, Output: "lint errors"},
		{CheckType: state.CheckTest, Status: state.ValidationSkip},
	}

	if AllPassed(results) {
		t.Error("expected AllPassed = false when lint fails")
	}
}

func TestAggregateResults_Empty(t *testing.T) {
	if !AllPassed(nil) {
		t.Error("expected AllPassed = true for empty results")
	}
}

func TestFormatResults(t *testing.T) {
	results := []CheckResult{
		{CheckType: state.CheckCompile, Status: state.ValidationPass, DurationMs: 1200},
		{CheckType: state.CheckLint, Status: state.ValidationFail, Output: "unused variable", DurationMs: 300},
	}

	summary := FormatResults(results)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

// --- Service orchestration with mock container ---

type mockContainerService struct {
	startCalled bool
	stopCalled  bool
	startErr    error
	stopErr     error
}

func (m *mockContainerService) Start(ctx context.Context, spec engine.ContainerSpec) (string, error) {
	m.startCalled = true
	if m.startErr != nil {
		return "", m.startErr
	}
	return spec.Name, nil
}

func (m *mockContainerService) Stop(ctx context.Context, id string) error {
	m.stopCalled = true
	return m.stopErr
}

func (m *mockContainerService) ListRunning(ctx context.Context) ([]string, error) {
	return nil, nil
}

func (m *mockContainerService) Cleanup(ctx context.Context) error {
	return nil
}

func TestService_RunChecks_ContainerStartFails(t *testing.T) {
	mock := &mockContainerService{
		startErr: fmt.Errorf("docker not available"),
	}

	svc := NewService(ServiceOptions{
		Containers: mock,
		Log:        testLogger(),
	})

	_, err := svc.RunChecks(context.Background(), CheckRequest{
		TaskID:     "task-001",
		RunID:      "run-001",
		Image:      "test:latest",
		StagingDir: t.TempDir(),
		ProjectDir: t.TempDir(),
		Config:     &config.ValidationConfig{Network: "none"},
		Languages:  []string{"go"},
	})

	if err == nil {
		t.Error("expected error when container start fails")
	}
	if !mock.startCalled {
		t.Error("expected container Start to be called")
	}
}

func TestService_RunChecks_ProducesResults(t *testing.T) {
	mock := &mockContainerService{}

	svc := NewService(ServiceOptions{
		Containers: mock,
		Log:        testLogger(),
		// Use a mock runner that returns pass for all checks
		Runner: &mockCheckRunner{
			results: []CheckResult{
				{CheckType: state.CheckCompile, Status: state.ValidationPass, DurationMs: 100},
				{CheckType: state.CheckLint, Status: state.ValidationPass, DurationMs: 50},
				{CheckType: state.CheckTest, Status: state.ValidationPass, DurationMs: 500},
			},
		},
	})

	results, err := svc.RunChecks(context.Background(), CheckRequest{
		TaskID:     "task-001",
		RunID:      "run-001",
		Image:      "test:latest",
		StagingDir: t.TempDir(),
		ProjectDir: t.TempDir(),
		Config:     &config.ValidationConfig{Network: "none"},
		Languages:  []string{"go"},
	})

	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("len(results) = %d, want 3", len(results))
	}
	if !AllPassed(results) {
		t.Error("expected all checks to pass")
	}
	if !mock.stopCalled {
		t.Error("expected container Stop to be called for cleanup")
	}
}

func TestService_RunChecks_SecurityScanSkippedByDefault(t *testing.T) {
	mock := &mockContainerService{}

	svc := NewService(ServiceOptions{
		Containers: mock,
		Log:        testLogger(),
		Runner: &mockCheckRunner{
			results: []CheckResult{
				{CheckType: state.CheckCompile, Status: state.ValidationPass},
				{CheckType: state.CheckLint, Status: state.ValidationPass},
				{CheckType: state.CheckTest, Status: state.ValidationPass},
			},
		},
	})

	results, err := svc.RunChecks(context.Background(), CheckRequest{
		TaskID:     "task-001",
		RunID:      "run-001",
		Image:      "test:latest",
		StagingDir: t.TempDir(),
		ProjectDir: t.TempDir(),
		Config:     &config.ValidationConfig{Network: "none", SecurityScan: false},
		Languages:  []string{"go"},
	})

	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}

	// Should not contain security check when SecurityScan is false
	for _, r := range results {
		if r.CheckType == state.CheckSecurity {
			t.Error("security check should be skipped when SecurityScan is false")
		}
	}
}

func TestService_RunChecks_WithSecurityScan(t *testing.T) {
	mock := &mockContainerService{}

	svc := NewService(ServiceOptions{
		Containers: mock,
		Log:        testLogger(),
		Runner: &mockCheckRunner{
			results: []CheckResult{
				{CheckType: state.CheckCompile, Status: state.ValidationPass},
				{CheckType: state.CheckLint, Status: state.ValidationPass},
				{CheckType: state.CheckTest, Status: state.ValidationPass},
				{CheckType: state.CheckSecurity, Status: state.ValidationPass},
			},
		},
	})

	results, err := svc.RunChecks(context.Background(), CheckRequest{
		TaskID:     "task-001",
		RunID:      "run-001",
		Image:      "test:latest",
		StagingDir: t.TempDir(),
		ProjectDir: t.TempDir(),
		Config:     &config.ValidationConfig{Network: "none", SecurityScan: true},
		Languages:  []string{"go"},
	})

	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}

	hasSecurity := false
	for _, r := range results {
		if r.CheckType == state.CheckSecurity {
			hasSecurity = true
		}
	}
	if !hasSecurity {
		t.Error("expected security check when SecurityScan is true")
	}
}

// --- Dependency cache miss ---

func TestService_RunChecks_DependencyCacheMiss(t *testing.T) {
	mock := &mockContainerService{}

	svc := NewService(ServiceOptions{
		Containers: mock,
		Log:        testLogger(),
		Runner: &mockCheckRunner{
			results: []CheckResult{
				{
					CheckType: state.CheckCompile,
					Status:    state.ValidationFail,
					Output:    "dependency_cache_miss: no cache for lockfile hash abc123",
				},
			},
		},
	})

	results, err := svc.RunChecks(context.Background(), CheckRequest{
		TaskID:     "task-001",
		RunID:      "run-001",
		Image:      "test:latest",
		StagingDir: t.TempDir(),
		ProjectDir: t.TempDir(),
		Config:     &config.ValidationConfig{Network: "none", FailOnCacheMiss: true},
		Languages:  []string{"go"},
	})

	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	if AllPassed(results) {
		t.Error("expected failure on cache miss")
	}
}

// --- mock check runner ---

type mockCheckRunner struct {
	results []CheckResult
}

func (m *mockCheckRunner) Run(_ context.Context, _ string, _ []string, _ bool) []CheckResult {
	return m.results
}

// --- helper ---

func containsLang(langs []string, lang string) bool {
	for _, l := range langs {
		if l == lang {
			return true
		}
	}
	return false
}
