package models

import (
	"context"
	"log/slog"

	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

// Selector adapts an engine.ModelService to the SelectModel shape used by review and scheduler helpers.
type Selector struct {
	models engine.ModelService
	log    *slog.Logger
}

// NewSelector creates a model selector backed by the engine model service.
func NewSelector(models engine.ModelService, log *slog.Logger) *Selector {
	if log == nil {
		log = slog.Default()
	}
	return &Selector{models: models, log: log}
}

// SelectModel returns the first available model at the requested tier, preferring a different family when requested.
func (s *Selector) SelectModel(_ context.Context, tier state.TaskTier, excludeFamily string) (string, string, error) {
	if s.models == nil {
		return "default/" + string(tier), "default", nil
	}

	models, err := s.models.List(string(tier), "")
	if err != nil {
		return "", "", err
	}
	if len(models) == 0 {
		return "default/" + string(tier), "default", nil
	}

	if excludeFamily != "" {
		for _, model := range models {
			if model.Family != excludeFamily {
				return model.ID, model.Family, nil
			}
		}
		if s.log != nil {
			s.log.Warn("all models at tier are from the excluded family", "tier", tier, "exclude_family", excludeFamily)
		}
	}

	return models[0].ID, models[0].Family, nil
}
