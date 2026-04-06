// Package session implements the Session UX Manager (Architecture Section 26.2.3).
// It manages interactive terminal sessions: creation, resumption, mode transitions,
// startup summaries, transcript persistence, compaction, export, and prompt suggestions.
// The TUI and plain CLI renderer consume this service; it does not perform any rendering.
package session

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

// StartupSummaryData is the deterministic startup frame data (Section 26.2.4).
type StartupSummaryData struct {
	ProjectName string
	ProjectSlug string
	RootDir     string
	Branch      string
	Mode        state.SessionMode
	RunID       string
	RunStatus   string
	ActionCard  string
	Tasks       engine.TaskSummary
	Budget      engine.BudgetSummary
	Commands    []string
}

// SessionExport is the exported transcript for a session.
type SessionExport struct {
	SessionID  string
	ProjectID  string
	Mode       string
	CreatedAt  time.Time
	Messages   []ExportMessage
	Summaries  []ExportSummary
}

// ExportMessage is a single message in an export.
type ExportMessage struct {
	Seq       int
	Role      string
	Kind      string
	Content   string
	Timestamp time.Time
}

// ExportSummary is a compaction summary in an export.
type ExportSummary struct {
	Kind      string
	Content   string
	Timestamp time.Time
}

// Manager is the Session UX Manager (Architecture Section 26.2.3).
type Manager struct {
	engine *engine.Engine
	cfg    *config.Config
	log    *slog.Logger

	mu       sync.Mutex
	seqCache map[string]int // session ID -> next sequence number
}

// New creates a new Session UX Manager.
func New(eng *engine.Engine, cfg *config.Config, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		engine:   eng,
		cfg:      cfg,
		log:      log,
		seqCache: make(map[string]int),
	}
}

// CreateSession creates a new interactive session for a project.
// The mode is determined from the current run state.
func (m *Manager) CreateSession(projectID string) (*state.UISession, error) {
	mode := m.DetermineMode(projectID)

	sess := &state.UISession{
		ID:        uuid.New().String(),
		ProjectID: projectID,
		Mode:      mode,
	}

	// Link to active run if one exists
	run, err := m.engine.DB().GetActiveRun(projectID)
	if err == nil {
		sess.RunID = &run.ID
	}

	if err := m.engine.DB().CreateSession(sess); err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}

	// Read back to get timestamps
	created, err := m.engine.DB().GetSession(sess.ID)
	if err != nil {
		return nil, err
	}

	m.log.Info("session created", "id", sess.ID, "mode", mode, "project", projectID)
	return created, nil
}

// ResumeSession resumes an existing session by ID. Updates the activity timestamp.
func (m *Manager) ResumeSession(sessionID string) (*state.UISession, error) {
	sess, err := m.engine.DB().GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("resuming session: %w", err)
	}

	if err := m.engine.DB().UpdateSessionActivity(sessionID); err != nil {
		m.log.Warn("failed to update session activity", "id", sessionID, "error", err)
	}

	// Refresh mode from current run state
	newMode := m.DetermineMode(sess.ProjectID)
	if newMode != sess.Mode {
		if err := m.engine.DB().UpdateSessionMode(sessionID, newMode); err != nil {
			m.log.Warn("failed to update session mode", "id", sessionID, "error", err)
		}
		sess.Mode = newMode
	}

	m.log.Info("session resumed", "id", sessionID, "mode", sess.Mode)
	return sess, nil
}

// ResumeOrCreateSession resumes the latest session for a project, or creates a new one.
func (m *Manager) ResumeOrCreateSession(projectID string) (*state.UISession, error) {
	sess, err := m.engine.DB().GetLatestSessionByProject(projectID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return m.CreateSession(projectID)
		}
		return nil, err
	}
	return m.ResumeSession(sess.ID)
}

// DetermineMode computes the correct session mode from the current run state.
// Per Architecture Section 26.2.7:
//   - No run or draft: bootstrap
//   - Awaiting SRS approval: approval
//   - Active or paused: execution
//   - Completed/cancelled/error: postrun
func (m *Manager) DetermineMode(projectID string) state.SessionMode {
	// First check for an active run (draft, awaiting, active, paused)
	run, err := m.engine.DB().GetActiveRun(projectID)
	if err != nil {
		// No active run — check if there's a completed/cancelled/error run
		latestRun, latestErr := m.engine.DB().GetLatestRunByProject(projectID)
		if latestErr != nil {
			return state.SessionBootstrap
		}
		return modeFromRunStatus(latestRun.Status)
	}
	return modeFromRunStatus(run.Status)
}

func modeFromRunStatus(status state.RunStatus) state.SessionMode {
	switch status {
	case state.RunDraftSRS:
		return state.SessionBootstrap
	case state.RunAwaitingSRSApproval:
		return state.SessionApproval
	case state.RunActive, state.RunPaused:
		return state.SessionExecution
	case state.RunCompleted, state.RunCancelled, state.RunError:
		return state.SessionPostrun
	default:
		return state.SessionBootstrap
	}
}

// StartupSummary generates the deterministic startup frame (Section 26.2.4).
// This is engine-authored, not LLM-authored.
func (m *Manager) StartupSummary(projectID string) (*StartupSummaryData, error) {
	status, err := m.engine.GetRunStatus(projectID)
	if err != nil {
		return nil, fmt.Errorf("getting run status: %w", err)
	}

	mode := m.DetermineMode(projectID)

	summary := &StartupSummaryData{
		ProjectName: status.ProjectName,
		ProjectSlug: status.ProjectSlug,
		RootDir:     status.RootDir,
		Mode:        mode,
		Tasks:       status.Tasks,
		Budget:      status.Budget,
	}

	if status.Run != nil {
		summary.RunID = status.Run.ID
		summary.RunStatus = string(status.Run.Status)
		summary.Branch = status.Run.WorkBranch
	}

	summary.ActionCard = m.buildActionCard(mode, status)
	summary.Commands = m.buildCommandRow(mode)

	// Emit view-model event
	m.engine.Bus().Publish(events.EngineEvent{
		Type: events.StartupSummary,
		Details: map[string]any{
			"project_id": projectID,
			"mode":       string(mode),
		},
		Timestamp: time.Now().UTC(),
	})

	return summary, nil
}

// buildActionCard produces the primary action card text from state (Section 26.2.4).
func (m *Manager) buildActionCard(mode state.SessionMode, status *engine.RunStatusProjection) string {
	switch mode {
	case state.SessionBootstrap:
		return "Describe what you want to build."
	case state.SessionApproval:
		return "Review the SRS and approve or reject it."
	case state.SessionExecution:
		var parts []string
		if status.Tasks.InProgress > 0 {
			parts = append(parts, fmt.Sprintf("%d task(s) in progress", status.Tasks.InProgress))
		}
		if status.Tasks.Queued > 0 {
			parts = append(parts, fmt.Sprintf("%d queued", status.Tasks.Queued))
		}
		if status.Tasks.Failed > 0 {
			parts = append(parts, fmt.Sprintf("%d failed", status.Tasks.Failed))
		}
		if len(parts) == 0 {
			return "Execution active. Use /status for details."
		}
		return "Execution active: " + strings.Join(parts, ", ") + "."
	case state.SessionPostrun:
		return "Run complete. Review diffs, export, or start a new session."
	default:
		return "Ready."
	}
}

// buildCommandRow returns the suggested commands for the current mode.
func (m *Manager) buildCommandRow(mode state.SessionMode) []string {
	base := []string{"/status", "/help"}
	switch mode {
	case state.SessionBootstrap:
		return append([]string{"/new"}, base...)
	case state.SessionApproval:
		return append([]string{"/srs"}, base...)
	case state.SessionExecution:
		return append([]string{"/tasks", "/diff", "/budget"}, base...)
	case state.SessionPostrun:
		return append([]string{"/diff", "/resume"}, base...)
	default:
		return base
	}
}

// AddTranscriptMessage adds a message to a session's transcript.
// Returns the assigned sequence number.
func (m *Manager) AddTranscriptMessage(sessionID, role, kind, content string) (int, error) {
	m.mu.Lock()
	if _, ok := m.seqCache[sessionID]; !ok {
		// Initialize from DB to avoid collisions on resumed sessions
		maxSeq, err := m.engine.DB().GetMaxSeqBySession(sessionID)
		if err != nil {
			m.mu.Unlock()
			return 0, fmt.Errorf("initializing seq cache: %w", err)
		}
		m.seqCache[sessionID] = maxSeq
	}
	seq := m.seqCache[sessionID] + 1
	m.seqCache[sessionID] = seq
	m.mu.Unlock()

	msg := &state.UIMessage{
		SessionID: sessionID,
		Seq:       seq,
		Role:      role,
		Kind:      kind,
		Content:   content,
	}
	if _, err := m.engine.DB().AddMessage(msg); err != nil {
		return 0, fmt.Errorf("adding transcript message: %w", err)
	}

	if err := m.engine.DB().UpdateSessionActivity(sessionID); err != nil {
		m.log.Warn("failed to update session activity", "id", sessionID, "error", err)
	}

	return seq, nil
}

// CompactSession compacts old transcript messages, keeping the last keepCount.
// A summary of compacted messages is stored before deletion.
func (m *Manager) CompactSession(sessionID string, keepCount int) error {
	msgs, err := m.engine.DB().GetMessages(sessionID)
	if err != nil {
		return fmt.Errorf("getting messages for compaction: %w", err)
	}

	if len(msgs) <= keepCount {
		return nil // nothing to compact
	}

	// Build summary of messages that will be removed
	cutoffIdx := len(msgs) - keepCount
	toCompact := msgs[:cutoffIdx]

	var summaryParts []string
	for _, msg := range toCompact {
		summaryParts = append(summaryParts, fmt.Sprintf("[%s/%s] %s", msg.Role, msg.Kind, truncate(msg.Content, 100)))
	}
	summaryContent := fmt.Sprintf("Compacted %d messages:\n%s",
		len(toCompact), strings.Join(summaryParts, "\n"))

	// Store summary
	summary := &state.UISessionSummary{
		SessionID:   sessionID,
		SummaryKind: "transcript_compaction",
		Content:     summaryContent,
	}
	if _, err := m.engine.DB().AddSessionSummary(summary); err != nil {
		return fmt.Errorf("adding compaction summary: %w", err)
	}

	// Delete old messages (those before the cutoff seq)
	cutoffSeq := msgs[cutoffIdx].Seq
	if _, err := m.engine.DB().DeleteMessagesBySessionBefore(sessionID, cutoffSeq); err != nil {
		return fmt.Errorf("deleting compacted messages: %w", err)
	}

	// Emit event
	m.engine.Bus().Publish(events.EngineEvent{
		Type: events.TranscriptCompacted,
		Details: map[string]any{
			"session_id":       sessionID,
			"compacted_count":  len(toCompact),
			"remaining_count":  keepCount,
		},
	})

	return nil
}

// ExportSession exports a session's full transcript and summaries.
func (m *Manager) ExportSession(sessionID string) (*SessionExport, error) {
	sess, err := m.engine.DB().GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("getting session: %w", err)
	}

	msgs, err := m.engine.DB().GetMessages(sessionID)
	if err != nil {
		return nil, fmt.Errorf("getting messages: %w", err)
	}

	sums, err := m.engine.DB().GetSessionSummaries(sessionID)
	if err != nil {
		return nil, fmt.Errorf("getting summaries: %w", err)
	}

	export := &SessionExport{
		SessionID: sess.ID,
		ProjectID: sess.ProjectID,
		Mode:      string(sess.Mode),
		CreatedAt: sess.CreatedAt,
	}

	for _, msg := range msgs {
		export.Messages = append(export.Messages, ExportMessage{
			Seq:       msg.Seq,
			Role:      msg.Role,
			Kind:      msg.Kind,
			Content:   msg.Content,
			Timestamp: msg.CreatedAt,
		})
	}

	for _, s := range sums {
		export.Summaries = append(export.Summaries, ExportSummary{
			Kind:      s.SummaryKind,
			Content:   s.Content,
			Timestamp: s.CreatedAt,
		})
	}

	return export, nil
}

// ListSessions returns all sessions for a project. This provides a view-model
// layer so that callers (TUI, plain renderer) don't need direct DB access.
func (m *Manager) ListSessions(projectID string) ([]state.UISession, error) {
	return m.engine.DB().ListSessionsByProject(projectID)
}

// PromptSuggestions returns suggested prompts based on current state.
// Per Section 26.2.8, priority is:
//  1. Pending approval actions
//  2. Active-run operator actions
//  3. Repo-aware bootstrap prompts
//  4. Generic fallback
func (m *Manager) PromptSuggestions(projectID string) []string {
	mode := m.DetermineMode(projectID)

	switch mode {
	case state.SessionApproval:
		return []string{
			"Review and approve the SRS",
			"Reject SRS with feedback",
		}
	case state.SessionExecution:
		return []string{
			"/status — View current progress",
			"/tasks — Show task list",
			"/diff — Preview latest changes",
			"/budget — Check budget usage",
			"/pause — Pause execution",
		}
	case state.SessionPostrun:
		return []string{
			"/diff — Review final changes",
			"Export session transcript",
			"Start a new run",
		}
	default:
		return []string{
			"Describe what you want to build",
			"Build a REST API with authentication",
			"Add unit tests for the auth module",
		}
	}
}

// RecordInput records an input to the project's input history.
func (m *Manager) RecordInput(projectID, sessionID, inputMode, content string) error {
	entry := &state.UIInputHistory{
		ProjectID: projectID,
		SessionID: &sessionID,
		InputMode: inputMode,
		Content:   content,
	}
	if _, err := m.engine.DB().AddInputHistory(entry); err != nil {
		return fmt.Errorf("recording input: %w", err)
	}
	return nil
}

// InputHistory returns recent input history strings for a project, most recent first.
func (m *Manager) InputHistory(projectID string, limit int) ([]string, error) {
	entries, err := m.engine.DB().GetInputHistoryByProject(projectID, limit)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, e := range entries {
		result = append(result, e.Content)
	}
	return result, nil
}

// UpdateMode transitions a session's mode and emits a SessionModeChanged event.
func (m *Manager) UpdateMode(sessionID string, newMode state.SessionMode) error {
	if err := m.engine.DB().UpdateSessionMode(sessionID, newMode); err != nil {
		return err
	}

	m.engine.Bus().Publish(events.EngineEvent{
		Type: events.SessionModeChanged,
		Details: map[string]any{
			"session_id": sessionID,
			"mode":       string(newMode),
		},
	})

	return nil
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
