package state

import (
	"fmt"
)

// CreateEvent inserts an event log entry and returns its ID.
func (d *DB) CreateEvent(e *Event) (int64, error) {
	res, err := d.Exec(`INSERT INTO events
		(run_id, event_type, task_id, agent_type, agent_id, details)
		VALUES (?, ?, ?, ?, ?, ?)`,
		e.RunID, e.EventType, e.TaskID, e.AgentType, e.AgentID, e.Details)
	if err != nil {
		return 0, fmt.Errorf("creating event: %w", err)
	}
	return res.LastInsertId()
}

// ListEventsByRun returns all events for a run ordered by timestamp.
func (d *DB) ListEventsByRun(runID string) ([]Event, error) {
	rows, err := d.Query(`SELECT id, run_id, event_type, task_id, agent_type, agent_id, details, timestamp
		FROM events WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}
	defer rows.Close()
	return collectEvents(rows)
}

// ListEventsByType returns events of a specific type for a run.
func (d *DB) ListEventsByType(runID, eventType string) ([]Event, error) {
	rows, err := d.Query(`SELECT id, run_id, event_type, task_id, agent_type, agent_id, details, timestamp
		FROM events WHERE run_id = ? AND event_type = ? ORDER BY id`, runID, eventType)
	if err != nil {
		return nil, fmt.Errorf("listing events by type: %w", err)
	}
	defer rows.Close()
	return collectEvents(rows)
}

func collectEvents(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]Event, error) {
	var events []Event
	for rows.Next() {
		var e Event
		var ts string
		if err := rows.Scan(&e.ID, &e.RunID, &e.EventType, &e.TaskID,
			&e.AgentType, &e.AgentID, &e.Details, &ts); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		e.Timestamp = parseTime(ts)
		events = append(events, e)
	}
	return events, rows.Err()
}

// --- Cost Log ---

// CreateCostLog inserts a cost log entry and returns its ID.
func (d *DB) CreateCostLog(c *CostLogEntry) (int64, error) {
	res, err := d.Exec(`INSERT INTO cost_log
		(run_id, task_id, attempt_id, agent_type, model_id, input_tokens, output_tokens, cost_usd)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.RunID, c.TaskID, c.AttemptID, c.AgentType, c.ModelID,
		c.InputTokens, c.OutputTokens, c.CostUSD)
	if err != nil {
		return 0, fmt.Errorf("creating cost log: %w", err)
	}
	return res.LastInsertId()
}

// ListCostLogByRun returns all cost entries for a run.
func (d *DB) ListCostLogByRun(runID string) ([]CostLogEntry, error) {
	rows, err := d.Query(`SELECT id, run_id, task_id, attempt_id, agent_type, model_id,
		input_tokens, output_tokens, cost_usd, timestamp
		FROM cost_log WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing cost log: %w", err)
	}
	defer rows.Close()

	var entries []CostLogEntry
	for rows.Next() {
		var c CostLogEntry
		var ts string
		if err := rows.Scan(&c.ID, &c.RunID, &c.TaskID, &c.AttemptID,
			&c.AgentType, &c.ModelID, &c.InputTokens, &c.OutputTokens,
			&c.CostUSD, &ts); err != nil {
			return nil, fmt.Errorf("scanning cost log: %w", err)
		}
		c.Timestamp = parseTime(ts)
		entries = append(entries, c)
	}
	return entries, rows.Err()
}

// TotalCostByRun returns the total cost for a run.
func (d *DB) TotalCostByRun(runID string) (float64, error) {
	var total *float64
	err := d.QueryRow(`SELECT SUM(cost_usd) FROM cost_log WHERE run_id = ?`, runID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("summing cost: %w", err)
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}
