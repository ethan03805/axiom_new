package validation

import (
	"context"

	"github.com/openaxiom/axiom/internal/engine"
)

// EngineAdapter bridges validation.Service to engine.ValidationService.
type EngineAdapter struct {
	svc *Service
}

// NewEngineAdapter wraps a validation service for engine use.
func NewEngineAdapter(svc *Service) *EngineAdapter {
	return &EngineAdapter{svc: svc}
}

// RunChecks converts engine request/response types to validation-native types.
func (a *EngineAdapter) RunChecks(ctx context.Context, req engine.ValidationCheckRequest) ([]engine.ValidationCheckResult, error) {
	results, err := a.svc.RunChecks(ctx, CheckRequest{
		TaskID:     req.TaskID,
		RunID:      req.RunID,
		Image:      req.Image,
		StagingDir: req.StagingDir,
		ProjectDir: req.ProjectDir,
		Config:     req.Config,
		Languages:  req.Languages,
	})
	if err != nil {
		return nil, err
	}

	adapted := make([]engine.ValidationCheckResult, 0, len(results))
	for _, result := range results {
		adapted = append(adapted, engine.ValidationCheckResult{
			CheckType:  result.CheckType,
			Status:     result.Status,
			Output:     result.Output,
			DurationMs: result.DurationMs,
		})
	}
	return adapted, nil
}
