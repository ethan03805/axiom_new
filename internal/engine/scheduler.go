package engine

import (
	"context"
	"log/slog"

	"github.com/openaxiom/axiom/internal/state"
	"github.com/openaxiom/axiom/internal/testgen"
)

// schedulerLoop is the engine worker that runs the scheduler on each tick.
func (e *Engine) schedulerLoop(ctx context.Context) error {
	return e.sched.Tick(ctx)
}

// Scheduler returns the engine's scheduler for external use (e.g., lock release).
func (e *Engine) Scheduler() interface {
	ReleaseLocks(ctx context.Context, taskID string) error
} {
	return e.sched
}

// engineModelSelector adapts ModelService to the scheduler's ModelSelector interface.
type engineModelSelector struct {
	models ModelService
	log    *slog.Logger
}

func (s *engineModelSelector) SelectModel(_ context.Context, tier state.TaskTier, excludeFamily string) (string, string, error) {
	if s.models == nil {
		// Fallback when no model service is available (e.g., tests)
		return "default/" + string(tier), "default", nil
	}
	models, err := s.models.List(string(tier), "")
	if err != nil {
		return "", "", err
	}
	if len(models) == 0 {
		// Fallback to a default model name based on tier
		return "default/" + string(tier), "default", nil
	}

	// Per Architecture Section 11.5: test tasks must use a different model family
	if excludeFamily != "" {
		for _, m := range models {
			if m.Family != excludeFamily {
				return m.ID, m.Family, nil
			}
		}
		// All models at this tier are from the excluded family — fall back to first available.
		// Per Architecture Section 11.5 this SHOULD NOT happen; log a warning.
		if s.log != nil {
			s.log.Warn("all models at tier are from excluded family, test-generation separation violated",
				"tier", tier, "exclude_family", excludeFamily)
		}
	}

	return models[0].ID, models[0].Family, nil
}

// engineFamilyExcluder adapts testgen.Service to the scheduler's FamilyExcluder interface.
// Per Architecture Section 11.5: test tasks must use a different model family.
type engineFamilyExcluder struct {
	testGen *testgen.Service
}

func (f *engineFamilyExcluder) GetExcludeFamily(ctx context.Context, taskID string) (string, error) {
	return f.testGen.GetExcludeFamily(ctx, taskID)
}

// engineSnapshotProvider adapts GitService to the scheduler's SnapshotProvider interface.
type engineSnapshotProvider struct {
	git     GitService
	rootDir string
}

func (p *engineSnapshotProvider) CurrentHEAD() (string, error) {
	if p.git == nil {
		return "unknown", nil
	}
	return p.git.CurrentHEAD(p.rootDir)
}
