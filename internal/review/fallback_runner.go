package review

import "context"

// FallbackRunner rejects work when no reviewer runtime is configured.
type FallbackRunner struct{}

// Run returns a structured rejection so the executor can retry/escalate safely.
func (FallbackRunner) Run(_ context.Context, _ string) (string, error) {
	return `### Verdict: REJECT

### Feedback (if REJECT)
Reviewer runner is not configured.`, nil
}
