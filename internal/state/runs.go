package state

import (
	"database/sql"
	"fmt"
)

// CreateRun inserts a new project run.
func (d *DB) CreateRun(r *ProjectRun) error {
	_, err := d.Exec(`INSERT INTO project_runs
		(id, project_id, status, base_branch, work_branch,
		 orchestrator_mode, orchestrator_runtime, orchestrator_identity,
		 srs_approval_delegate, budget_max_usd, config_snapshot, srs_hash,
		 initial_prompt, start_source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ProjectID, string(r.Status), r.BaseBranch, r.WorkBranch,
		r.OrchestratorMode, r.OrchestratorRuntime, r.OrchestratorIdentity,
		r.SRSApprovalDelegate, r.BudgetMaxUSD, r.ConfigSnapshot, r.SRSHash,
		r.InitialPrompt, r.StartSource)
	if err != nil {
		return fmt.Errorf("creating run: %w", err)
	}
	return nil
}

// GetRun retrieves a project run by ID.
func (d *DB) GetRun(id string) (*ProjectRun, error) {
	row := d.QueryRow(`SELECT id, project_id, status, base_branch, work_branch,
		orchestrator_mode, orchestrator_runtime, orchestrator_identity,
		srs_approval_delegate, budget_max_usd, config_snapshot, srs_hash,
		initial_prompt, start_source,
		started_at, paused_at, cancelled_at, completed_at
		FROM project_runs WHERE id = ?`, id)
	return scanRun(row)
}

// GetActiveRun returns the currently active run for a project.
// Active means status is one of: draft_srs, awaiting_srs_approval, active, paused.
func (d *DB) GetActiveRun(projectID string) (*ProjectRun, error) {
	row := d.QueryRow(`SELECT id, project_id, status, base_branch, work_branch,
		orchestrator_mode, orchestrator_runtime, orchestrator_identity,
		srs_approval_delegate, budget_max_usd, config_snapshot, srs_hash,
		initial_prompt, start_source,
		started_at, paused_at, cancelled_at, completed_at
		FROM project_runs
		WHERE project_id = ? AND status IN ('draft_srs','awaiting_srs_approval','active','paused')
		ORDER BY started_at DESC LIMIT 1`, projectID)
	return scanRun(row)
}

// GetLatestRunByProject returns the most recent run for a project regardless of status.
func (d *DB) GetLatestRunByProject(projectID string) (*ProjectRun, error) {
	row := d.QueryRow(`SELECT id, project_id, status, base_branch, work_branch,
		orchestrator_mode, orchestrator_runtime, orchestrator_identity,
		srs_approval_delegate, budget_max_usd, config_snapshot, srs_hash,
		initial_prompt, start_source,
		started_at, paused_at, cancelled_at, completed_at
		FROM project_runs
		WHERE project_id = ?
		ORDER BY started_at DESC LIMIT 1`, projectID)
	return scanRun(row)
}

// ListRunsByProject returns all runs for a project ordered by start time.
func (d *DB) ListRunsByProject(projectID string) ([]ProjectRun, error) {
	rows, err := d.Query(`SELECT id, project_id, status, base_branch, work_branch,
		orchestrator_mode, orchestrator_runtime, orchestrator_identity,
		srs_approval_delegate, budget_max_usd, config_snapshot, srs_hash,
		initial_prompt, start_source,
		started_at, paused_at, cancelled_at, completed_at
		FROM project_runs WHERE project_id = ? ORDER BY started_at`, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}
	defer rows.Close()

	var runs []ProjectRun
	for rows.Next() {
		r, err := scanRunRow(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, *r)
	}
	return runs, rows.Err()
}

// UpdateRunStatus transitions a run to a new status with invariant checks.
// Sets appropriate timestamps (paused_at, cancelled_at, completed_at).
func (d *DB) UpdateRunStatus(id string, to RunStatus) error {
	return d.WithTx(func(tx *sql.Tx) error {
		var currentStatus string
		err := tx.QueryRow(`SELECT status FROM project_runs WHERE id = ?`, id).Scan(&currentStatus)
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("reading run status: %w", err)
		}

		from := RunStatus(currentStatus)
		if !ValidRunTransition(from, to) {
			return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
		}

		// Build timestamp updates based on target status
		var timestampClause string
		switch to {
		case RunPaused:
			timestampClause = ", paused_at = CURRENT_TIMESTAMP"
		case RunCancelled:
			timestampClause = ", cancelled_at = CURRENT_TIMESTAMP"
		case RunCompleted:
			timestampClause = ", completed_at = CURRENT_TIMESTAMP"
		}

		_, err = tx.Exec(
			`UPDATE project_runs SET status = ?`+timestampClause+` WHERE id = ?`,
			string(to), id)
		if err != nil {
			return fmt.Errorf("updating run status: %w", err)
		}
		return nil
	})
}

// UpdateRunHandoff persists the initial prompt, start source, and orchestrator mode
// on a run record. Used by engine.StartRun after the low-level CreateRun.
func (d *DB) UpdateRunHandoff(id, prompt, source, orchMode string) error {
	res, err := d.Exec(
		`UPDATE project_runs SET initial_prompt = ?, start_source = ?, orchestrator_mode = ? WHERE id = ?`,
		prompt, source, orchMode, id)
	if err != nil {
		return fmt.Errorf("updating run handoff: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateRunSRSHash stores the SRS SHA-256 hash on a run record.
func (d *DB) UpdateRunSRSHash(id string, hash string) error {
	res, err := d.Exec(`UPDATE project_runs SET srs_hash = ? WHERE id = ?`, hash, id)
	if err != nil {
		return fmt.Errorf("updating SRS hash: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// scanRun scans a single row from *sql.Row into a ProjectRun.
func scanRun(row *sql.Row) (*ProjectRun, error) {
	var r ProjectRun
	var status string
	var orchIdentity, srsHash, startedAt, pausedAt, cancelledAt, completedAt *string

	err := row.Scan(
		&r.ID, &r.ProjectID, &status, &r.BaseBranch, &r.WorkBranch,
		&r.OrchestratorMode, &r.OrchestratorRuntime, &orchIdentity,
		&r.SRSApprovalDelegate, &r.BudgetMaxUSD, &r.ConfigSnapshot, &srsHash,
		&r.InitialPrompt, &r.StartSource,
		&startedAt, &pausedAt, &cancelledAt, &completedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning run: %w", err)
	}

	r.Status = RunStatus(status)
	r.OrchestratorIdentity = orchIdentity
	r.SRSHash = srsHash
	if startedAt != nil {
		r.StartedAt = parseTime(*startedAt)
	}
	r.PausedAt = parseNullTime(pausedAt)
	r.CancelledAt = parseNullTime(cancelledAt)
	r.CompletedAt = parseNullTime(completedAt)
	return &r, nil
}

// scanRunRow scans a row from *sql.Rows (multi-row query) into a ProjectRun.
func scanRunRow(rows *sql.Rows) (*ProjectRun, error) {
	var r ProjectRun
	var status string
	var orchIdentity, srsHash, startedAt, pausedAt, cancelledAt, completedAt *string

	err := rows.Scan(
		&r.ID, &r.ProjectID, &status, &r.BaseBranch, &r.WorkBranch,
		&r.OrchestratorMode, &r.OrchestratorRuntime, &orchIdentity,
		&r.SRSApprovalDelegate, &r.BudgetMaxUSD, &r.ConfigSnapshot, &srsHash,
		&r.InitialPrompt, &r.StartSource,
		&startedAt, &pausedAt, &cancelledAt, &completedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning run row: %w", err)
	}

	r.Status = RunStatus(status)
	r.OrchestratorIdentity = orchIdentity
	r.SRSHash = srsHash
	if startedAt != nil {
		r.StartedAt = parseTime(*startedAt)
	}
	r.PausedAt = parseNullTime(pausedAt)
	r.CancelledAt = parseNullTime(cancelledAt)
	r.CompletedAt = parseNullTime(completedAt)
	return &r, nil
}
