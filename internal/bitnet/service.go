// Package bitnet implements the BitNet local inference server lifecycle management
// per Architecture Section 19. It provides start/stop/status/models commands for
// the local BitNet server running Falcon3 1.58-bit quantized models.
package bitnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
	Endpoint   string
	ModelCount int
}

// LocalModel represents a model loaded in the BitNet server.
type LocalModel struct {
	ID      string
	OwnedBy string
}

// Service manages the BitNet local inference server lifecycle.
// Per Architecture Section 19.8, BitNet is enabled by default and
// controllable via config.
type Service struct {
	cfg     *config.Config
	baseURL string
	client  *http.Client
}

// NewService creates a new BitNet service manager.
func NewService(cfg *config.Config) *Service {
	baseURL := fmt.Sprintf("http://%s:%d", cfg.BitNet.Host, cfg.BitNet.Port)
	return &Service{
		cfg:     cfg,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
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
// Per Architecture Section 19.9, weights are stored under ~/.axiom/bitnet/models/.
func (s *Service) WeightDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".axiom", "bitnet", "models")
	}
	return filepath.Join(home, ".axiom", "bitnet", "models")
}

// Status checks the health of the BitNet server and returns its status.
func (s *Service) Status(ctx context.Context) ServiceStatus {
	status := ServiceStatus{
		Endpoint: s.baseURL,
	}

	// Health check
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

	// Count loaded models
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

// Start attempts to start the BitNet server process.
// Per Architecture Section 19.9, if no model weights are present,
// the user should be prompted to download them first.
func (s *Service) Start(ctx context.Context) error {
	if !s.cfg.BitNet.Enabled {
		return ErrDisabled
	}

	// Check if already running
	status := s.Status(ctx)
	if status.Running {
		return nil // already running
	}

	// In the initial implementation, we rely on the user starting the BitNet
	// server externally. Full process management (spawning bitnet.cpp) will
	// be implemented when the BitNet binary integration is built.
	//
	// For now, return an error indicating the server needs to be started manually.
	return fmt.Errorf("bitnet: server not running at %s — start it manually with: python run_inference_server.py", s.baseURL)
}

// Stop attempts to stop the BitNet server process.
func (s *Service) Stop(ctx context.Context) error {
	status := s.Status(ctx)
	if !status.Running {
		return ErrNotRunning
	}

	// In the initial implementation, we rely on the user stopping the server
	// externally. Full process management will be added later.
	return fmt.Errorf("bitnet: stop the server manually (Ctrl-C the running process)")
}
