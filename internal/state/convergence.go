package state

import (
	"database/sql"
	"fmt"
)

// CreateConvergencePair inserts a new convergence pair and returns its ID.
// Per Architecture Section 11.5: tracks the impl→test→fix convergence lifecycle.
func (d *DB) CreateConvergencePair(cp *ConvergencePair) (int64, error) {
	status := cp.Status
	if status == "" {
		status = ConvergencePending
	}
	iteration := cp.Iteration
	if iteration == 0 {
		iteration = 1
	}
	res, err := d.Exec(`INSERT INTO convergence_pairs
		(impl_task_id, test_task_id, fix_task_id, status, impl_model_family, iteration)
		VALUES (?, ?, ?, ?, ?, ?)`,
		cp.ImplTaskID, cp.TestTaskID, cp.FixTaskID, string(status), cp.ImplModelFamily, iteration)
	if err != nil {
		return 0, fmt.Errorf("creating convergence pair: %w", err)
	}
	return res.LastInsertId()
}

// GetConvergencePair retrieves a convergence pair by ID.
func (d *DB) GetConvergencePair(id int64) (*ConvergencePair, error) {
	row := d.QueryRow(`SELECT id, impl_task_id, test_task_id, fix_task_id, status,
		impl_model_family, iteration, created_at, converged_at
		FROM convergence_pairs WHERE id = ?`, id)
	return scanConvergencePair(row)
}

// GetConvergencePairByImplTask retrieves the convergence pair for an implementation task.
func (d *DB) GetConvergencePairByImplTask(implTaskID string) (*ConvergencePair, error) {
	row := d.QueryRow(`SELECT id, impl_task_id, test_task_id, fix_task_id, status,
		impl_model_family, iteration, created_at, converged_at
		FROM convergence_pairs WHERE impl_task_id = ?`, implTaskID)
	return scanConvergencePair(row)
}

// GetConvergencePairByTestTask retrieves the convergence pair for a test task.
func (d *DB) GetConvergencePairByTestTask(testTaskID string) (*ConvergencePair, error) {
	row := d.QueryRow(`SELECT id, impl_task_id, test_task_id, fix_task_id, status,
		impl_model_family, iteration, created_at, converged_at
		FROM convergence_pairs WHERE test_task_id = ?`, testTaskID)
	return scanConvergencePair(row)
}

// UpdateConvergencePairStatus transitions a convergence pair to a new status.
func (d *DB) UpdateConvergencePairStatus(id int64, status ConvergenceStatus) error {
	var timestampClause string
	if status == ConvergenceConverged {
		timestampClause = ", converged_at = CURRENT_TIMESTAMP"
	}
	_, err := d.Exec(
		`UPDATE convergence_pairs SET status = ?`+timestampClause+` WHERE id = ?`,
		string(status), id)
	if err != nil {
		return fmt.Errorf("updating convergence pair status: %w", err)
	}
	return nil
}

// SetConvergenceTestTask sets the test task ID on a convergence pair.
func (d *DB) SetConvergenceTestTask(id int64, testTaskID string) error {
	_, err := d.Exec(`UPDATE convergence_pairs SET test_task_id = ? WHERE id = ?`,
		testTaskID, id)
	if err != nil {
		return fmt.Errorf("setting convergence test task: %w", err)
	}
	return nil
}

// SetConvergenceFixTask sets the fix task ID on a convergence pair.
func (d *DB) SetConvergenceFixTask(id int64, fixTaskID string) error {
	_, err := d.Exec(`UPDATE convergence_pairs SET fix_task_id = ? WHERE id = ?`,
		fixTaskID, id)
	if err != nil {
		return fmt.Errorf("setting convergence fix task: %w", err)
	}
	return nil
}

// IncrementConvergenceIteration bumps the iteration count for fix loops.
func (d *DB) IncrementConvergenceIteration(id int64) error {
	_, err := d.Exec(`UPDATE convergence_pairs SET iteration = iteration + 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("incrementing convergence iteration: %w", err)
	}
	return nil
}

// ListConvergencePairsByRun returns all convergence pairs for tasks in a given run.
func (d *DB) ListConvergencePairsByRun(runID string) ([]ConvergencePair, error) {
	rows, err := d.Query(`SELECT cp.id, cp.impl_task_id, cp.test_task_id, cp.fix_task_id,
		cp.status, cp.impl_model_family, cp.iteration, cp.created_at, cp.converged_at
		FROM convergence_pairs cp
		JOIN tasks t ON t.id = cp.impl_task_id
		WHERE t.run_id = ?
		ORDER BY cp.id`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing convergence pairs: %w", err)
	}
	defer rows.Close()

	var pairs []ConvergencePair
	for rows.Next() {
		var cp ConvergencePair
		var status string
		var testTaskID, fixTaskID *string
		var createdAt string
		var convergedAt *string

		if err := rows.Scan(&cp.ID, &cp.ImplTaskID, &testTaskID, &fixTaskID,
			&status, &cp.ImplModelFamily, &cp.Iteration, &createdAt, &convergedAt); err != nil {
			return nil, fmt.Errorf("scanning convergence pair: %w", err)
		}
		cp.TestTaskID = testTaskID
		cp.FixTaskID = fixTaskID
		cp.Status = ConvergenceStatus(status)
		cp.CreatedAt = parseTime(createdAt)
		cp.ConvergedAt = parseNullTime(convergedAt)
		pairs = append(pairs, cp)
	}
	return pairs, rows.Err()
}

// --- scan helpers ---

func scanConvergencePair(row *sql.Row) (*ConvergencePair, error) {
	var cp ConvergencePair
	var status string
	var testTaskID, fixTaskID *string
	var createdAt string
	var convergedAt *string

	err := row.Scan(&cp.ID, &cp.ImplTaskID, &testTaskID, &fixTaskID,
		&status, &cp.ImplModelFamily, &cp.Iteration, &createdAt, &convergedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning convergence pair: %w", err)
	}

	cp.TestTaskID = testTaskID
	cp.FixTaskID = fixTaskID
	cp.Status = ConvergenceStatus(status)
	cp.CreatedAt = parseTime(createdAt)
	cp.ConvergedAt = parseNullTime(convergedAt)
	return &cp, nil
}
