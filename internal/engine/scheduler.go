package engine

import (
	"context"

	"github.com/openaxiom/axiom/internal/state"
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
}

func (s *engineModelSelector) SelectModel(_ context.Context, tier state.TaskTier, _ string) (string, string, error) {
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
	return models[0].ID, models[0].Family, nil
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
