// Package review implements the reviewer pipeline per Architecture
// Sections 11 and 14.2 (Stage 3–4). It handles reviewer container
// spawning, model-family diversification, risky-file escalation,
// verdict parsing, and the orchestrator final gate.
package review

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

// --- Risky file detection (Section 11.6) ---

// riskyPatterns defines file path patterns that require elevated review.
// Per Architecture Section 11.6.
var riskyPatterns = []struct {
	prefix   string
	suffix   string
	contains string
	exact    string
}{
	// CI/CD configuration
	{prefix: ".github/workflows/"},
	{prefix: ".gitlab-ci"},
	{exact: "Jenkinsfile"},
	{prefix: ".circleci/"},

	// Package manifests and lockfiles
	{exact: "package.json"},
	{exact: "package-lock.json"},
	{exact: "yarn.lock"},
	{exact: "pnpm-lock.yaml"},
	{exact: "go.mod"},
	{exact: "go.sum"},
	{exact: "requirements.txt"},
	{exact: "Pipfile"},
	{exact: "Pipfile.lock"},
	{exact: "pyproject.toml"},
	{exact: "Cargo.toml"},
	{exact: "Cargo.lock"},
	{exact: "Gemfile"},
	{exact: "Gemfile.lock"},

	// Infrastructure definitions
	{exact: "Dockerfile"},
	{prefix: "docker-compose"},
	{suffix: ".tf"},
	{suffix: ".tfvars"},

	// Build scripts and Makefiles
	{exact: "Makefile"},
	{exact: "CMakeLists.txt"},
	{exact: "build.gradle"},
	{exact: "build.gradle.kts"},
	{prefix: "scripts/"},

	// Database migrations
	{contains: "migration"},

	// Auth and security code
	{contains: "/auth/"},
	{contains: "/security/"},
	{contains: "/crypto/"},
}

// IsRiskyFile returns true if the file path matches risky-file patterns.
// Per Section 11.6, these files always require standard-tier or higher review.
func IsRiskyFile(path string) bool {
	normalized := filepath.ToSlash(path)
	base := filepath.Base(normalized)

	for _, p := range riskyPatterns {
		if p.exact != "" && base == p.exact {
			return true
		}
		if p.prefix != "" && strings.HasPrefix(normalized, p.prefix) {
			return true
		}
		if p.suffix != "" && strings.HasSuffix(normalized, p.suffix) {
			return true
		}
		if p.contains != "" && strings.Contains(normalized, p.contains) {
			return true
		}
	}
	return false
}

// FindRiskyFiles returns the subset of paths that match risky-file patterns.
func FindRiskyFiles(paths []string) []string {
	var risky []string
	for _, p := range paths {
		if IsRiskyFile(p) {
			risky = append(risky, p)
		}
	}
	return risky
}

// --- Reviewer tier escalation (Section 11.6) ---

// ReviewerTier determines the reviewer model tier, applying risky-file
// escalation. Per Section 11.6, risky files always get standard-tier
// or higher review, regardless of the original task tier.
func ReviewerTier(taskTier state.TaskTier, riskyFiles []string) state.TaskTier {
	if len(riskyFiles) > 0 {
		switch taskTier {
		case state.TierLocal, state.TierCheap:
			return state.TierStandard
		}
	}
	return taskTier
}

// --- Model family diversification (Section 11.3) ---

// RequiresDiversification returns true if the tier requires the reviewer
// to be from a different model family than the Meeseeks.
// Per Section 11.3: diversification is required for standard and premium tiers.
// For local and cheap tiers, it is optional.
func RequiresDiversification(tier state.TaskTier) bool {
	return tier == state.TierStandard || tier == state.TierPremium
}

// SelectReviewerModel picks a reviewer model from the available models.
// For standard/premium tiers, it prefers a different model family.
// Falls back to same family if no alternatives exist.
func SelectReviewerModel(models []engine.ModelInfo, meeseeksFamily string, tier state.TaskTier) (*engine.ModelInfo, error) {
	if len(models) == 0 {
		return nil, fmt.Errorf("no models available for reviewer selection")
	}

	needsDiversity := RequiresDiversification(tier)

	// First pass: find a model from a different family
	if needsDiversity {
		for i := range models {
			if models[i].Family != meeseeksFamily {
				return &models[i], nil
			}
		}
	}

	// Fallback: use any available model (same family OK for cheap/local,
	// or as best-effort for standard/premium when no alternative exists)
	return &models[0], nil
}

// --- Verdict parsing ---

// ParseVerdict extracts the review verdict and feedback from reviewer output.
// Per Architecture Section 11.7, the reviewer outputs a structured format
// with "### Verdict: APPROVE | REJECT".
// Malformed output defaults to REJECT (fail-safe).
func ParseVerdict(output string) (state.ReviewVerdict, string) {
	lines := strings.Split(output, "\n")

	verdict := state.ReviewReject // default to reject (fail-safe)
	verdictFound := false
	var feedbackLines []string
	inFeedback := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		// Look for verdict line
		if strings.Contains(lower, "verdict:") {
			if strings.Contains(lower, "approve") {
				verdict = state.ReviewApprove
			} else {
				verdict = state.ReviewReject
			}
			verdictFound = true
			continue
		}

		// Capture explicit feedback section
		if strings.Contains(lower, "feedback") && strings.Contains(lower, "reject") {
			inFeedback = true
			continue
		}
		if inFeedback && trimmed != "" {
			feedbackLines = append(feedbackLines, trimmed)
		}
	}

	feedback := strings.Join(feedbackLines, "\n")

	// For REJECT verdicts: if no explicit feedback section was found,
	// collect all content after the verdict line as feedback (fail-safe).
	if verdict == state.ReviewReject && feedback == "" && verdictFound {
		var postVerdict []string
		pastVerdict := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			lower := strings.ToLower(trimmed)
			if strings.Contains(lower, "verdict:") {
				pastVerdict = true
				continue
			}
			if pastVerdict && trimmed != "" {
				postVerdict = append(postVerdict, trimmed)
			}
		}
		feedback = strings.Join(postVerdict, "\n")
	}

	if verdict == state.ReviewApprove {
		feedback = "" // no feedback needed for approvals
	}

	return verdict, feedback
}

// --- Container spec building ---

// ReviewContainerParams holds parameters for building a reviewer container spec.
type ReviewContainerParams struct {
	TaskID   string
	RunID    string
	Image    string
	SpecDir  string
	CPULimit float64
	MemLimit string
}

// BuildReviewContainerSpec constructs a container spec for a reviewer.
// Per Architecture Section 11.8: reviewers have no project filesystem access,
// no network, and communicate only via IPC.
func BuildReviewContainerSpec(params ReviewContainerParams) engine.ContainerSpec {
	name := fmt.Sprintf("axiom-reviewer-%s", params.TaskID)

	cpuLimit := params.CPULimit
	if cpuLimit == 0 {
		cpuLimit = 0.5
	}
	memLimit := params.MemLimit
	if memLimit == "" {
		memLimit = "2g"
	}

	var mounts []string
	if params.SpecDir != "" {
		mounts = append(mounts, params.SpecDir+":/workspace/spec:ro")
	}

	return engine.ContainerSpec{
		Name:     name,
		Image:    params.Image,
		CPULimit: cpuLimit,
		MemLimit: memLimit,
		Network:  "none", // Section 11.8: no network
		Mounts:   mounts,
		Env: map[string]string{
			"AXIOM_CONTAINER_TYPE": string(state.ContainerReviewer),
			"AXIOM_TASK_ID":        params.TaskID,
			"AXIOM_RUN_ID":         params.RunID,
		},
	}
}

// --- Service ---

// ReviewRunner abstracts reading the reviewer's output from a container.
type ReviewRunner interface {
	Run(ctx context.Context, containerID string) (string, error)
}

// ModelSelector abstracts model selection for the reviewer.
type ModelSelector interface {
	SelectModel(ctx context.Context, tier state.TaskTier, excludeFamily string) (modelID, modelFamily string, err error)
}

// ServiceOptions configures a new review Service.
type ServiceOptions struct {
	Containers engine.ContainerService
	Models     ModelSelector
	Runner     ReviewRunner
	Log        *slog.Logger
}

// Service orchestrates reviewer lifecycle.
type Service struct {
	containers engine.ContainerService
	models     ModelSelector
	runner     ReviewRunner
	log        *slog.Logger
}

// NewService creates a new review Service.
func NewService(opts ServiceOptions) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		containers: opts.Containers,
		models:     opts.Models,
		runner:     opts.Runner,
		log:        log,
	}
}

// ReviewRequest describes the review to be performed.
type ReviewRequest struct {
	TaskID         string
	RunID          string
	Image          string
	SpecDir        string
	TaskTier       state.TaskTier
	MeeseeksFamily string
	CPULimit       float64
	MemLimit       string
	AffectedFiles  []string
}

// ReviewResult holds the outcome of a review.
type ReviewResult struct {
	Verdict        state.ReviewVerdict
	Feedback       string
	ReviewerModel  string
	ReviewerFamily string
	ReviewerTier   state.TaskTier
}

// RunReview orchestrates the full review pipeline:
// 1. Detect risky files and escalate tier if needed
// 2. Select reviewer model with family diversification
// 3. Start reviewer container
// 4. Collect and parse reviewer output
// 5. Destroy reviewer container
//
// Per Architecture Section 14.2 Stage 3.
func (s *Service) RunReview(ctx context.Context, req ReviewRequest) (*ReviewResult, error) {
	// Step 1: Risky file escalation
	risky := FindRiskyFiles(req.AffectedFiles)
	effectiveTier := ReviewerTier(req.TaskTier, risky)

	if len(risky) > 0 {
		s.log.Info("risky files detected, escalating review tier",
			"task", req.TaskID,
			"risky_files", risky,
			"original_tier", req.TaskTier,
			"effective_tier", effectiveTier,
		)
	}

	// Step 2: Select reviewer model
	excludeFamily := ""
	if RequiresDiversification(effectiveTier) {
		excludeFamily = req.MeeseeksFamily
	}

	modelID, modelFamily, err := s.models.SelectModel(ctx, effectiveTier, excludeFamily)
	if err != nil {
		return nil, fmt.Errorf("selecting reviewer model: %w", err)
	}

	s.log.Info("reviewer model selected",
		"task", req.TaskID,
		"model", modelID,
		"family", modelFamily,
		"tier", effectiveTier,
	)

	// Step 3: Start reviewer container
	spec := BuildReviewContainerSpec(ReviewContainerParams{
		TaskID:   req.TaskID,
		RunID:    req.RunID,
		Image:    req.Image,
		SpecDir:  req.SpecDir,
		CPULimit: req.CPULimit,
		MemLimit: req.MemLimit,
	})

	containerID, err := s.containers.Start(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("starting reviewer container: %w", err)
	}

	defer func() {
		if stopErr := s.containers.Stop(ctx, containerID); stopErr != nil {
			s.log.Warn("failed to stop reviewer container",
				"container", containerID, "error", stopErr)
		}
	}()

	// Step 4: Run review and parse output
	output, err := s.runner.Run(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("running review: %w", err)
	}

	verdict, feedback := ParseVerdict(output)

	return &ReviewResult{
		Verdict:        verdict,
		Feedback:       feedback,
		ReviewerModel:  modelID,
		ReviewerFamily: modelFamily,
		ReviewerTier:   effectiveTier,
	}, nil
}

// --- Orchestrator gate (Section 14.2 Stage 4) ---

// GateRequest holds the inputs for the orchestrator final gate.
type GateRequest struct {
	Verdict  state.ReviewVerdict
	Feedback string
}

// GateResult holds the orchestrator gate decision.
type GateResult struct {
	Approved bool
	Feedback string
}

// OrchestratorGate implements the final approval gate per Section 14.2 Stage 4.
// The orchestrator validates the approved output against SRS requirements.
// Currently a pass-through for reviewer decisions; future versions will
// include SRS cross-validation via IPC.
func OrchestratorGate(req GateRequest) GateResult {
	if req.Verdict == state.ReviewApprove {
		return GateResult{Approved: true}
	}
	return GateResult{
		Approved: false,
		Feedback: req.Feedback,
	}
}
