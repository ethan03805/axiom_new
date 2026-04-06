package bitnet

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestBitNetHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_BITNET_HELPER") != "1" {
		return
	}

	portArg := os.Args[len(os.Args)-1]
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "bitnet/falcon3-1b", "owned_by": "tiiuae"},
			},
		})
	})

	srv := &http.Server{
		Addr:    "127.0.0.1:" + portArg,
		Handler: mux,
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestServiceStartStop_ManagesConfiguredServerProcess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cfg := testConfig()
	cfg.BitNet.Host = "127.0.0.1"
	cfg.BitNet.Port = port
	cfg.BitNet.Command = os.Args[0]
	cfg.BitNet.Args = []string{"-test.run=TestBitNetHelperProcess", "--", strconv.Itoa(port)}
	cfg.BitNet.StartupTimeoutSeconds = 10

	home := t.TempDir()
	svc := NewService(
		cfg,
		WithHomeDir(func() (string, error) { return home, nil }),
		WithCommandFactory(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Env = append(os.Environ(), "GO_WANT_BITNET_HELPER=1")
			return cmd
		}),
	)

	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := svc.Status(context.Background())
		if status.Running {
			if status.ModelCount != 1 {
				t.Fatalf("ModelCount = %d, want 1", status.ModelCount)
			}
			if !status.Managed {
				t.Fatal("expected started server to be marked as Axiom-managed")
			}
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	status := svc.Status(context.Background())
	if status.Running {
		t.Fatal("expected server to be stopped")
	}
	if status.Managed {
		t.Fatal("expected stopped server to no longer be marked as managed")
	}
}
