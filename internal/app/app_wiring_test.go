package app

import (
	"io"
	"log/slog"
	"os"
	"reflect"
	"testing"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/validation"
)

func quietWiringLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func wiringCfgWithImage() *config.Config {
	return &config.Config{Docker: config.DockerConfig{Image: "axiom-meeseeks-multi:latest"}}
}

func wiringCfgNoImage() *config.Config {
	return &config.Config{Docker: config.DockerConfig{}}
}

// TestApp_DefaultRunnerIsDockerCheckRunner protects against regressions where
// somebody swaps FallbackRunner back in as the production validator. Per
// Architecture Section 23.3, the default composition root must wire the real
// in-container check runner so the merge queue runs real build/test/lint
// before committing.
func TestApp_DefaultRunnerIsDockerCheckRunner(t *testing.T) {
	// Make sure the escape hatch is off in the test environment.
	_ = os.Unsetenv("AXIOM_VALIDATION_DISABLED")

	want := reflect.TypeOf(&validation.DockerCheckRunner{})
	got := defaultValidationRunnerType()
	if got != want {
		t.Fatalf("default validation runner type = %v, want %v", got, want)
	}
}

// TestApp_EscapeHatchFallsBackToFallbackRunner verifies the explicit opt-out
// path returns the fail-closed runner.
func TestApp_EscapeHatchFallsBackToFallbackRunner(t *testing.T) {
	t.Setenv("AXIOM_VALIDATION_DISABLED", "1")

	runner := buildValidationRunner(wiringCfgWithImage(), nil, quietWiringLogger())
	if _, ok := runner.(validation.FallbackRunner); !ok {
		t.Fatalf("runner = %T, want validation.FallbackRunner when AXIOM_VALIDATION_DISABLED=1", runner)
	}
}

// TestApp_NoDockerImageFallsBackToFallbackRunner verifies that a blank
// docker.image config uses the fail-closed runner.
func TestApp_NoDockerImageFallsBackToFallbackRunner(t *testing.T) {
	_ = os.Unsetenv("AXIOM_VALIDATION_DISABLED")

	runner := buildValidationRunner(wiringCfgNoImage(), nil, quietWiringLogger())
	if _, ok := runner.(validation.FallbackRunner); !ok {
		t.Fatalf("runner = %T, want validation.FallbackRunner when docker.image is empty", runner)
	}
}
