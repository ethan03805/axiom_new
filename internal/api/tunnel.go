package api

import (
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
)

// Tunnel manages a Cloudflare Tunnel for remote Claw access.
// Per Architecture Section 24.4.
type Tunnel struct {
	localAddr string
	mu        sync.Mutex
	cmd       *exec.Cmd
	running   bool
	url       string
	log       *slog.Logger
}

// NewTunnel creates a tunnel manager for the given local address.
func NewTunnel(localAddr string) *Tunnel {
	return &Tunnel{
		localAddr: localAddr,
		log:       slog.Default(),
	}
}

// Start launches a Cloudflare Tunnel pointing to the local API server.
// The tunnel URL is available via URL() after start succeeds.
func (t *Tunnel) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return fmt.Errorf("tunnel already running")
	}

	// Check that cloudflared is available
	path, err := exec.LookPath("cloudflared")
	if err != nil {
		return fmt.Errorf("cloudflared not found: install from https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/install-and-setup/installation/ : %w", err)
	}

	t.cmd = exec.Command(path, "tunnel", "--url", "http://"+t.localAddr)

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("starting tunnel: %w", err)
	}

	t.running = true
	t.log.Info("tunnel started", "local_addr", t.localAddr)
	return nil
}

// Stop shuts down the tunnel process.
func (t *Tunnel) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running || t.cmd == nil {
		return nil
	}

	if t.cmd.Process != nil {
		if err := t.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("killing tunnel process: %w", err)
		}
	}

	t.running = false
	t.url = ""
	t.log.Info("tunnel stopped")
	return nil
}

// Running returns whether the tunnel is active.
func (t *Tunnel) Running() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// URL returns the public tunnel URL, or empty if not running.
func (t *Tunnel) URL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url
}
