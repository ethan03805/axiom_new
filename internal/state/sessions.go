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

// UpdateSessionMode changes a session's mode.
func (d *DB) UpdateSessionMode(id string, mode SessionMode) error {
	res, err := d.Exec(`UPDATE ui_sessions SET mode = ? WHERE id = ?`, string(mode), id)
	if err != nil {
		return fmt.Errorf("updating session mode: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateSessionRunID associates a session with a run.
func (d *DB) UpdateSessionRunID(id string, runID *string) error {
	res, err := d.Exec(`UPDATE ui_sessions SET run_id = ? WHERE id = ?`, runID, id)
	if err != nil {
		return fmt.Errorf("updating session run_id: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetLatestSessionByProject returns the most recently active session for a project.
func (d *DB) GetLatestSessionByProject(projectID string) (*UISession, error) {
	var s UISession
	var mode string
	var createdAt, lastActiveAt string

	err := d.QueryRow(`SELECT id, project_id, run_id, name, mode, created_at, last_active_at
		FROM ui_sessions WHERE project_id = ? ORDER BY last_active_at DESC, rowid DESC LIMIT 1`, projectID).Scan(
		&s.ID, &s.ProjectID, &s.RunID, &s.Name, &mode, &createdAt, &lastActiveAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting latest session: %w", err)
	}
	s.Mode = SessionMode(mode)
	s.CreatedAt = parseTime(createdAt)
	s.LastActiveAt = parseTime(lastActiveAt)
	return &s, nil
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

// GetSessionSummaries returns all summaries for a session ordered by creation time.
func (d *DB) GetSessionSummaries(sessionID string) ([]UISessionSummary, error) {
	rows, err := d.Query(`SELECT id, session_id, summary_kind, content, created_at
		FROM ui_session_summaries WHERE session_id = ? ORDER BY created_at`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("getting session summaries: %w", err)
	}
	defer rows.Close()

	var sums []UISessionSummary
	for rows.Next() {
		var s UISessionSummary
		var createdAt string
		if err := rows.Scan(&s.ID, &s.SessionID, &s.SummaryKind, &s.Content, &createdAt); err != nil {
			return nil, err
		}
		s.CreatedAt = parseTime(createdAt)
		sums = append(sums, s)
	}
	if sums == nil {
		sums = []UISessionSummary{}
	}
	return sums, rows.Err()
}

// GetMaxSeqBySession returns the maximum sequence number for a session, or 0 if empty.
func (d *DB) GetMaxSeqBySession(sessionID string) (int, error) {
	var maxSeq sql.NullInt64
	err := d.QueryRow(`SELECT MAX(seq) FROM ui_messages WHERE session_id = ?`, sessionID).Scan(&maxSeq)
	if err != nil {
		return 0, fmt.Errorf("getting max seq: %w", err)
	}
	if !maxSeq.Valid {
		return 0, nil
	}
	return int(maxSeq.Int64), nil
}

// GetMessageCount returns the number of messages in a session.
func (d *DB) GetMessageCount(sessionID string) (int, error) {
	var count int
	err := d.QueryRow(`SELECT COUNT(*) FROM ui_messages WHERE session_id = ?`, sessionID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("getting message count: %w", err)
	}
	return count, nil
}

// DeleteMessagesBySessionBefore deletes messages with seq < cutoff for a session.
// Returns the number of deleted rows.
func (d *DB) DeleteMessagesBySessionBefore(sessionID string, seqCutoff int) (int64, error) {
	res, err := d.Exec(`DELETE FROM ui_messages WHERE session_id = ? AND seq < ?`,
		sessionID, seqCutoff)
	if err != nil {
		return 0, fmt.Errorf("deleting messages before seq %d: %w", seqCutoff, err)
	}
	return res.RowsAffected()
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

// GetInputHistoryByProject returns recent input history for a project, most recent first.
func (d *DB) GetInputHistoryByProject(projectID string, limit int) ([]UIInputHistory, error) {
	rows, err := d.Query(`SELECT id, project_id, session_id, input_mode, content, created_at
		FROM ui_input_history WHERE project_id = ? ORDER BY id DESC LIMIT ?`,
		projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("getting input history: %w", err)
	}
	defer rows.Close()

	var entries []UIInputHistory
	for rows.Next() {
		var h UIInputHistory
		var createdAt string
		if err := rows.Scan(&h.ID, &h.ProjectID, &h.SessionID, &h.InputMode, &h.Content, &createdAt); err != nil {
			return nil, err
		}
		h.CreatedAt = parseTime(createdAt)
		entries = append(entries, h)
	}
	return entries, rows.Err()
}
