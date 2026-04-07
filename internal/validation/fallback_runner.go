package validation

import (
	"context"

	"github.com/openaxiom/axiom/internal/state"
)

// FallbackRunner fails closed when no real validation runner is configured.
type FallbackRunner struct{}

// Run returns a single failing validation result so unconfigured runtimes do not silently pass work.
func (FallbackRunner) Run(_ context.Context, _ string, _ []string, _ bool) []CheckResult {
	return []CheckResult{{
		CheckType:  state.CheckCompile,
		Status:     state.ValidationFail,
		Output:     "validation runner is not configured",
		DurationMs: 0,
	}}
}
