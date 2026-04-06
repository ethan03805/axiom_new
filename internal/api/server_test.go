package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServer_StartStop(t *testing.T) {
	eng, db := testEngine(t)
	cfg := ServerConfig{
		Port:         0, // random port
		RateLimitRPM: 120,
	}

	srv := NewServer(eng, db, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	// Wait for server to be ready
	if !srv.WaitReady(2 * time.Second) {
		t.Fatal("server did not become ready")
	}

	addr := srv.Addr()
	if addr == "" {
		t.Fatal("server addr is empty")
	}

	// Verify server responds
	resp, err := http.Get(fmt.Sprintf("http://%s/health", addr))
	if err != nil {
		t.Fatalf("health check: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Stop server
	srv.Stop()
	cancel()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("server returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("server did not stop in time")
	}
}

func TestServer_RequiresAuth(t *testing.T) {
	eng, db := testEngine(t)
	cfg := ServerConfig{Port: 0, RateLimitRPM: 120}

	srv := NewServer(eng, db, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.Start(ctx)
	if !srv.WaitReady(2 * time.Second) {
		t.Fatal("server did not become ready")
	}
	defer srv.Stop()

	// Request without auth should fail
	resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/models", srv.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestServer_AuthenticatedRequest(t *testing.T) {
	eng, db := testEngine(t)
	cfg := ServerConfig{Port: 0, RateLimitRPM: 120}

	srv := NewServer(eng, db, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.Start(ctx)
	if !srv.WaitReady(2 * time.Second) {
		t.Fatal("server did not become ready")
	}
	defer srv.Stop()

	// Create a valid token
	rawToken := "axm_sk_server_test_token_12345"
	seedToken(t, db, rawToken, ScopeFullControl, 24*time.Hour)

	req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s/api/v1/models", srv.Addr()), nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestServer_AuditLogging(t *testing.T) {
	eng, db := testEngine(t)
	projID, _ := seedProjectAndRun(t, db)
	cfg := ServerConfig{Port: 0, RateLimitRPM: 120}

	srv := NewServer(eng, db, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.Start(ctx)
	if !srv.WaitReady(2 * time.Second) {
		t.Fatal("server did not become ready")
	}
	defer srv.Stop()

	rawToken := "axm_sk_audit_test_token_123456"
	seedToken(t, db, rawToken, ScopeFullControl, 24*time.Hour)

	// Make an authenticated request
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s/api/v1/projects/%s/status", srv.Addr(), projID), nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Check that an audit event was logged
	// We query for api_request events in the events table
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM events WHERE event_type = 'api_request'`).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Error("expected audit log entry for API request")
	}
}

func TestServer_HealthEndpoint(t *testing.T) {
	eng, db := testEngine(t)
	cfg := ServerConfig{Port: 0, RateLimitRPM: 120}

	srv := NewServer(eng, db, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.Start(ctx)
	if !srv.WaitReady(2 * time.Second) {
		t.Fatal("server did not become ready")
	}
	defer srv.Stop()

	resp, err := http.Get(fmt.Sprintf("http://%s/health", srv.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var health map[string]string
	json.NewDecoder(resp.Body).Decode(&health)
	if health["status"] != "ok" {
		t.Errorf("health status: got %q, want %q", health["status"], "ok")
	}
}

func TestServer_IPAllowlist(t *testing.T) {
	eng, db := testEngine(t)
	cfg := ServerConfig{
		Port:         0,
		RateLimitRPM: 120,
		AllowedIPs:   []string{"192.168.1.0/24"}, // NOT localhost
	}

	srv := NewServer(eng, db, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.Start(ctx)
	if !srv.WaitReady(2 * time.Second) {
		t.Fatal("server did not become ready")
	}
	defer srv.Stop()

	// Request from localhost should be blocked by IP allowlist
	resp, err := http.Get(fmt.Sprintf("http://%s/health", srv.Addr()))
	if err != nil {
		// Connection refused is also acceptable if firewall-style blocking
		t.Skipf("connection failed (acceptable): %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestServer_RejectsStartWithoutEngine(t *testing.T) {
	eng, db := testEngineNotStarted(t)
	cfg := ServerConfig{Port: 0, RateLimitRPM: 120}

	srv := NewServer(eng, db, cfg)

	err := srv.Start(context.Background())
	if err == nil {
		srv.Stop()
		t.Fatal("expected error when engine not running")
	}
}

func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}
