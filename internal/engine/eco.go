package engine

import (
	"fmt"

	"github.com/openaxiom/axiom/internal/eco"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

// ECOProposal holds the data for proposing an Engineering Change Order.
// Per Architecture Section 7.3.
type ECOProposal struct {
	RunID          string
	Category       string
	AffectedRefs   string
	Description    string
	ProposedChange string
}

// ProposeECO creates a new ECO proposal for the given run.
// The ECO category must be valid per Architecture Section 7.2.
// The run must be in active or paused status. Returns the ECO ID.
func (e *Engine) ProposeECO(proposal ECOProposal) (int64, error) {
	// Validate proposal
	if err := eco.ValidateProposal(eco.Proposal{
		Category:       proposal.Category,
		AffectedRefs:   proposal.AffectedRefs,
		Description:    proposal.Description,
		ProposedChange: proposal.ProposedChange,
	}); err != nil {
		return 0, fmt.Errorf("invalid ECO proposal: %w", err)
	}

	// Verify run is active or paused (ECOs only during execution)
	run, err := e.db.GetRun(proposal.RunID)
	if err != nil {
		return 0, fmt.Errorf("getting run: %w", err)
	}
	if run.Status != state.RunActive && run.Status != state.RunPaused {
		return 0, fmt.Errorf("cannot propose ECO: run is in %q status (must be active or paused)", run.Status)
	}

	// Generate ECO code
	existing, err := e.db.ListECOsByRun(proposal.RunID)
	if err != nil {
		return 0, fmt.Errorf("listing existing ECOs: %w", err)
	}
	ecoCode := fmt.Sprintf("ECO-%03d", len(existing)+1)

	// Create ECO in DB
	entry := &state.ECOLogEntry{
		RunID:          proposal.RunID,
		ECOCode:        ecoCode,
		Category:       proposal.Category,
		Description:    proposal.Description,
		AffectedRefs:   proposal.AffectedRefs,
		ProposedChange: proposal.ProposedChange,
		Status:         state.ECOProposed,
	}

	ecoID, err := e.db.CreateECO(entry)
	if err != nil {
		return 0, fmt.Errorf("creating ECO: %w", err)
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.ECOProposed,
		RunID: proposal.RunID,
		Details: map[string]any{
			"eco_id":   ecoID,
			"eco_code": ecoCode,
			"category": proposal.Category,
		},
	})

	e.log.Info("ECO proposed",
		"run_id", proposal.RunID,
		"eco_id", ecoID,
		"eco_code", ecoCode,
		"category", proposal.Category,
	)

	return ecoID, nil
}

// ApproveECO approves an ECO proposal, writes the ECO file to .axiom/eco/,
// and transitions the ECO status. Per Architecture Section 7.3 step 5.
func (e *Engine) ApproveECO(ecoID int64, approvedBy string) error {
	entry, err := e.db.GetECO(ecoID)
	if err != nil {
		return fmt.Errorf("getting ECO: %w", err)
	}

	// Update status in DB
	if err := e.db.UpdateECOStatus(ecoID, state.ECOApproved, &approvedBy); err != nil {
		return fmt.Errorf("approving ECO: %w", err)
	}

	// Write ECO file to .axiom/eco/
	record := eco.Record{
		ECOCode:        entry.ECOCode,
		Category:       entry.Category,
		Status:         "Approved",
		AffectedRefs:   entry.AffectedRefs,
		Description:    entry.Description,
		ProposedChange: entry.ProposedChange,
	}
	if err := eco.WriteECOFile(e.rootDir, record); err != nil {
		return fmt.Errorf("writing ECO file: %w", err)
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.ECOResolved,
		RunID: entry.RunID,
		Details: map[string]any{
			"eco_id":      ecoID,
			"eco_code":    entry.ECOCode,
			"resolution":  "approved",
			"approved_by": approvedBy,
		},
	})

	e.log.Info("ECO approved",
		"eco_id", ecoID,
		"eco_code", entry.ECOCode,
		"approved_by", approvedBy,
	)

	return nil
}

// RejectECO rejects an ECO proposal. Per Architecture Section 7.3 step 6:
// the orchestrator must find an alternative within the original SRS.
func (e *Engine) RejectECO(ecoID int64) error {
	entry, err := e.db.GetECO(ecoID)
	if err != nil {
		return fmt.Errorf("getting ECO: %w", err)
	}

	if err := e.db.UpdateECOStatus(ecoID, state.ECORejected, nil); err != nil {
		return fmt.Errorf("rejecting ECO: %w", err)
	}

	e.emitEvent(events.EngineEvent{
		Type:  events.ECOResolved,
		RunID: entry.RunID,
		Details: map[string]any{
			"eco_id":     ecoID,
			"eco_code":   entry.ECOCode,
			"resolution": "rejected",
		},
	})

	e.log.Info("ECO rejected", "eco_id", ecoID, "eco_code", entry.ECOCode)
	return nil
}
