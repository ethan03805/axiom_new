package state

import (
	"database/sql"
	"fmt"
)

// CreateSession inserts a new UI session.
func (d *DB) CreateSession(s *UISession) error {
	_, err := d.Exec(`INSERT INTO ui_sessions (id, project_id, run_id, name, mode)
		VALUES (?, ?, ?, ?, ?)`,
		s.ID, s.ProjectID, s.RunID, s.Name, string(s.Mode))
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	return nil
}

// GetSession retrieves a UI session by ID.
func (d *DB) GetSession(id string) (*UISession, error) {
	var s UISession
	var mode string
	var createdAt, lastActiveAt string

	err := d.QueryRow(`SELECT id, project_id, run_id, name, mode, created_at, last_active_at
		FROM ui_sessions WHERE id = ?`, id).Scan(
		&s.ID, &s.ProjectID, &s.RunID, &s.Name, &mode, &createdAt, &lastActiveAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting session: %w", err)
	}
	s.Mode = SessionMode(mode)
	s.CreatedAt = parseTime(createdAt)
	s.LastActiveAt = parseTime(lastActiveAt)
	return &s, nil
}

// ListSessionsByProject returns all sessions for a project.
func (d *DB) ListSessionsByProject(projectID string) ([]UISession, error) {
	rows, err := d.Query(`SELECT id, project_id, run_id, name, mode, created_at, last_active_at
		FROM ui_sessions WHERE project_id = ? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []UISession
	for rows.Next() {
		var s UISession
		var mode, createdAt, lastActiveAt string
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.RunID, &s.Name, &mode,
			&createdAt, &lastActiveAt); err != nil {
			return nil, err
		}
		s.Mode = SessionMode(mode)
		s.CreatedAt = parseTime(createdAt)
		s.LastActiveAt = parseTime(lastActiveAt)
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// UpdateSessionActivity updates the last_active_at timestamp for a session.
func (d *DB) UpdateSessionActivity(id string) error {
	_, err := d.Exec(`UPDATE ui_sessions SET last_active_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("updating session activity: %w", err)
	}
	return nil
}

// --- Messages ---

// AddMessage inserts a transcript message and returns its ID.
func (d *DB) AddMessage(m *UIMessage) (int64, error) {
	res, err := d.Exec(`INSERT INTO ui_messages
		(session_id, seq, role, kind, content, related_task_id, request_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.SessionID, m.Seq, m.Role, m.Kind, m.Content, m.RelatedTaskID, m.RequestID)
	if err != nil {
		return 0, fmt.Errorf("adding message: %w", err)
	}
	return res.LastInsertId()
}

// GetMessages returns all messages for a session ordered by sequence number.
func (d *DB) GetMessages(sessionID string) ([]UIMessage, error) {
	rows, err := d.Query(`SELECT id, session_id, seq, role, kind, content,
		related_task_id, request_id, created_at
		FROM ui_messages WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("getting messages: %w", err)
	}
	defer rows.Close()

	var msgs []UIMessage
	for rows.Next() {
		var m UIMessage
		var createdAt string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Seq, &m.Role, &m.Kind, &m.Content,
			&m.RelatedTaskID, &m.RequestID, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt = parseTime(createdAt)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// --- Summaries ---

// AddSessionSummary inserts a compacted session summary and returns its ID.
func (d *DB) AddSessionSummary(s *UISessionSummary) (int64, error) {
	res, err := d.Exec(`INSERT INTO ui_session_summaries (session_id, summary_kind, content)
		VALUES (?, ?, ?)`, s.SessionID, s.SummaryKind, s.Content)
	if err != nil {
		return 0, fmt.Errorf("adding session summary: %w", err)
	}
	return res.LastInsertId()
}

// --- Input History ---

// AddInputHistory records a CLI input entry and returns its ID.
func (d *DB) AddInputHistory(h *UIInputHistory) (int64, error) {
	res, err := d.Exec(`INSERT INTO ui_input_history (project_id, session_id, input_mode, content)
		VALUES (?, ?, ?, ?)`, h.ProjectID, h.SessionID, h.InputMode, h.Content)
	if err != nil {
		return 0, fmt.Errorf("adding input history: %w", err)
	}
	return res.LastInsertId()
}
