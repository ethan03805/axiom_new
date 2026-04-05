package state

import (
	"database/sql"
	"fmt"
)

// CreateContainerSession inserts a container session record.
func (d *DB) CreateContainerSession(cs *ContainerSession) error {
	_, err := d.Exec(`INSERT INTO container_sessions
		(id, run_id, task_id, container_type, image, model_id, cpu_limit, mem_limit)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		cs.ID, cs.RunID, cs.TaskID, string(cs.ContainerType), cs.Image,
		cs.ModelID, cs.CPULimit, cs.MemLimit)
	if err != nil {
		return fmt.Errorf("creating container session: %w", err)
	}
	return nil
}

// GetContainerSession retrieves a container session by ID.
func (d *DB) GetContainerSession(id string) (*ContainerSession, error) {
	var cs ContainerSession
	var containerType, startedAt string
	var stoppedAt *string

	err := d.QueryRow(`SELECT id, run_id, task_id, container_type, image,
		model_id, cpu_limit, mem_limit, started_at, stopped_at, exit_reason
		FROM container_sessions WHERE id = ?`, id).Scan(
		&cs.ID, &cs.RunID, &cs.TaskID, &containerType, &cs.Image,
		&cs.ModelID, &cs.CPULimit, &cs.MemLimit, &startedAt, &stoppedAt, &cs.ExitReason)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting container session: %w", err)
	}
	cs.ContainerType = ContainerType(containerType)
	cs.StartedAt = parseTime(startedAt)
	cs.StoppedAt = parseNullTime(stoppedAt)
	return &cs, nil
}

// ListActiveContainers returns all running containers for a run (stopped_at IS NULL).
func (d *DB) ListActiveContainers(runID string) ([]ContainerSession, error) {
	return d.listContainers(`SELECT id, run_id, task_id, container_type, image,
		model_id, cpu_limit, mem_limit, started_at, stopped_at, exit_reason
		FROM container_sessions WHERE run_id = ? AND stopped_at IS NULL ORDER BY started_at`, runID)
}

// ListContainersByRun returns all containers for a run.
func (d *DB) ListContainersByRun(runID string) ([]ContainerSession, error) {
	return d.listContainers(`SELECT id, run_id, task_id, container_type, image,
		model_id, cpu_limit, mem_limit, started_at, stopped_at, exit_reason
		FROM container_sessions WHERE run_id = ? ORDER BY started_at`, runID)
}

// MarkContainerStopped sets the stopped_at and exit_reason for a container.
func (d *DB) MarkContainerStopped(id, exitReason string) error {
	_, err := d.Exec(`UPDATE container_sessions SET stopped_at = CURRENT_TIMESTAMP, exit_reason = ? WHERE id = ?`,
		exitReason, id)
	if err != nil {
		return fmt.Errorf("marking container stopped: %w", err)
	}
	return nil
}

func (d *DB) listContainers(query string, args ...any) ([]ContainerSession, error) {
	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	defer rows.Close()

	var containers []ContainerSession
	for rows.Next() {
		var cs ContainerSession
		var containerType, startedAt string
		var stoppedAt *string
		if err := rows.Scan(&cs.ID, &cs.RunID, &cs.TaskID, &containerType, &cs.Image,
			&cs.ModelID, &cs.CPULimit, &cs.MemLimit, &startedAt, &stoppedAt, &cs.ExitReason); err != nil {
			return nil, fmt.Errorf("scanning container: %w", err)
		}
		cs.ContainerType = ContainerType(containerType)
		cs.StartedAt = parseTime(startedAt)
		cs.StoppedAt = parseNullTime(stoppedAt)
		containers = append(containers, cs)
	}
	return containers, rows.Err()
}
