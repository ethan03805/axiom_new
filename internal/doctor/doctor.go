package doctor

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/openaxiom/axiom/internal/bitnet"
	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/security"
)

// Status describes the outcome of a doctor check.
type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

// CheckResult is a single doctor check result.
type CheckResult struct {
	Name    string
	Status  Status
	Summary string
}

// Report is the full output of a doctor run.
type Report struct {
	Checks []CheckResult
}

// ResourceSnapshot describes local machine capacity.
type ResourceSnapshot struct {
	CPUCount         int
	TotalMemoryBytes uint64
}

// DockerChecker probes Docker daemon and image availability.
type DockerChecker interface {
	Available(ctx context.Context) error
	ImagePresent(ctx context.Context, image string) error
}

// ResourceProbe provides local resource information.
type ResourceProbe interface {
	Snapshot(ctx context.Context) (ResourceSnapshot, error)
}

// HTTPDoer is the minimal HTTP client interface used by doctor.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Options configures a doctor service.
type Options struct {
	Config        *config.Config
	ProjectRoot   string
	Docker        DockerChecker
	BitNetStatus  func(context.Context) bitnet.ServiceStatus
	HTTPClient    HTTPDoer
	ResourceProbe ResourceProbe
}

// Service runs operational diagnostics.
type Service struct {
	cfg          *config.Config
	projectRoot  string
	docker       DockerChecker
	bitnetStatus func(context.Context) bitnet.ServiceStatus
	httpClient   HTTPDoer
	resource     ResourceProbe
}

// New creates a doctor service.
func New(opts Options) *Service {
	cfg := opts.Config
	if cfg == nil {
		defaultCfg := config.Default("doctor", "doctor")
		cfg = &defaultCfg
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}

	statusFn := opts.BitNetStatus
	if statusFn == nil {
		statusFn = func(context.Context) bitnet.ServiceStatus { return bitnet.ServiceStatus{} }
	}

	resourceProbe := opts.ResourceProbe
	if resourceProbe == nil {
		resourceProbe = localResourceProbe{}
	}

	dockerChecker := opts.Docker
	if dockerChecker == nil {
		dockerChecker = dockerCLI{}
	}

	return &Service{
		cfg:          cfg,
		projectRoot:  opts.ProjectRoot,
		docker:       dockerChecker,
		bitnetStatus: statusFn,
		httpClient:   client,
		resource:     resourceProbe,
	}
}

// Run executes the doctor checks and returns a structured report.
func (s *Service) Run(ctx context.Context) Report {
	report := Report{}
	report.Checks = append(report.Checks, s.checkDocker(ctx))
	report.Checks = append(report.Checks, s.checkBitNet(ctx))
	report.Checks = append(report.Checks, s.checkNetwork(ctx))
	report.Checks = append(report.Checks, s.checkResources(ctx))
	report.Checks = append(report.Checks, s.checkCache(ctx))
	report.Checks = append(report.Checks, s.checkSecurity())
	return report
}

func (s *Service) checkDocker(ctx context.Context) CheckResult {
	if err := s.docker.Available(ctx); err != nil {
		return CheckResult{Name: "docker", Status: StatusFail, Summary: err.Error()}
	}
	return CheckResult{Name: "docker", Status: StatusPass, Summary: "Docker daemon reachable"}
}

func (s *Service) checkBitNet(ctx context.Context) CheckResult {
	if !s.cfg.BitNet.Enabled {
		return CheckResult{Name: "bitnet", Status: StatusSkip, Summary: "BitNet disabled in config"}
	}

	status := s.bitnetStatus(ctx)
	if status.Running {
		return CheckResult{Name: "bitnet", Status: StatusPass, Summary: "BitNet server is reachable"}
	}
	if s.cfg.BitNet.Command == "" {
		return CheckResult{
			Name:    "bitnet",
			Status:  StatusWarn,
			Summary: "BitNet is enabled in manual mode; start the server manually or configure [bitnet].command",
		}
	}
	if s.cfg.BitNet.WorkingDir != "" {
		if info, err := os.Stat(s.cfg.BitNet.WorkingDir); err != nil || !info.IsDir() {
			return CheckResult{Name: "bitnet", Status: StatusFail, Summary: "BitNet working directory is not available"}
		}
	}
	return CheckResult{Name: "bitnet", Status: StatusWarn, Summary: "BitNet is configured but not currently running"}
}

func (s *Service) checkNetwork(ctx context.Context) CheckResult {
	if s.cfg.Inference.OpenRouterBase == "" {
		return CheckResult{Name: "network", Status: StatusSkip, Summary: "No provider URL configured"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.Inference.OpenRouterBase, nil)
	if err != nil {
		return CheckResult{Name: "network", Status: StatusFail, Summary: err.Error()}
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return CheckResult{Name: "network", Status: StatusFail, Summary: err.Error()}
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 500 {
		return CheckResult{Name: "network", Status: StatusFail, Summary: fmt.Sprintf("provider returned %d", resp.StatusCode)}
	}
	return CheckResult{Name: "network", Status: StatusPass, Summary: "Provider endpoint reachable"}
}

func (s *Service) checkResources(ctx context.Context) CheckResult {
	snapshot, err := s.resource.Snapshot(ctx)
	if err != nil {
		return CheckResult{Name: "resources", Status: StatusSkip, Summary: err.Error()}
	}
	if snapshot.CPUCount <= 0 {
		return CheckResult{Name: "resources", Status: StatusSkip, Summary: "CPU capacity unavailable"}
	}

	configuredCPU := float64(s.cfg.Concurrency.MaxMeeseeks)*s.cfg.Docker.CPULimit + float64(s.cfg.BitNet.CPUThreads)
	if configuredCPU > float64(snapshot.CPUCount) {
		return CheckResult{
			Name:    "resources",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("Configured CPU pressure %.1f exceeds local capacity %d", configuredCPU, snapshot.CPUCount),
		}
	}

	return CheckResult{Name: "resources", Status: StatusPass, Summary: "Configured resource pressure is within local CPU capacity"}
}

func (s *Service) checkCache(ctx context.Context) CheckResult {
	if s.projectRoot == "" {
		return CheckResult{Name: "cache", Status: StatusSkip, Summary: "No project root available"}
	}

	required := []string{
		filepath.Join(s.projectRoot, ".axiom", "validation"),
		filepath.Join(s.projectRoot, ".axiom", "logs", "prompts"),
	}
	for _, dir := range required {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return CheckResult{Name: "cache", Status: StatusFail, Summary: fmt.Sprintf("Missing runtime directory %s", dir)}
		}
	}

	if err := s.docker.ImagePresent(ctx, s.cfg.Docker.Image); err != nil {
		return CheckResult{Name: "cache", Status: StatusWarn, Summary: fmt.Sprintf("Docker image %s is not present locally", s.cfg.Docker.Image)}
	}

	return CheckResult{Name: "cache", Status: StatusPass, Summary: "Project cache directories and image baseline are ready"}
}

func (s *Service) checkSecurity() CheckResult {
	if security.NewPolicy(s.cfg.Security) == nil {
		return CheckResult{Name: "security", Status: StatusFail, Summary: "Secret scanner policy failed to initialize"}
	}
	return CheckResult{Name: "security", Status: StatusPass, Summary: "Secret scanner patterns loaded successfully"}
}

type dockerCLI struct{}

func (dockerCLI) Available(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker daemon unavailable: %s", string(out))
	}
	return nil
}

func (dockerCLI) ImagePresent(ctx context.Context, image string) error {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", image)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("image inspect failed: %s", string(out))
	}
	return nil
}

type localResourceProbe struct{}

func (localResourceProbe) Snapshot(ctx context.Context) (ResourceSnapshot, error) {
	_ = ctx
	return ResourceSnapshot{CPUCount: runtime.NumCPU()}, nil
}
