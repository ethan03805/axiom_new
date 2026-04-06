package doctor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/bitnet"
	"github.com/openaxiom/axiom/internal/config"
)

type fakeDockerChecker struct {
	availableErr error
	imageErr     error
}

func (f fakeDockerChecker) Available(ctx context.Context) error {
	return f.availableErr
}

func (f fakeDockerChecker) ImagePresent(ctx context.Context, image string) error {
	return f.imageErr
}

type fakeResourceProbe struct {
	snapshot ResourceSnapshot
	err      error
}

func (f fakeResourceProbe) Snapshot(ctx context.Context) (ResourceSnapshot, error) {
	return f.snapshot, f.err
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func findCheck(t *testing.T, report Report, name string) CheckResult {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("check %q not found", name)
	return CheckResult{}
}

func TestServiceRun_ReportsDependencyFailures(t *testing.T) {
	cfg := config.Default("doctor", "doctor")
	cfg.BitNet.Enabled = true

	svc := New(Options{
		Config:      &cfg,
		ProjectRoot: t.TempDir(),
		Docker: fakeDockerChecker{
			availableErr: errors.New("docker daemon unavailable"),
		},
		BitNetStatus: func(ctx context.Context) bitnet.ServiceStatus {
			return bitnet.ServiceStatus{}
		},
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusUnauthorized,
					Body:       io.NopCloser(strings.NewReader("unauthorized")),
					Header:     make(http.Header),
				}, nil
			}),
		},
		ResourceProbe: fakeResourceProbe{
			snapshot: ResourceSnapshot{CPUCount: 8, TotalMemoryBytes: 16 << 30},
		},
	})

	report := svc.Run(context.Background())

	if findCheck(t, report, "docker").Status != StatusFail {
		t.Fatal("expected docker availability check to fail")
	}
	if findCheck(t, report, "bitnet").Status != StatusFail {
		t.Fatal("expected bitnet availability check to fail when enabled but unavailable")
	}
	if findCheck(t, report, "network").Status != StatusPass {
		t.Fatal("expected unauthorized provider probe to still count as reachable")
	}
}

func TestServiceRun_WarnsOnResourcePressureAndPassesCacheChecks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".axiom", "validation"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".axiom", "logs", "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default("doctor", "doctor")
	cfg.Concurrency.MaxMeeseeks = 6
	cfg.Docker.CPULimit = 1
	cfg.BitNet.CPUThreads = 4
	cfg.BitNet.Enabled = true
	cfg.BitNet.Command = "python"
	cfg.BitNet.WorkingDir = root

	svc := New(Options{
		Config:      &cfg,
		ProjectRoot: root,
		Docker:      fakeDockerChecker{},
		BitNetStatus: func(ctx context.Context) bitnet.ServiceStatus {
			return bitnet.ServiceStatus{Running: true, Managed: true}
		},
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusUnauthorized,
					Body:       io.NopCloser(strings.NewReader("unauthorized")),
					Header:     make(http.Header),
				}, nil
			}),
		},
		ResourceProbe: fakeResourceProbe{
			snapshot: ResourceSnapshot{CPUCount: 4, TotalMemoryBytes: 8 << 30},
		},
	})

	report := svc.Run(context.Background())

	if findCheck(t, report, "cache").Status != StatusPass {
		t.Fatal("expected cache readiness check to pass for initialized project directories")
	}
	if findCheck(t, report, "security").Status != StatusPass {
		t.Fatal("expected security check to pass for built-in secret scanner patterns")
	}
	if findCheck(t, report, "resources").Status != StatusWarn {
		t.Fatal("expected resources check to warn when configured CPU pressure exceeds local capacity")
	}
}
