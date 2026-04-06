package state

import (
	"database/sql"
	"fmt"
)

// CreateAttempt inserts a new task attempt and returns its auto-generated ID.
// If Tier is not set, it defaults to TierStandard (matching the DB schema default).
func (d *DB) CreateAttempt(a *TaskAttempt) (int64, error) {
	tier := a.Tier
	if tier == "" {
		tier = TierStandard
	}
	res, err := d.Exec(`INSERT INTO task_attempts
		(task_id, attempt_number, model_id, model_family, tier, base_snapshot, status, phase,
		 input_tokens, output_tokens, cost_usd, failure_reason, feedback)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.TaskID, a.AttemptNumber, a.ModelID, a.ModelFamily, string(tier), a.BaseSnapshot,
		string(a.Status), string(a.Phase),
		a.InputTokens, a.OutputTokens, a.CostUSD, a.FailureReason, a.Feedback)
	if err != nil {
		return 0, fmt.Errorf("creating attempt: %w", err)
	}
	return res.LastInsertId()
}

// GetAttempt retrieves a task attempt by ID.
func (d *DB) GetAttempt(id int64) (*TaskAttempt, error) {
	row := d.QueryRow(`SELECT id, task_id, attempt_number, model_id, model_family, tier,
		base_snapshot, status, phase, input_tokens, output_tokens, cost_usd,
		failure_reason, feedback, started_at, completed_at
		FROM task_attempts WHERE id = ?`, id)
	return scanAttempt(row)
}

// ListAttemptsByTask returns all attempts for a task ordered by attempt number.
func (d *DB) ListAttemptsByTask(taskID string) ([]TaskAttempt, error) {
	rows, err := d.Query(`SELECT id, task_id, attempt_number, model_id, model_family, tier,
		base_snapshot, status, phase, input_tokens, output_tokens, cost_usd,
		failure_reason, feedback, started_at, completed_at
		FROM task_attempts WHERE task_id = ? ORDER BY attempt_number`, taskID)
	if err != nil {
		return nil, fmt.Errorf("listing attempts: %w", err)
	}
	defer rows.Close()

	var attempts []TaskAttempt
	for rows.Next() {
		a, err := scanAttemptRow(rows)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, *a)
	}
	return attempts, rows.Err()
}

// UpdateAttemptStatus transitions an attempt to a new status with invariant checks.
func (d *DB) UpdateAttemptStatus(id int64, to AttemptStatus) error {
	return d.WithTx(func(tx *sql.Tx) error {
		var current string
		err := tx.QueryRow(`SELECT status FROM task_attempts WHERE id = ?`, id).Scan(&current)
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("reading attempt status: %w", err)
		}

		from := AttemptStatus(current)
		if !ValidAttemptTransition(from, to) {
			return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
		}

		var timestampClause string
		switch to {
		case AttemptPassed, AttemptFailed, AttemptEscalated:
			timestampClause = ", completed_at = CURRENT_TIMESTAMP"
		}

		_, err = tx.Exec(`UPDATE task_attempts SET status = ?`+timestampClause+` WHERE id = ?`,
			string(to), id)
		return err
	})
}

// UpdateAttemptPhase transitions an attempt to a new phase with invariant checks.
func (d *DB) UpdateAttemptPhase(id int64, to AttemptPhase) error {
	return d.WithTx(func(tx *sql.Tx) error {
		var current string
		err := tx.QueryRow(`SELECT phase FROM task_attempts WHERE id = ?`, id).Scan(&current)
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("reading attempt phase: %w", err)
		}

		from := AttemptPhase(current)
		if !ValidPhaseTransition(from, to) {
			return fmt.Errorf("%w: phase %s → %s", ErrInvalidTransition, from, to)
		}

		_, err = tx.Exec(`UPDATE task_attempts SET phase = ? WHERE id = ?`, string(to), id)
		return err
	})
}

// --- Validation Runs ---

// CreateValidationRun inserts a validation run and returns its ID.
func (d *DB) CreateValidationRun(vr *ValidationRun) (int64, error) {
	res, err := d.Exec(`INSERT INTO validation_runs
		(attempt_id, check_type, status, output, duration_ms)
		VALUES (?, ?, ?, ?, ?)`,
		vr.AttemptID, string(vr.CheckType), string(vr.Status), vr.Output, vr.DurationMs)
	if err != nil {
		return 0, fmt.Errorf("creating validation run: %w", err)
	}
	return res.LastInsertId()
}

// ListValidationRuns returns all validation runs for an attempt.
func (d *DB) ListValidationRuns(attemptID int64) ([]ValidationRun, error) {
	rows, err := d.Query(`SELECT id, attempt_id, check_type, status, output, duration_ms, timestamp
		FROM validation_runs WHERE attempt_id = ? ORDER BY id`, attemptID)
	if err != nil {
		return nil, fmt.Errorf("listing validation runs: %w", err)
	}
	defer rows.Close()

	var runs []ValidationRun
	for rows.Next() {
		var vr ValidationRun
		var checkType, status string
		var ts string
		if err := rows.Scan(&vr.ID, &vr.AttemptID, &checkType, &status,
			&vr.Output, &vr.DurationMs, &ts); err != nil {
			return nil, err
		}
		vr.CheckType = ValidationCheckType(checkType)
		vr.Status = ValidationStatus(status)
		vr.Timestamp = parseTime(ts)
		runs = append(runs, vr)
	}
	return runs, rows.Err()
}

// --- Review Runs ---

// CreateReviewRun inserts a review run and returns its ID.
func (d *DB) CreateReviewRun(rr *ReviewRun) (int64, error) {
	res, err := d.Exec(`INSERT INTO review_runs
		(attempt_id, reviewer_model, reviewer_family, verdict, feedback, cost_usd)
		VALUES (?, ?, ?, ?, ?, ?)`,
		rr.AttemptID, rr.ReviewerModel, rr.ReviewerFamily, string(rr.Verdict), rr.Feedback, rr.CostUSD)
	if err != nil {
		return 0, fmt.Errorf("creating review run: %w", err)
	}
	return res.LastInsertId()
}

// ListReviewRuns returns all review runs for an attempt.
func (d *DB) ListReviewRuns(attemptID int64) ([]ReviewRun, error) {
	rows, err := d.Query(`SELECT id, attempt_id, reviewer_model, reviewer_family, verdict,
		feedback, cost_usd, timestamp
		FROM review_runs WHERE attempt_id = ? ORDER BY id`, attemptID)
	if err != nil {
		return nil, fmt.Errorf("listing review runs: %w", err)
	}
	defer rows.Close()

	var runs []ReviewRun
	for rows.Next() {
		var rr ReviewRun
		var verdict, ts string
		if err := rows.Scan(&rr.ID, &rr.AttemptID, &rr.ReviewerModel, &rr.ReviewerFamily,
			&verdict, &rr.Feedback, &rr.CostUSD, &ts); err != nil {
			return nil, err
		}
		rr.Verdict = ReviewVerdict(verdict)
		rr.Timestamp = parseTime(ts)
		runs = append(runs, rr)
	}
	return runs, rows.Err()
}

// --- Artifacts ---

// CreateArtifact inserts an artifact record and returns its ID.
func (d *DB) CreateArtifact(a *TaskArtifact) (int64, error) {
	res, err := d.Exec(`INSERT INTO task_artifacts
		(attempt_id, operation, path_from, path_to, sha256_before, sha256_after, size_before, size_after)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.AttemptID, string(a.Operation), a.PathFrom, a.PathTo,
		a.SHA256Before, a.SHA256After, a.SizeBefore, a.SizeAfter)
	if err != nil {
		return 0, fmt.Errorf("creating artifact: %w", err)
	}
	return res.LastInsertId()
}

// ListArtifacts returns all artifacts for an attempt.
func (d *DB) ListArtifacts(attemptID int64) ([]TaskArtifact, error) {
	rows, err := d.Query(`SELECT id, attempt_id, operation, path_from, path_to,
		sha256_before, sha256_after, size_before, size_after, timestamp
		FROM task_artifacts WHERE attempt_id = ? ORDER BY id`, attemptID)
	if err != nil {
		return nil, fmt.Errorf("listing artifacts: %w", err)
	}
	defer rows.Close()

	var arts []TaskArtifact
	for rows.Next() {
		var a TaskArtifact
		var op, ts string
		if err := rows.Scan(&a.ID, &a.AttemptID, &op, &a.PathFrom, &a.PathTo,
			&a.SHA256Before, &a.SHA256After, &a.SizeBefore, &a.SizeAfter, &ts); err != nil {
			return nil, err
		}
		a.Operation = ArtifactOp(op)
		a.Timestamp = parseTime(ts)
		arts = append(arts, a)
	}
	return arts, rows.Err()
}

// --- scan helpers ---

func scanAttempt(row *sql.Row) (*TaskAttempt, error) {
	var a TaskAttempt
	var status, phase, tier string
	var startedAt string
	var completedAt *string

	err := row.Scan(&a.ID, &a.TaskID, &a.AttemptNumber, &a.ModelID, &a.ModelFamily, &tier,
		&a.BaseSnapshot, &status, &phase, &a.InputTokens, &a.OutputTokens, &a.CostUSD,
		&a.FailureReason, &a.Feedback, &startedAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning attempt: %w", err)
	}

	a.Tier = TaskTier(tier)
	a.Status = AttemptStatus(status)
	a.Phase = AttemptPhase(phase)
	a.StartedAt = parseTime(startedAt)
	a.CompletedAt = parseNullTime(completedAt)
	return &a, nil
}

func scanAttemptRow(rows *sql.Rows) (*TaskAttempt, error) {
	var a TaskAttempt
	var status, phase, tier string
	var startedAt string
	var completedAt *string

	err := rows.Scan(&a.ID, &a.TaskID, &a.AttemptNumber, &a.ModelID, &a.ModelFamily, &tier,
		&a.BaseSnapshot, &status, &phase, &a.InputTokens, &a.OutputTokens, &a.CostUSD,
		&a.FailureReason, &a.Feedback, &startedAt, &completedAt)
	if err != nil {
		return nil, fmt.Errorf("scanning attempt row: %w", err)
	}

	a.Tier = TaskTier(tier)
	a.Status = AttemptStatus(status)
	a.Phase = AttemptPhase(phase)
	a.StartedAt = parseTime(startedAt)
	a.CompletedAt = parseNullTime(completedAt)
	return &a, nil
}
