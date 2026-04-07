package review

import (
	"context"

	"github.com/openaxiom/axiom/internal/engine"
)

// EngineAdapter bridges review.Service to engine.ReviewService.
type EngineAdapter struct {
	svc *Service
}

// NewEngineAdapter wraps a review service for engine use.
func NewEngineAdapter(svc *Service) *EngineAdapter {
	return &EngineAdapter{svc: svc}
}

// RunReview converts engine request/response types to review-native types.
func (a *EngineAdapter) RunReview(ctx context.Context, req engine.ReviewRunRequest) (*engine.ReviewRunResult, error) {
	result, err := a.svc.RunReview(ctx, ReviewRequest{
		TaskID:         req.TaskID,
		RunID:          req.RunID,
		Image:          req.Image,
		SpecDir:        req.SpecDir,
		TaskTier:       req.TaskTier,
		MeeseeksFamily: req.MeeseeksFamily,
		CPULimit:       req.CPULimit,
		MemLimit:       req.MemLimit,
		AffectedFiles:  req.AffectedFiles,
	})
	if err != nil {
		return nil, err
	}
	return &engine.ReviewRunResult{
		Verdict:        result.Verdict,
		Feedback:       result.Feedback,
		ReviewerModel:  result.ReviewerModel,
		ReviewerFamily: result.ReviewerFamily,
		ReviewerTier:   result.ReviewerTier,
	}, nil
}
