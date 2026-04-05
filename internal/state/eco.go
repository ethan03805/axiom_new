package state

import (
	"database/sql"
	"fmt"
)

// CreateECO inserts an ECO log entry and returns its ID.
func (d *DB) CreateECO(e *ECOLogEntry) (int64, error) {
	res, err := d.Exec(`INSERT INTO eco_log
		(run_id, eco_code, category, description, affected_refs, proposed_change, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.RunID, e.ECOCode, e.Category, e.Description,
		e.AffectedRefs, e.ProposedChange, string(e.Status))
	if err != nil {
		return 0, fmt.Errorf("creating ECO: %w", err)
	}
	return res.LastInsertId()
}

// GetECO retrieves an ECO log entry by ID.
func (d *DB) GetECO(id int64) (*ECOLogEntry, error) {
	var e ECOLogEntry
	var status, createdAt string
	var resolvedAt *string

	err := d.QueryRow(`SELECT id, run_id, eco_code, category, description,
		affected_refs, proposed_change, status, approved_by, created_at, resolved_at
		FROM eco_log WHERE id = ?`, id).Scan(
		&e.ID, &e.RunID, &e.ECOCode, &e.Category, &e.Description,
		&e.AffectedRefs, &e.ProposedChange, &status, &e.ApprovedBy, &createdAt, &resolvedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting ECO: %w", err)
	}
	e.Status = ECOStatus(status)
	e.CreatedAt = parseTime(createdAt)
	e.ResolvedAt = parseNullTime(resolvedAt)
	return &e, nil
}

// ListECOsByRun returns all ECO entries for a run.
func (d *DB) ListECOsByRun(runID string) ([]ECOLogEntry, error) {
	rows, err := d.Query(`SELECT id, run_id, eco_code, category, description,
		affected_refs, proposed_change, status, approved_by, created_at, resolved_at
		FROM eco_log WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing ECOs: %w", err)
	}
	defer rows.Close()

	var ecos []ECOLogEntry
	for rows.Next() {
		var e ECOLogEntry
		var status, createdAt string
		var resolvedAt *string
		if err := rows.Scan(&e.ID, &e.RunID, &e.ECOCode, &e.Category, &e.Description,
			&e.AffectedRefs, &e.ProposedChange, &status, &e.ApprovedBy,
			&createdAt, &resolvedAt); err != nil {
			return nil, fmt.Errorf("scanning ECO: %w", err)
		}
		e.Status = ECOStatus(status)
		e.CreatedAt = parseTime(createdAt)
		e.ResolvedAt = parseNullTime(resolvedAt)
		ecos = append(ecos, e)
	}
	return ecos, rows.Err()
}

// UpdateECOStatus transitions an ECO to a new status with invariant checks.
// Sets resolved_at and approved_by on terminal transitions.
func (d *DB) UpdateECOStatus(id int64, to ECOStatus, approvedBy *string) error {
	return d.WithTx(func(tx *sql.Tx) error {
		var current string
		err := tx.QueryRow(`SELECT status FROM eco_log WHERE id = ?`, id).Scan(&current)
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("reading ECO status: %w", err)
		}

		from := ECOStatus(current)
		if !ValidECOTransition(from, to) {
			return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
		}

		_, err = tx.Exec(`UPDATE eco_log SET status = ?, approved_by = ?, resolved_at = CURRENT_TIMESTAMP WHERE id = ?`,
			string(to), approvedBy, id)
		return err
	})
}
