package engine

import (
	"fmt"

	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/srs"
	"github.com/openaxiom/axiom/internal/state"
)

// SubmitSRS validates an SRS draft and transitions the run to awaiting_srs_approval.
// The SRS content is validated for required structure per Architecture Section 6.1,
// then persisted as a draft. The run must be in draft_srs status.
func (e *Engine) SubmitSRS(runID string, content string) error {
	// Validate SRS structure
	if err := srs.ValidateStructure(content); err != nil {
		return fmt.Errorf("invalid SRS structure: %w", err)
	}

	// Verify run is in draft_srs status
	run, err := e.db.GetRun(runID)
	if err != nil {
		return fmt.Errorf("getting run: %w", err)
	}
	if run.Status != state.RunDraftSRS {
		return fmt.Errorf("cannot submit SRS: run is in %q status (must be %q)", run.Status, state.RunDraftSRS)
	}

	// Persist draft
	if err := srs.WriteDraft(e.rootDir, runID, content); err != nil {
		return fmt.Errorf("persisting SRS draft: %w", err)
	}

	// Transition to awaiting_srs_approval
	if err := e.db.UpdateRunStatus(runID, state.RunAwaitingSRSApproval); err != nil {
		return fmt.Errorf("transitioning run: %w", err)
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.SRSSubmitted,
		RunID: runID,
	})

	e.log.Info("SRS submitted for approval", "run_id", runID)
	return nil
}

// ApproveSRS finalizes an approved SRS: writes the immutable SRS file,
// computes and stores the SHA-256 hash, and transitions the run to active.
// The run must be in awaiting_srs_approval status.
// Per Architecture Section 6.2: SRS is written read-only with hash verification.
func (e *Engine) ApproveSRS(runID string) error {
	// Verify run is in awaiting_srs_approval status
	run, err := e.db.GetRun(runID)
	if err != nil {
		return fmt.Errorf("getting run: %w", err)
	}
	if run.Status != state.RunAwaitingSRSApproval {
		return fmt.Errorf("cannot approve SRS: run is in %q status (must be %q)", run.Status, state.RunAwaitingSRSApproval)
	}

	// Read the draft
	content, err := srs.ReadDraft(e.rootDir, runID)
	if err != nil {
		return fmt.Errorf("reading SRS draft: %w", err)
	}

	// Write immutable SRS file (read-only permissions) and hash file
	if err := project.WriteSRS(e.rootDir, []byte(content)); err != nil {
		return fmt.Errorf("writing SRS: %w", err)
	}

	// Compute and store hash in DB
	hash := srs.ComputeHash([]byte(content))
	if err := e.db.UpdateRunSRSHash(runID, hash); err != nil {
		return fmt.Errorf("storing SRS hash: %w", err)
	}

	// Transition to active
	if err := e.db.UpdateRunStatus(runID, state.RunActive); err != nil {
		return fmt.Errorf("transitioning run: %w", err)
	}

	// Clean up draft
	_ = srs.DeleteDraft(e.rootDir, runID)

	e.emitEvent(events.EngineEvent{
		Type:  events.SRSApproved,
		RunID: runID,
		Details: map[string]any{
			"srs_hash": hash,
		},
	})

	e.log.Info("SRS approved", "run_id", runID, "hash", hash)
	return nil
}

// RejectSRS rejects an SRS draft with feedback and transitions the run
// back to draft_srs so the orchestrator can revise and resubmit.
// Per Architecture Section 6: on rejection, persist feedback and reopen the revision loop.
func (e *Engine) RejectSRS(runID string, feedback string) error {
	// Verify run is in awaiting_srs_approval status
	run, err := e.db.GetRun(runID)
	if err != nil {
		return fmt.Errorf("getting run: %w", err)
	}
	if run.Status != state.RunAwaitingSRSApproval {
		return fmt.Errorf("cannot reject SRS: run is in %q status (must be %q)", run.Status, state.RunAwaitingSRSApproval)
	}

	// Transition back to draft_srs
	if err := e.db.UpdateRunStatus(runID, state.RunDraftSRS); err != nil {
		return fmt.Errorf("transitioning run: %w", err)
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.SRSRejected,
		RunID: runID,
		Details: map[string]any{
			"feedback": feedback,
		},
	})

	e.log.Info("SRS rejected", "run_id", runID, "feedback", feedback)
	return nil
}
