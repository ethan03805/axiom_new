package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/state"
)

// RecoveryReport summarizes the work performed during engine startup recovery.
type RecoveryReport struct {
	TasksRequeued         int
	LocksReleased         int
	LockWaitsRequeued     int
	ContainerSessionsSeen int
	StagingEntriesRemoved int
	Warnings              []string
}

type requestedResource struct {
	ResourceType string `json:"resource_type"`
	ResourceKey  string `json:"resource_key"`
}

// Recover executes the phase-19 startup recovery pass.
func (e *Engine) Recover(ctx context.Context) (*RecoveryReport, error) {
	report := &RecoveryReport{}

	e.emitEvent(events.EngineEvent{
		Type:      events.RecoveryStarted,
		Timestamp: time.Now().UTC(),
	})

	if e.container != nil {
		if err := e.container.Cleanup(ctx); err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("container cleanup: %v", err))
			e.emitDiagnosticWarning("", "", "container_cleanup_failed", err.Error())
		}
	}

	if err := e.markRecoveredContainers(); err != nil {
		return nil, err
	}

	containerCount, err := e.countRecoveredContainers()
	if err != nil {
		return nil, err
	}
	report.ContainerSessionsSeen = containerCount

	staleTasks, err := e.requeueStaleInProgressTasks()
	if err != nil {
		return nil, err
	}
	report.TasksRequeued += staleTasks

	locksReleased, err := e.releaseAllLocks()
	if err != nil {
		return nil, err
	}
	report.LocksReleased = locksReleased

	requeuedWaits, err := e.rebuildLockWaits()
	if err != nil {
		return nil, err
	}
	report.TasksRequeued += requeuedWaits
	report.LockWaitsRequeued = requeuedWaits

	removed, err := e.cleanStaging()
	if err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("staging cleanup: %v", err))
		e.emitDiagnosticWarning("", "", "staging_cleanup_failed", err.Error())
	} else {
		report.StagingEntriesRemoved = removed
	}

	if err := e.verifySRSIntegrity(); err != nil {
		return nil, err
	}

	e.emitEvent(events.EngineEvent{
		Type:      events.RecoveryCompleted,
		Timestamp: time.Now().UTC(),
		Details: map[string]any{
			"tasks_requeued":          report.TasksRequeued,
			"locks_released":          report.LocksReleased,
			"lock_waits_requeued":     report.LockWaitsRequeued,
			"container_sessions_seen": report.ContainerSessionsSeen,
			"staging_entries_removed": report.StagingEntriesRemoved,
			"warnings":                report.Warnings,
		},
	})

	return report, nil
}

func (e *Engine) emitDiagnosticWarning(runID, taskID, code, message string) {
	e.emitEvent(events.EngineEvent{
		Type:      events.DiagnosticWarning,
		RunID:     runID,
		TaskID:    taskID,
		Timestamp: time.Now().UTC(),
		Details: map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func (e *Engine) markRecoveredContainers() error {
	rows, err := e.db.Query(`SELECT id FROM container_sessions WHERE stopped_at IS NULL`)
	if err != nil {
		return fmt.Errorf("listing active container sessions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scanning active container session: %w", err)
		}
		if err := e.db.MarkContainerStopped(id, "recovered_startup"); err != nil {
			return fmt.Errorf("marking recovered container stopped: %w", err)
		}
	}
	return rows.Err()
}

func (e *Engine) countRecoveredContainers() (int, error) {
	var count int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM container_sessions WHERE exit_reason = 'recovered_startup'`).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting recovered container sessions: %w", err)
	}
	return count, nil
}

func (e *Engine) requeueStaleInProgressTasks() (int, error) {
	tasks, err := e.listTasksByStatus(state.TaskInProgress)
	if err != nil {
		return 0, err
	}

	requeued := 0
	for _, taskID := range tasks {
		attempts, err := e.db.ListAttemptsByTask(taskID)
		if err != nil {
			return 0, fmt.Errorf("listing attempts for %s: %w", taskID, err)
		}
		if len(attempts) > 0 {
			latest := attempts[len(attempts)-1]
			if isTerminalPhase(latest.Phase) {
				continue
			}
			reason := "recovered after engine restart"
			if err := e.db.UpdateAttemptPhase(latest.ID, state.PhaseFailed); err != nil {
				return 0, fmt.Errorf("marking attempt phase failed: %w", err)
			}
			if err := e.db.UpdateAttemptStatus(latest.ID, state.AttemptFailed); err != nil {
				return 0, fmt.Errorf("marking attempt status failed: %w", err)
			}
			if _, err := e.db.Exec(`UPDATE task_attempts SET failure_reason = ? WHERE id = ?`, reason, latest.ID); err != nil {
				return 0, fmt.Errorf("storing recovery failure reason: %w", err)
			}
		}

		if _, err := e.db.Exec(`UPDATE tasks SET status = ?, completed_at = NULL WHERE id = ?`, string(state.TaskQueued), taskID); err != nil {
			return 0, fmt.Errorf("requeueing task %s: %w", taskID, err)
		}
		requeued++
	}

	return requeued, nil
}

func (e *Engine) rebuildLockWaits() (int, error) {
	rows, err := e.db.Query(`SELECT task_id, requested_resources FROM task_lock_waits`)
	if err != nil {
		return 0, fmt.Errorf("listing lock waits for rebuild: %w", err)
	}
	defer rows.Close()

	requeued := 0
	for rows.Next() {
		var taskID string
		var payload string
		if err := rows.Scan(&taskID, &payload); err != nil {
			return 0, fmt.Errorf("scanning lock wait: %w", err)
		}

		var requested []requestedResource
		if err := json.Unmarshal([]byte(payload), &requested); err != nil {
			return 0, fmt.Errorf("parsing lock wait resources: %w", err)
		}

		if !e.resourcesAvailable(requested) {
			continue
		}

		if err := e.db.UpdateTaskStatus(taskID, state.TaskQueued); err != nil {
			return 0, fmt.Errorf("requeueing lock wait task %s: %w", taskID, err)
		}
		if err := e.db.RemoveLockWait(taskID); err != nil {
			return 0, fmt.Errorf("removing lock wait for %s: %w", taskID, err)
		}
		requeued++
	}

	return requeued, rows.Err()
}

func (e *Engine) resourcesAvailable(resources []requestedResource) bool {
	for _, resource := range resources {
		var holder string
		err := e.db.QueryRow(`SELECT task_id FROM task_locks WHERE resource_type = ? AND resource_key = ?`,
			resource.ResourceType, resource.ResourceKey).Scan(&holder)
		if err == nil {
			return false
		}
	}
	return true
}

func (e *Engine) releaseAllLocks() (int, error) {
	var count int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM task_locks`).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting stale locks: %w", err)
	}
	if _, err := e.db.Exec(`DELETE FROM task_locks`); err != nil {
		return 0, fmt.Errorf("releasing stale locks: %w", err)
	}
	return count, nil
}

func (e *Engine) cleanStaging() (int, error) {
	root := filepath.Join(e.rootDir, ".axiom", "containers", "staging")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading staging directory: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return removed, fmt.Errorf("removing staging entry %s: %w", entry.Name(), err)
		}
		removed++
	}
	return removed, nil
}

func (e *Engine) verifySRSIntegrity() error {
	if err := project.VerifySRS(e.rootDir); err != nil {
		return err
	}

	hashPath := filepath.Join(e.rootDir, ".axiom", project.SRSHashFile)
	storedHashBytes, err := os.ReadFile(hashPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading SRS hash file: %w", err)
	}
	fileHash := strings.TrimSpace(string(storedHashBytes))

	var dbHash *string
	err = e.db.QueryRow(`SELECT srs_hash FROM project_runs WHERE srs_hash IS NOT NULL ORDER BY started_at DESC LIMIT 1`).Scan(&dbHash)
	if err != nil {
		if strings.Contains(err.Error(), "sql: no rows in result set") {
			return nil
		}
		return fmt.Errorf("reading stored SRS hash: %w", err)
	}
	if dbHash != nil && *dbHash != fileHash {
		return fmt.Errorf("SRS integrity check failed: database hash %s does not match file hash %s", *dbHash, fileHash)
	}

	return nil
}

func (e *Engine) listTasksByStatus(status state.TaskStatus) ([]string, error) {
	rows, err := e.db.Query(`SELECT id FROM tasks WHERE status = ?`, string(status))
	if err != nil {
		return nil, fmt.Errorf("listing tasks in status %s: %w", status, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning task id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func isTerminalPhase(phase state.AttemptPhase) bool {
	return phase == state.PhaseSucceeded || phase == state.PhaseFailed || phase == state.PhaseEscalated
}
