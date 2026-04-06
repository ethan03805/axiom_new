package bitnet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openaxiom/axiom/internal/config"
)

func testConfig() *config.Config {
	cfg := config.Default("test", "test")
	return &cfg
}

// --- Service creation ---

func TestNewService(t *testing.T) {
	cfg := testConfig()
	svc := NewService(cfg)
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
}

// --- Status checks ---

func TestStatusServerDown(t *testing.T) {
	cfg := testConfig()
	cfg.BitNet.Port = 1 // unreachable
	svc := NewService(cfg)

	status := svc.Status(context.Background())
	if status.Running {
		t.Error("expected Running=false when server is down")
	}
}

func TestStatusServerUp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := testConfig()
	svc := NewService(cfg)
	svc.baseURL = server.URL // override for testing

	status := svc.Status(context.Background())
	if !status.Running {
		t.Error("expected Running=true when server is healthy")
	}
}

// --- Model listing ---

func TestListModelsServerUp(t *testing.T) {
	resp := map[string]any{
		"data": []map[string]any{
			{"id": "Falcon3-7B-Instruct-1.58bit", "owned_by": "tiiuae"},
			{"id": "Falcon3-3B-Instruct-1.58bit", "owned_by": "tiiuae"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := testConfig()
	svc := NewService(cfg)
	svc.baseURL = server.URL

	models, err := svc.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
}

func TestListModelsServerDown(t *testing.T) {
	cfg := testConfig()
	cfg.BitNet.Port = 1
	svc := NewService(cfg)

	_, err := svc.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error when server is down")
	}
}

// --- Enabled check ---

func TestEnabledFromConfig(t *testing.T) {
	cfg := testConfig()
	cfg.BitNet.Enabled = true
	svc := NewService(cfg)
	if !svc.Enabled() {
		t.Error("expected Enabled=true")
	}

	cfg.BitNet.Enabled = false
	svc2 := NewService(cfg)
	if svc2.Enabled() {
		t.Error("expected Enabled=false")
	}
}

// --- BaseURL construction ---

func TestBaseURL(t *testing.T) {
	cfg := testConfig()
	cfg.BitNet.Host = "localhost"
	cfg.BitNet.Port = 3002
	svc := NewService(cfg)
	if svc.BaseURL() != "http://localhost:3002" {
		t.Errorf("BaseURL = %q, want http://localhost:3002", svc.BaseURL())
	}
}

// --- Start/Stop commands ---

func TestStartWhenDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.BitNet.Enabled = false
	svc := NewService(cfg)

	err := svc.Start(context.Background())
	if err == nil {
		t.Fatal("expected error when BitNet is disabled")
	}
	if err != ErrDisabled {
		t.Errorf("err = %v, want ErrDisabled", err)
	}
}

func TestStopWhenNotRunning(t *testing.T) {
	cfg := testConfig()
	svc := NewService(cfg)

	err := svc.Stop(context.Background())
	if err == nil {
		t.Fatal("expected error when not running")
	}
	if err != ErrNotRunning {
		t.Errorf("err = %v, want ErrNotRunning", err)
	}
}

// --- Weight path resolution ---

func TestDefaultWeightDir(t *testing.T) {
	cfg := testConfig()
	svc := NewService(cfg)

	dir := svc.WeightDir()
	if dir == "" {
		t.Error("WeightDir should not be empty")
	}
}

// --- Status struct fields ---

func TestStatusFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
			})
			return
		}
		if r.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "Falcon3-7B-Instruct-1.58bit", "owned_by": "tiiuae"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := testConfig()
	svc := NewService(cfg)
	svc.baseURL = server.URL

	status := svc.Status(context.Background())
	if !status.Running {
		t.Error("expected Running=true")
	}
	if status.Endpoint != server.URL {
		t.Errorf("Endpoint = %q, want %q", status.Endpoint, server.URL)
	}
	if status.ModelCount != 1 {
		t.Errorf("ModelCount = %d, want 1", status.ModelCount)
	}
}
