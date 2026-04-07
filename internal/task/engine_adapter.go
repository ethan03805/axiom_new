package task

import (
	"context"

	"github.com/openaxiom/axiom/internal/engine"
)

// EngineAdapter bridges task.Service to engine.TaskService.
type EngineAdapter struct {
	svc *Service
}

// NewEngineAdapter wraps a task service for engine use.
func NewEngineAdapter(svc *Service) *EngineAdapter {
	return &EngineAdapter{svc: svc}
}

// HandleTaskFailure converts task failure actions to engine-level actions.
func (a *EngineAdapter) HandleTaskFailure(ctx context.Context, taskID string, feedback string) (engine.TaskFailureAction, error) {
	action, err := a.svc.HandleTaskFailure(ctx, taskID, feedback)
	if err != nil {
		return "", err
	}
	switch action {
	case ActionRetry:
		return engine.TaskFailureRetry, nil
	case ActionEscalate:
		return engine.TaskFailureEscalate, nil
	default:
		return engine.TaskFailureBlock, nil
	}
}

// RequestScopeExpansion converts engine target file specs to task target file inputs.
func (a *EngineAdapter) RequestScopeExpansion(ctx context.Context, taskID string, additionalFiles []engine.TargetFileSpec) error {
	files := make([]TargetFileInput, 0, len(additionalFiles))
	for _, file := range additionalFiles {
		files = append(files, TargetFileInput{
			FilePath:        file.FilePath,
			LockScope:       file.LockScope,
			LockResourceKey: file.LockResourceKey,
		})
	}
	return a.svc.RequestScopeExpansion(ctx, taskID, files)
}
