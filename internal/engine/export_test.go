package engine

import (
	"context"

	"github.com/openaxiom/axiom/internal/config"
)

// RunMergeQueueIntegrationChecksForTest exercises the internal
// mergeQueueValidatorAdapter against the provided ValidationService. It
// exists only in test builds so external integration tests (package
// engine_test) can drive the adapter end-to-end without exporting its
// concrete type to production callers.
//
// Per Architecture Section 23.3, this is the exact code path the merge
// queue uses when deciding whether to commit: when the underlying
// validation service reports a failure, RunIntegrationChecks must return
// (false, feedback, nil) so the merge queue requeues instead of committing.
func RunMergeQueueIntegrationChecksForTest(ctx context.Context, v ValidationService, cfg *config.Config, projectDir string) (bool, string, error) {
	adapter := &mergeQueueValidatorAdapter{validation: v, cfg: cfg}
	return adapter.RunIntegrationChecks(ctx, projectDir)
}
