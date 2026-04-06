package api

import (
	"testing"
)

func TestTunnel_NewTunnel(t *testing.T) {
	tun := NewTunnel("localhost:3000")
	if tun == nil {
		t.Fatal("NewTunnel returned nil")
	}
	if tun.Running() {
		t.Error("tunnel should not be running initially")
	}
}

func TestTunnel_StopWithoutStart(t *testing.T) {
	tun := NewTunnel("localhost:3000")
	// Stopping an unstarted tunnel should not panic
	if err := tun.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

func TestTunnel_URL_Empty(t *testing.T) {
	tun := NewTunnel("localhost:3000")
	if url := tun.URL(); url != "" {
		t.Errorf("URL: got %q, want empty", url)
	}
}
