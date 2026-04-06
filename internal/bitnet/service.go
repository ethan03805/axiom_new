// Package bitnet implements BitNet local inference server lifecycle management.
package bitnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/openaxiom/axiom/internal/config"
)

// Sentinel errors for the BitNet service.
var (
	ErrDisabled   = errors.New("bitnet: local inference is disabled in config")
	ErrNotRunning = errors.New("bitnet: server is not running")
	ErrNoWeights  = errors.New("bitnet: model weights not found")
)

// ServiceStatus holds the current state of the BitNet server.
type ServiceStatus struct {
	Running    bool
	Managed    bool
	PID        int
	Endpoint   string
	ModelCount int
}

// LocalModel represents a model loaded in the BitNet server.
type LocalModel struct {
	ID      string
	OwnedBy string
}

type serviceState struct {
	PID        int       `json:"pid"`
	Command    string    `json:"command"`
	Args       []string  `json:"args,omitempty"`
	WorkingDir string    `json:"working_dir,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

// Option customizes a BitNet service instance.
type Option func(*Service)

// WithHomeDir overrides the home-directory lookup used for state files and model paths.
func WithHomeDir(fn func() (string, error)) Option {
	return func(s *Service) {
		s.homeDir = fn
	}
}

// WithCommandFactory overrides process creation, primarily for tests.
func WithCommandFactory(fn func(context.Context, string, ...string) *exec.Cmd) Option {
	return func(s *Service) {
		s.commandFactory = fn
	}
}

// Service manages the BitNet local inference server lifecycle.
type Service struct {
	cfg            *config.Config
	baseURL        string
	client         *http.Client
	homeDir        func() (string, error)
	commandFactory func(context.Context, string, ...string) *exec.Cmd
}

// NewService creates a new BitNet service manager.
func NewService(cfg *config.Config, opts ...Option) *Service {
	baseURL := fmt.Sprintf("http://%s:%d", cfg.BitNet.Host, cfg.BitNet.Port)
	svc := &Service{
		cfg:     cfg,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		homeDir: os.UserHomeDir,
		commandFactory: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, name, args...)
		},
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// Enabled returns whether BitNet is enabled in the configuration.
func (s *Service) Enabled() bool {
	return s.cfg.BitNet.Enabled
}

// BaseURL returns the configured BitNet server URL.
func (s *Service) BaseURL() string {
	return s.baseURL
}

// WeightDir returns the directory where BitNet model weights are stored.
func (s *Service) WeightDir() string {
	home, err := s.homeDir()
	if err != nil {
		return filepath.Join(".", ".axiom", "bitnet", "models")
	}
	return filepath.Join(home, ".axiom", "bitnet", "models")
}

func (s *Service) stateDir() string {
	home, err := s.homeDir()
	if err != nil {
		return filepath.Join(".", ".axiom", "bitnet")
	}
	return filepath.Join(home, ".axiom", "bitnet")
}

func (s *Service) statePath() string {
	return filepath.Join(s.stateDir(), "service.json")
}

func (s *Service) processLogPath() string {
	return filepath.Join(s.stateDir(), "service.log")
}

// Status checks the health of the BitNet server and returns its status.
func (s *Service) Status(ctx context.Context) ServiceStatus {
	status := ServiceStatus{
		Endpoint: s.baseURL,
	}

	if state, err := s.readState(); err == nil {
		status.Managed = true
		status.PID = state.PID
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/health", nil)
	if err != nil {
		return status
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return status
	}
	resp.Body.Close()

	if resp.StatusCode >= 500 {
		return status
	}

	status.Running = true

	models, err := s.ListModels(ctx)
	if err == nil {
		status.ModelCount = len(models)
	}

	return status
}

// ListModels queries the BitNet server for currently loaded models.
func (s *Service) ListModels(ctx context.Context) ([]LocalModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("bitnet: create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bitnet: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bitnet: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bitnet: server error %d: %s", resp.StatusCode, string(body))
	}

	var modelsResp struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		return nil, fmt.Errorf("bitnet: decode response: %w", err)
	}

	models := make([]LocalModel, len(modelsResp.Data))
	for i, m := range modelsResp.Data {
		models[i] = LocalModel{ID: m.ID, OwnedBy: m.OwnedBy}
	}
	return models, nil
}

// Start launches the configured BitNet server process and waits for it to become healthy.
func (s *Service) Start(ctx context.Context) error {
	if !s.cfg.BitNet.Enabled {
		return ErrDisabled
	}

	status := s.Status(ctx)
	if status.Running {
		return nil
	}

	if s.cfg.BitNet.Command == "" {
		return fmt.Errorf("bitnet: manual setup required; configure [bitnet].command (and optionally args/working_dir) or start the server manually")
	}
	if s.cfg.BitNet.WorkingDir != "" {
		if info, err := os.Stat(s.cfg.BitNet.WorkingDir); err != nil || !info.IsDir() {
			return fmt.Errorf("bitnet: manual setup required; working_dir %q is not available", s.cfg.BitNet.WorkingDir)
		}
	}

	if err := os.MkdirAll(s.stateDir(), 0o755); err != nil {
		return fmt.Errorf("bitnet: create state dir: %w", err)
	}

	cmd := s.commandFactory(ctx, s.cfg.BitNet.Command, s.cfg.BitNet.Args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if s.cfg.BitNet.WorkingDir != "" {
		cmd.Dir = s.cfg.BitNet.WorkingDir
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("bitnet: start process: %w", err)
	}

	if err := s.writeState(serviceState{
		PID:        cmd.Process.Pid,
		Command:    s.cfg.BitNet.Command,
		Args:       append([]string(nil), s.cfg.BitNet.Args...),
		WorkingDir: s.cfg.BitNet.WorkingDir,
		StartedAt:  time.Now().UTC(),
	}); err != nil {
		_ = cmd.Process.Kill()
		return err
	}

	timeout := time.Duration(s.cfg.BitNet.StartupTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.Status(context.Background()).Running {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	_ = cmd.Process.Kill()
	_ = s.clearState()
	return fmt.Errorf("bitnet: timed out waiting for the local server to become healthy at %s", s.baseURL)
}

// Stop terminates an Axiom-managed BitNet server.
func (s *Service) Stop(ctx context.Context) error {
	status := s.Status(ctx)
	state, err := s.readState()
	if err != nil {
		if status.Running {
			return fmt.Errorf("bitnet: server is running but not managed by axiom; stop it manually")
		}
		return ErrNotRunning
	}

	proc, err := os.FindProcess(state.PID)
	if err != nil {
		return fmt.Errorf("bitnet: find process: %w", err)
	}
	if err := proc.Kill(); err != nil {
		return fmt.Errorf("bitnet: stop process: %w", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !s.Status(ctx).Running {
			_ = s.clearState()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	_ = s.clearState()
	return fmt.Errorf("bitnet: process %d was signaled but the server is still responding at %s", state.PID, s.baseURL)
}

func (s *Service) writeState(state serviceState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("bitnet: marshal state: %w", err)
	}
	if err := os.WriteFile(s.statePath(), data, 0o644); err != nil {
		return fmt.Errorf("bitnet: write state: %w", err)
	}
	return nil
}

func (s *Service) readState() (*serviceState, error) {
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		return nil, err
	}
	var state serviceState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("bitnet: decode state: %w", err)
	}
	return &state, nil
}

func (s *Service) clearState() error {
	if err := os.Remove(s.statePath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("bitnet: remove state: %w", err)
	}
	return nil
}
