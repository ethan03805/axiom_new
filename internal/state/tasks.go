package state

import (
	"database/sql"
	"fmt"
)

// CreateTask inserts a new task record.
func (d *DB) CreateTask(t *Task) error {
	_, err := d.Exec(`INSERT INTO tasks
		(id, run_id, parent_id, title, description, status, tier, task_type, base_snapshot, eco_ref)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.RunID, t.ParentID, t.Title, t.Description,
		string(t.Status), string(t.Tier), string(t.TaskType), t.BaseSnapshot, t.ECORef)
	if err != nil {
		return fmt.Errorf("creating task: %w", err)
	}
	return nil
}

// GetTask retrieves a task by ID.
func (d *DB) GetTask(id string) (*Task, error) {
	row := d.QueryRow(`SELECT id, run_id, parent_id, title, description, status, tier, task_type,
		base_snapshot, eco_ref, created_at, completed_at
		FROM tasks WHERE id = ?`, id)
	return scanTask(row)
}

// ListTasksByRun returns all tasks for a run.
func (d *DB) ListTasksByRun(runID string) ([]Task, error) {
	rows, err := d.Query(`SELECT id, run_id, parent_id, title, description, status, tier, task_type,
		base_snapshot, eco_ref, created_at, completed_at
		FROM tasks WHERE run_id = ? ORDER BY created_at`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()
	return collectTasks(rows)
}

// ListTasksByStatus returns tasks for a run with a specific status.
func (d *DB) ListTasksByStatus(runID string, status TaskStatus) ([]Task, error) {
	rows, err := d.Query(`SELECT id, run_id, parent_id, title, description, status, tier, task_type,
		base_snapshot, eco_ref, created_at, completed_at
		FROM tasks WHERE run_id = ? AND status = ? ORDER BY created_at`, runID, string(status))
	if err != nil {
		return nil, fmt.Errorf("listing tasks by status: %w", err)
	}
	defer rows.Close()
	return collectTasks(rows)
}

// UpdateTaskStatus transitions a task to a new status with invariant checks.
// Sets completed_at for terminal states.
func (d *DB) UpdateTaskStatus(id string, to TaskStatus) error {
	return d.WithTx(func(tx *sql.Tx) error {
		var currentStatus string
		err := tx.QueryRow(`SELECT status FROM tasks WHERE id = ?`, id).Scan(&currentStatus)
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("reading task status: %w", err)
		}

		from := TaskStatus(currentStatus)
		if !ValidTaskTransition(from, to) {
			return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
		}

		var timestampClause string
		switch to {
		case TaskDone, TaskFailed, TaskBlocked, TaskCancelledECO:
			timestampClause = ", completed_at = CURRENT_TIMESTAMP"
		}

		_, err = tx.Exec(
			`UPDATE tasks SET status = ?`+timestampClause+` WHERE id = ?`,
			string(to), id)
		if err != nil {
			return fmt.Errorf("updating task status: %w", err)
		}
		return nil
	})
}

// --- Dependencies ---

// AddTaskDependency records that taskID depends on dependsOn.
func (d *DB) AddTaskDependency(taskID, dependsOn string) error {
	_, err := d.Exec(`INSERT INTO task_dependencies (task_id, depends_on) VALUES (?, ?)`,
		taskID, dependsOn)
	if err != nil {
		return fmt.Errorf("adding dependency: %w", err)
	}
	return nil
}

// GetTaskDependencies returns the IDs of tasks that taskID depends on.
func (d *DB) GetTaskDependencies(taskID string) ([]string, error) {
	rows, err := d.Query(`SELECT depends_on FROM task_dependencies WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("getting dependencies: %w", err)
	}
	defer rows.Close()

	var deps []string
	for rows.Next() {
		var dep string
		if err := rows.Scan(&dep); err != nil {
			return nil, err
		}
		deps = append(deps, dep)
	}
	return deps, rows.Err()
}

// --- SRS Refs ---

// AddTaskSRSRef links a task to an SRS requirement reference.
func (d *DB) AddTaskSRSRef(taskID, srsRef string) error {
	_, err := d.Exec(`INSERT INTO task_srs_refs (task_id, srs_ref) VALUES (?, ?)`,
		taskID, srsRef)
	if err != nil {
		return fmt.Errorf("adding SRS ref: %w", err)
	}
	return nil
}

// GetTaskSRSRefs returns all SRS references for a task.
func (d *DB) GetTaskSRSRefs(taskID string) ([]string, error) {
	rows, err := d.Query(`SELECT srs_ref FROM task_srs_refs WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("getting SRS refs: %w", err)
	}
	defer rows.Close()

	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// --- Target Files ---

// AddTaskTargetFile records a file targeted by a task.
func (d *DB) AddTaskTargetFile(tf *TaskTargetFile) error {
	_, err := d.Exec(`INSERT INTO task_target_files (task_id, file_path, lock_scope, lock_resource_key)
		VALUES (?, ?, ?, ?)`,
		tf.TaskID, tf.FilePath, tf.LockScope, tf.LockResourceKey)
	if err != nil {
		return fmt.Errorf("adding target file: %w", err)
	}
	return nil
}

// GetTaskTargetFiles returns all target files for a task.
func (d *DB) GetTaskTargetFiles(taskID string) ([]TaskTargetFile, error) {
	rows, err := d.Query(`SELECT task_id, file_path, lock_scope, lock_resource_key
		FROM task_target_files WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("getting target files: %w", err)
	}
	defer rows.Close()

	var files []TaskTargetFile
	for rows.Next() {
		var f TaskTargetFile
		if err := rows.Scan(&f.TaskID, &f.FilePath, &f.LockScope, &f.LockResourceKey); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// --- Locks ---

// AcquireLock atomically acquires a write-set lock. Returns ErrLockConflict
// if the resource is already locked by a different task.
func (d *DB) AcquireLock(resourceType, resourceKey, taskID string) error {
	return d.WithTx(func(tx *sql.Tx) error {
		var holder string
		err := tx.QueryRow(`SELECT task_id FROM task_locks WHERE resource_type = ? AND resource_key = ?`,
			resourceType, resourceKey).Scan(&holder)
		if err == nil {
			if holder == taskID {
				return nil // already held by this task
			}
			return ErrLockConflict
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("checking lock: %w", err)
		}

		_, err = tx.Exec(`INSERT INTO task_locks (resource_type, resource_key, task_id) VALUES (?, ?, ?)`,
			resourceType, resourceKey, taskID)
		if err != nil {
			return fmt.Errorf("acquiring lock: %w", err)
		}
		return nil
	})
}

// ReleaseLock releases a specific lock.
func (d *DB) ReleaseLock(resourceType, resourceKey string) error {
	_, err := d.Exec(`DELETE FROM task_locks WHERE resource_type = ? AND resource_key = ?`,
		resourceType, resourceKey)
	if err != nil {
		return fmt.Errorf("releasing lock: %w", err)
	}
	return nil
}

// ReleaseTaskLocks releases all locks held by a task.
func (d *DB) ReleaseTaskLocks(taskID string) error {
	_, err := d.Exec(`DELETE FROM task_locks WHERE task_id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("releasing task locks: %w", err)
	}
	return nil
}

// GetTaskLocks returns all locks held by a task.
func (d *DB) GetTaskLocks(taskID string) ([]TaskLock, error) {
	rows, err := d.Query(`SELECT resource_type, resource_key, task_id, locked_at
		FROM task_locks WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("getting locks: %w", err)
	}
	defer rows.Close()

	var locks []TaskLock
	for rows.Next() {
		var l TaskLock
		var lockedAt string
		if err := rows.Scan(&l.ResourceType, &l.ResourceKey, &l.TaskID, &lockedAt); err != nil {
			return nil, err
		}
		l.LockedAt = parseTime(lockedAt)
		locks = append(locks, l)
	}
	return locks, rows.Err()
}

// --- Lock Waits ---

// AddLockWait records a task waiting for locks.
func (d *DB) AddLockWait(w *TaskLockWait) error {
	_, err := d.Exec(`INSERT INTO task_lock_waits
		(task_id, wait_reason, requested_resources, blocked_by_task_id)
		VALUES (?, ?, ?, ?)`,
		w.TaskID, w.WaitReason, w.RequestedResources, w.BlockedByTaskID)
	if err != nil {
		return fmt.Errorf("adding lock wait: %w", err)
	}
	return nil
}

// RemoveLockWait removes the lock wait entry for a task.
func (d *DB) RemoveLockWait(taskID string) error {
	_, err := d.Exec(`DELETE FROM task_lock_waits WHERE task_id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("removing lock wait: %w", err)
	}
	return nil
}

// ListLockWaits returns all lock waits for tasks in a given run.
func (d *DB) ListLockWaits(runID string) ([]TaskLockWait, error) {
	rows, err := d.Query(`SELECT w.task_id, w.wait_reason, w.requested_resources, w.blocked_by_task_id, w.created_at
		FROM task_lock_waits w
		JOIN tasks t ON t.id = w.task_id
		WHERE t.run_id = ?`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing lock waits: %w", err)
	}
	defer rows.Close()

	var waits []TaskLockWait
	for rows.Next() {
		var w TaskLockWait
		var blockedBy *string
		var createdAt string
		if err := rows.Scan(&w.TaskID, &w.WaitReason, &w.RequestedResources, &blockedBy, &createdAt); err != nil {
			return nil, err
		}
		w.BlockedByTaskID = blockedBy
		w.CreatedAt = parseTime(createdAt)
		waits = append(waits, w)
	}
	return waits, rows.Err()
}

// --- scan helpers ---

func scanTask(row *sql.Row) (*Task, error) {
	var t Task
	var status, tier, taskType string
	var parentID, description, baseSnapshot *string
	var ecoRef *int64
	var createdAt string
	var completedAt *string

	err := row.Scan(&t.ID, &t.RunID, &parentID, &t.Title, &description,
		&status, &tier, &taskType, &baseSnapshot, &ecoRef, &createdAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning task: %w", err)
	}

	t.ParentID = parentID
	t.Description = description
	t.Status = TaskStatus(status)
	t.Tier = TaskTier(tier)
	t.TaskType = TaskType(taskType)
	t.BaseSnapshot = baseSnapshot
	t.ECORef = ecoRef
	t.CreatedAt = parseTime(createdAt)
	t.CompletedAt = parseNullTime(completedAt)
	return &t, nil
}

func collectTasks(rows *sql.Rows) ([]Task, error) {
	var tasks []Task
	for rows.Next() {
		var t Task
		var status, tier, taskType string
		var parentID, description, baseSnapshot *string
		var ecoRef *int64
		var createdAt string
		var completedAt *string

		err := rows.Scan(&t.ID, &t.RunID, &parentID, &t.Title, &description,
			&status, &tier, &taskType, &baseSnapshot, &ecoRef, &createdAt, &completedAt)
		if err != nil {
			return nil, fmt.Errorf("scanning task row: %w", err)
		}

		t.ParentID = parentID
		t.Description = description
		t.Status = TaskStatus(status)
		t.Tier = TaskTier(tier)
		t.TaskType = TaskType(taskType)
		t.BaseSnapshot = baseSnapshot
		t.ECORef = ecoRef
		t.CreatedAt = parseTime(createdAt)
		t.CompletedAt = parseNullTime(completedAt)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
