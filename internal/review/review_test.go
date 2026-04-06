package review

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// --- Risky file detection ---

func TestIsRiskyFile_CIConfig(t *testing.T) {
	tests := []struct {
		path  string
		risky bool
	}{
		{".github/workflows/ci.yml", true},
		{"Jenkinsfile", true},
		{".gitlab-ci.yml", true},
		{"src/handler.go", false},
	}
	for _, tt := range tests {
		if got := IsRiskyFile(tt.path); got != tt.risky {
			t.Errorf("IsRiskyFile(%q) = %v, want %v", tt.path, got, tt.risky)
		}
	}
}

func TestIsRiskyFile_PackageManifests(t *testing.T) {
	tests := []struct {
		path  string
		risky bool
	}{
		{"package.json", true},
		{"go.mod", true},
		{"go.sum", true},
		{"requirements.txt", true},
		{"Cargo.toml", true},
		{"Cargo.lock", true},
		{"package-lock.json", true},
		{"yarn.lock", true},
	}
	for _, tt := range tests {
		if got := IsRiskyFile(tt.path); got != tt.risky {
			t.Errorf("IsRiskyFile(%q) = %v, want %v", tt.path, got, tt.risky)
		}
	}
}

func TestIsRiskyFile_InfraAndSecurity(t *testing.T) {
	tests := []struct {
		path  string
		risky bool
	}{
		{"Dockerfile", true},
		{"docker-compose.yml", true},
		{"docker-compose.yaml", true},
		{"terraform/main.tf", true},
		{"Makefile", true},
		{"internal/auth/handler.go", true},        // auth-related path
		{"internal/security/crypto.go", true},       // security-related path
		{"migrations/001_init.sql", true},           // database migration
	}
	for _, tt := range tests {
		if got := IsRiskyFile(tt.path); got != tt.risky {
			t.Errorf("IsRiskyFile(%q) = %v, want %v", tt.path, got, tt.risky)
		}
	}
}

func TestIsRiskyFile_BuildScripts(t *testing.T) {
	tests := []struct {
		path  string
		risky bool
	}{
		{"scripts/build.sh", true},
		{"build.gradle", true},
		{"CMakeLists.txt", true},
	}
	for _, tt := range tests {
		if got := IsRiskyFile(tt.path); got != tt.risky {
			t.Errorf("IsRiskyFile(%q) = %v, want %v", tt.path, got, tt.risky)
		}
	}
}

// --- Reviewer tier escalation ---

func TestReviewerTier_Standard(t *testing.T) {
	tier := ReviewerTier(state.TierStandard, nil)
	if tier != state.TierStandard {
		t.Errorf("ReviewerTier(standard, no risky) = %q, want %q", tier, state.TierStandard)
	}
}

func TestReviewerTier_LocalNoRisky(t *testing.T) {
	tier := ReviewerTier(state.TierLocal, nil)
	if tier != state.TierLocal {
		t.Errorf("ReviewerTier(local, no risky) = %q, want %q", tier, state.TierLocal)
	}
}

func TestReviewerTier_LocalWithRisky(t *testing.T) {
	// Per Section 11.6: risky files always get standard-tier or higher review
	riskyFiles := []string{"Dockerfile", "Makefile"}
	tier := ReviewerTier(state.TierLocal, riskyFiles)
	if tier != state.TierStandard {
		t.Errorf("ReviewerTier(local, risky) = %q, want %q", tier, state.TierStandard)
	}
}

func TestReviewerTier_CheapWithRisky(t *testing.T) {
	riskyFiles := []string{"go.mod"}
	tier := ReviewerTier(state.TierCheap, riskyFiles)
	if tier != state.TierStandard {
		t.Errorf("ReviewerTier(cheap, risky) = %q, want %q", tier, state.TierStandard)
	}
}

func TestReviewerTier_PremiumStaysPremium(t *testing.T) {
	riskyFiles := []string{"Dockerfile"}
	tier := ReviewerTier(state.TierPremium, riskyFiles)
	if tier != state.TierPremium {
		t.Errorf("ReviewerTier(premium, risky) = %q, want %q", tier, state.TierPremium)
	}
}

// --- Model family diversification ---

func TestRequiresDiversification_Standard(t *testing.T) {
	if !RequiresDiversification(state.TierStandard) {
		t.Error("standard tier should require diversification")
	}
}

func TestRequiresDiversification_Premium(t *testing.T) {
	if !RequiresDiversification(state.TierPremium) {
		t.Error("premium tier should require diversification")
	}
}

func TestRequiresDiversification_Local(t *testing.T) {
	if RequiresDiversification(state.TierLocal) {
		t.Error("local tier should not require diversification")
	}
}

func TestRequiresDiversification_Cheap(t *testing.T) {
	if RequiresDiversification(state.TierCheap) {
		t.Error("cheap tier should not require diversification")
	}
}

func TestSelectReviewerModel_Diversified(t *testing.T) {
	models := []engine.ModelInfo{
		{ID: "anthropic/claude-4-sonnet", Family: "anthropic", Tier: "standard"},
		{ID: "openai/gpt-4o", Family: "openai", Tier: "standard"},
		{ID: "meta/llama-3", Family: "meta", Tier: "standard"},
	}

	selected, err := SelectReviewerModel(models, "anthropic", state.TierStandard)
	if err != nil {
		t.Fatalf("SelectReviewerModel: %v", err)
	}
	if selected.Family == "anthropic" {
		t.Errorf("reviewer should be from different family than meeseeks (anthropic), got %q", selected.Family)
	}
}

func TestSelectReviewerModel_NoDiversificationNeeded(t *testing.T) {
	models := []engine.ModelInfo{
		{ID: "anthropic/claude-haiku", Family: "anthropic", Tier: "cheap"},
	}

	// Cheap tier doesn't require diversification
	selected, err := SelectReviewerModel(models, "anthropic", state.TierCheap)
	if err != nil {
		t.Fatalf("SelectReviewerModel: %v", err)
	}
	if selected.ID != "anthropic/claude-haiku" {
		t.Errorf("expected anthropic/claude-haiku for cheap tier, got %q", selected.ID)
	}
}

func TestSelectReviewerModel_NoModelsAvailable(t *testing.T) {
	_, err := SelectReviewerModel(nil, "anthropic", state.TierStandard)
	if err == nil {
		t.Error("expected error when no models available")
	}
}

func TestSelectReviewerModel_AllSameFamily(t *testing.T) {
	models := []engine.ModelInfo{
		{ID: "anthropic/claude-4-sonnet", Family: "anthropic", Tier: "standard"},
		{ID: "anthropic/claude-4-opus", Family: "anthropic", Tier: "premium"},
	}

	// Standard tier requires diversification but all models are anthropic
	// Should still return a model (best-effort)
	selected, err := SelectReviewerModel(models, "anthropic", state.TierStandard)
	if err != nil {
		t.Fatalf("SelectReviewerModel: %v", err)
	}
	// Falls back to same family when no alternative exists
	if selected.Family != "anthropic" {
		t.Errorf("expected fallback to anthropic, got %q", selected.Family)
	}
}

// --- Verdict parsing ---

func TestParseVerdict_Approve(t *testing.T) {
	output := `### Verdict: APPROVE

### Criterion Evaluation
- [x] AC-001: PASS — Implementation is correct
- [x] AC-002: PASS — All edge cases handled`

	verdict, feedback := ParseVerdict(output)
	if verdict != state.ReviewApprove {
		t.Errorf("verdict = %q, want %q", verdict, state.ReviewApprove)
	}
	if feedback != "" {
		t.Errorf("feedback should be empty for approve, got %q", feedback)
	}
}

func TestParseVerdict_Reject(t *testing.T) {
	output := `### Verdict: REJECT

### Criterion Evaluation
- [x] AC-001: PASS — Implementation is correct
- [ ] AC-002: FAIL — Missing null check on line 42

### Feedback (if REJECT)
Line 42 needs a nil check before accessing the map value.`

	verdict, feedback := ParseVerdict(output)
	if verdict != state.ReviewReject {
		t.Errorf("verdict = %q, want %q", verdict, state.ReviewReject)
	}
	if feedback == "" {
		t.Error("expected feedback for rejection")
	}
}

func TestParseVerdict_Malformed(t *testing.T) {
	output := "This is not a valid review output"

	verdict, _ := ParseVerdict(output)
	if verdict != state.ReviewReject {
		t.Errorf("malformed output should default to reject, got %q", verdict)
	}
}

func TestParseVerdict_CaseInsensitive(t *testing.T) {
	output := "### Verdict: approve\n\nLooks good."
	verdict, _ := ParseVerdict(output)
	if verdict != state.ReviewApprove {
		t.Errorf("verdict = %q, want %q", verdict, state.ReviewApprove)
	}
}

// --- Review request building ---

func TestBuildReviewContainerSpec(t *testing.T) {
	spec := BuildReviewContainerSpec(ReviewContainerParams{
		TaskID:    "task-001",
		RunID:     "run-001",
		Image:     "axiom-meeseeks-multi:latest",
		SpecDir:   "/tmp/specs/task-001",
		CPULimit:  0.5,
		MemLimit:  "2g",
	})

	if spec.Network != "none" {
		t.Errorf("Network = %q, want %q", spec.Network, "none")
	}
	if spec.Env["AXIOM_CONTAINER_TYPE"] != "reviewer" {
		t.Errorf("AXIOM_CONTAINER_TYPE = %q, want %q", spec.Env["AXIOM_CONTAINER_TYPE"], "reviewer")
	}
	if spec.Image != "axiom-meeseeks-multi:latest" {
		t.Errorf("Image = %q, want %q", spec.Image, "axiom-meeseeks-multi:latest")
	}
}

// --- Service orchestration ---

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

type mockModelSelector struct {
	models []engine.ModelInfo
}

func (m *mockModelSelector) SelectModel(_ context.Context, tier state.TaskTier, excludeFamily string) (string, string, error) {
	for _, model := range m.models {
		if string(tier) == model.Tier && model.Family != excludeFamily {
			return model.ID, model.Family, nil
		}
	}
	// Fallback to any model of the tier
	for _, model := range m.models {
		if string(tier) == model.Tier {
			return model.ID, model.Family, nil
		}
	}
	return "", "", fmt.Errorf("no model available for tier %s", tier)
}

type mockReviewRunner struct {
	output string
	err    error
}

func (m *mockReviewRunner) Run(_ context.Context, _ string) (string, error) {
	return m.output, m.err
}

func TestService_RunReview_Approve(t *testing.T) {
	containers := &mockContainerService{}
	runner := &mockReviewRunner{
		output: "### Verdict: APPROVE\n\n### Criterion Evaluation\n- [x] AC-001: PASS\n",
	}

	svc := NewService(ServiceOptions{
		Containers: containers,
		Models: &mockModelSelector{
			models: []engine.ModelInfo{
				{ID: "openai/gpt-4o", Family: "openai", Tier: "standard"},
			},
		},
		Runner: runner,
		Log:    testLogger(),
	})

	result, err := svc.RunReview(context.Background(), ReviewRequest{
		TaskID:         "task-001",
		RunID:          "run-001",
		Image:          "test:latest",
		SpecDir:        t.TempDir(),
		TaskTier:       state.TierStandard,
		MeeseeksFamily: "anthropic",
		CPULimit:       0.5,
		MemLimit:       "2g",
		AffectedFiles:  []string{"src/handler.go"},
	})

	if err != nil {
		t.Fatalf("RunReview: %v", err)
	}
	if result.Verdict != state.ReviewApprove {
		t.Errorf("Verdict = %q, want %q", result.Verdict, state.ReviewApprove)
	}
	if !containers.stopCalled {
		t.Error("expected container to be stopped after review")
	}
}

func TestService_RunReview_Reject(t *testing.T) {
	containers := &mockContainerService{}
	runner := &mockReviewRunner{
		output: "### Verdict: REJECT\n\n### Feedback (if REJECT)\nMissing null check.\n",
	}

	svc := NewService(ServiceOptions{
		Containers: containers,
		Models: &mockModelSelector{
			models: []engine.ModelInfo{
				{ID: "openai/gpt-4o", Family: "openai", Tier: "standard"},
			},
		},
		Runner: runner,
		Log:    testLogger(),
	})

	result, err := svc.RunReview(context.Background(), ReviewRequest{
		TaskID:         "task-001",
		RunID:          "run-001",
		Image:          "test:latest",
		SpecDir:        t.TempDir(),
		TaskTier:       state.TierStandard,
		MeeseeksFamily: "anthropic",
		AffectedFiles:  []string{"src/handler.go"},
	})

	if err != nil {
		t.Fatalf("RunReview: %v", err)
	}
	if result.Verdict != state.ReviewReject {
		t.Errorf("Verdict = %q, want %q", result.Verdict, state.ReviewReject)
	}
	if result.Feedback == "" {
		t.Error("expected feedback for rejection")
	}
}

func TestService_RunReview_ContainerStartFails(t *testing.T) {
	containers := &mockContainerService{
		startErr: fmt.Errorf("docker not available"),
	}

	svc := NewService(ServiceOptions{
		Containers: containers,
		Models: &mockModelSelector{
			models: []engine.ModelInfo{
				{ID: "openai/gpt-4o", Family: "openai", Tier: "standard"},
			},
		},
		Runner: &mockReviewRunner{},
		Log:    testLogger(),
	})

	_, err := svc.RunReview(context.Background(), ReviewRequest{
		TaskID:         "task-001",
		RunID:          "run-001",
		Image:          "test:latest",
		SpecDir:        t.TempDir(),
		TaskTier:       state.TierStandard,
		MeeseeksFamily: "anthropic",
		AffectedFiles:  []string{"src/handler.go"},
	})

	if err == nil {
		t.Error("expected error when container start fails")
	}
}

func TestService_RunReview_RiskyFileEscalation(t *testing.T) {
	containers := &mockContainerService{}
	runner := &mockReviewRunner{
		output: "### Verdict: APPROVE\n",
	}

	selector := &mockModelSelector{
		models: []engine.ModelInfo{
			{ID: "local/bitnet", Family: "local", Tier: "local"},
			{ID: "openai/gpt-4o", Family: "openai", Tier: "standard"},
		},
	}

	svc := NewService(ServiceOptions{
		Containers: containers,
		Models:     selector,
		Runner:     runner,
		Log:        testLogger(),
	})

	result, err := svc.RunReview(context.Background(), ReviewRequest{
		TaskID:         "task-001",
		RunID:          "run-001",
		Image:          "test:latest",
		SpecDir:        t.TempDir(),
		TaskTier:       state.TierLocal,
		MeeseeksFamily: "local",
		AffectedFiles:  []string{"Dockerfile", "src/main.go"},
	})

	if err != nil {
		t.Fatalf("RunReview: %v", err)
	}

	// Because Dockerfile is risky, review should be escalated to standard tier
	if result.ReviewerTier != state.TierStandard {
		t.Errorf("ReviewerTier = %q, want %q (risky file escalation)", result.ReviewerTier, state.TierStandard)
	}
}

// --- Orchestrator gate ---

func TestOrchestratorGate_Approve(t *testing.T) {
	result := OrchestratorGate(GateRequest{
		Verdict:  state.ReviewApprove,
		Feedback: "",
	})

	if !result.Approved {
		t.Error("expected orchestrator gate to approve after reviewer approval")
	}
}

func TestOrchestratorGate_RejectOnReviewerReject(t *testing.T) {
	result := OrchestratorGate(GateRequest{
		Verdict:  state.ReviewReject,
		Feedback: "issues found",
	})

	if result.Approved {
		t.Error("expected orchestrator gate to reject after reviewer rejection")
	}
}

// --- FindRiskyFiles ---

func TestFindRiskyFiles(t *testing.T) {
	files := []string{
		"src/handler.go",
		"Dockerfile",
		"internal/auth/handler.go",
		"README.md",
		"go.mod",
	}

	risky := FindRiskyFiles(files)
	if len(risky) != 3 {
		t.Errorf("len(risky) = %d, want 3", len(risky))
	}

	expected := map[string]bool{
		"Dockerfile":              true,
		"internal/auth/handler.go": true,
		"go.mod":                  true,
	}
	for _, f := range risky {
		if !expected[f] {
			t.Errorf("unexpected risky file: %q", f)
		}
	}
}
