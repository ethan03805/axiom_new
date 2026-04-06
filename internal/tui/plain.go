package tui

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/session"
)

// PlainRenderer is the line-oriented plain-text fallback for non-interactive
// environments (Section 26.2.11). It preserves the same workflow states and
// approval prompts as the full-screen TUI.
type PlainRenderer struct {
	engine  *engine.Engine
	session *session.Manager
	cfg     *config.Config
	projID  string
	log     *slog.Logger
}

// NewPlainRenderer creates a plain-text renderer.
func NewPlainRenderer(eng *engine.Engine, mgr *session.Manager, cfg *config.Config, projectID string, log *slog.Logger) *PlainRenderer {
	return &PlainRenderer{
		engine:  eng,
		session: mgr,
		cfg:     cfg,
		projID:  projectID,
		log:     log,
	}
}

// RenderStartup writes the deterministic startup frame to w.
func (r *PlainRenderer) RenderStartup(w io.Writer) error {
	summary, err := r.session.StartupSummary(r.projID)
	if err != nil {
		return fmt.Errorf("getting startup summary: %w", err)
	}

	fmt.Fprintf(w, "Axiom — %s\n", summary.ProjectName)
	fmt.Fprintf(w, "  Mode:    %s\n", summary.Mode)
	fmt.Fprintf(w, "  Root:    %s\n", summary.RootDir)

	if summary.Branch != "" {
		fmt.Fprintf(w, "  Branch:  %s\n", summary.Branch)
	}
	if summary.RunID != "" {
		fmt.Fprintf(w, "  Run:     %s (%s)\n", summary.RunID[:8], summary.RunStatus)
	}

	fmt.Fprintf(w, "  Budget:  $%.2f / $%.2f\n", summary.Budget.SpentUSD, summary.Budget.MaxUSD)

	if summary.Tasks.Total > 0 {
		fmt.Fprintf(w, "  Tasks:   %d total, %d done, %d running\n",
			summary.Tasks.Total, summary.Tasks.Done, summary.Tasks.InProgress)
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", summary.ActionCard)
	fmt.Fprintln(w)

	if len(summary.Commands) > 0 {
		fmt.Fprintf(w, "  Commands: %s\n", strings.Join(summary.Commands, "  "))
	}

	return nil
}

// RenderMessage writes a single message line to w.
func (r *PlainRenderer) RenderMessage(w io.Writer, role, content string) {
	switch role {
	case "user":
		fmt.Fprintf(w, "> %s\n", content)
	case "assistant":
		fmt.Fprintf(w, "  %s\n", content)
	case "system":
		fmt.Fprintf(w, "  [system] %s\n", content)
	default:
		fmt.Fprintf(w, "  %s\n", content)
	}
}

// RenderStatus writes the current project status to w.
func (r *PlainRenderer) RenderStatus(w io.Writer) error {
	status, err := r.engine.GetRunStatus(r.projID)
	if err != nil {
		return fmt.Errorf("getting status: %w", err)
	}

	mode := r.session.DetermineMode(r.projID)

	fmt.Fprintf(w, "Project: %s\n", status.ProjectName)
	fmt.Fprintf(w, "  Root:   %s\n", status.RootDir)
	fmt.Fprintf(w, "  Mode:   %s\n", mode)

	if status.Run != nil {
		fmt.Fprintf(w, "  Run:    %s (%s)\n", status.Run.ID[:8], status.Run.Status)
		fmt.Fprintf(w, "  Branch: %s\n", status.Run.WorkBranch)
		fmt.Fprintf(w, "  Budget: $%.2f / $%.2f\n", status.Budget.SpentUSD, status.Budget.MaxUSD)

		if status.Tasks.Total > 0 {
			fmt.Fprintf(w, "  Tasks:  %d total", status.Tasks.Total)
			if status.Tasks.Done > 0 {
				fmt.Fprintf(w, ", %d done", status.Tasks.Done)
			}
			if status.Tasks.InProgress > 0 {
				fmt.Fprintf(w, ", %d running", status.Tasks.InProgress)
			}
			if status.Tasks.Queued > 0 {
				fmt.Fprintf(w, ", %d queued", status.Tasks.Queued)
			}
			if status.Tasks.Failed > 0 {
				fmt.Fprintf(w, ", %d failed", status.Tasks.Failed)
			}
			fmt.Fprintln(w)
		}
	} else {
		fmt.Fprintf(w, "  Run:    idle\n")
		fmt.Fprintf(w, "  Budget: $%.2f configured\n", r.cfg.Budget.MaxUSD)
	}

	return nil
}

// RenderSessionList writes the list of resumable sessions to w.
func (r *PlainRenderer) RenderSessionList(w io.Writer, projectID string) error {
	sessions, err := r.session.ListSessions(projectID)
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Fprintln(w, "No sessions found.")
		return nil
	}

	fmt.Fprintf(w, "Sessions for project:\n")
	for _, s := range sessions {
		name := "(unnamed)"
		if s.Name != nil {
			name = *s.Name
		}
		runInfo := ""
		if s.RunID != nil {
			runInfo = fmt.Sprintf(" run:%s", (*s.RunID)[:8])
		}
		fmt.Fprintf(w, "  %s  %s  mode:%s%s  last:%s\n",
			s.ID[:8], name, s.Mode, runInfo,
			s.LastActiveAt.Format("2006-01-02 15:04"))
	}

	return nil
}

// RenderExport writes a session export to w.
func (r *PlainRenderer) RenderExport(w io.Writer, export *session.SessionExport) {
	fmt.Fprintf(w, "Session Export: %s\n", export.SessionID[:8])
	fmt.Fprintf(w, "  Project:  %s\n", export.ProjectID)
	fmt.Fprintf(w, "  Mode:     %s\n", export.Mode)
	fmt.Fprintf(w, "  Created:  %s\n", export.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintln(w)

	if len(export.Summaries) > 0 {
		fmt.Fprintln(w, "--- Compaction Summaries ---")
		for _, s := range export.Summaries {
			fmt.Fprintf(w, "[%s] %s\n", s.Kind, s.Content)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "--- Transcript ---")
	for _, msg := range export.Messages {
		ts := msg.Timestamp.Format("15:04:05")
		switch msg.Role {
		case "user":
			fmt.Fprintf(w, "[%s] > %s\n", ts, msg.Content)
		case "assistant":
			fmt.Fprintf(w, "[%s]   %s\n", ts, msg.Content)
		case "system":
			fmt.Fprintf(w, "[%s]   [%s] %s\n", ts, msg.Kind, msg.Content)
		default:
			fmt.Fprintf(w, "[%s]   %s\n", ts, msg.Content)
		}
	}
}

// RenderApproval writes an approval prompt to w and reads the decision.
func (r *PlainRenderer) RenderApproval(w io.Writer, approvalType, description string) {
	fmt.Fprintf(w, "\n=== %s APPROVAL REQUIRED ===\n", strings.ToUpper(approvalType))
	fmt.Fprintf(w, "%s\n", description)
	fmt.Fprintf(w, "  [approve] Accept    [reject] Decline\n")
}

// RenderTaskList writes the current task summary to w.
func (r *PlainRenderer) RenderTaskList(w io.Writer, tasks engine.TaskSummary) {
	if tasks.Total == 0 {
		fmt.Fprintln(w, "No tasks.")
		return
	}

	fmt.Fprintf(w, "Tasks: %d total\n", tasks.Total)
	entries := []struct {
		label string
		count int
	}{
		{"  Done:        ", tasks.Done},
		{"  In progress: ", tasks.InProgress},
		{"  Queued:      ", tasks.Queued},
		{"  Waiting:     ", tasks.WaitingLock},
		{"  Failed:      ", tasks.Failed},
		{"  Blocked:     ", tasks.Blocked},
	}
	for _, e := range entries {
		if e.count > 0 {
			fmt.Fprintf(w, "%s%d\n", e.label, e.count)
		}
	}
}

// RenderEvent writes a single event line suitable for CI/pipe output.
func (r *PlainRenderer) RenderEvent(w io.Writer, eventType string, details map[string]any) {
	parts := []string{fmt.Sprintf("[%s]", eventType)}
	for k, v := range details {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	fmt.Fprintln(w, strings.Join(parts, " "))
}

// RunStatus writes a brief status line suitable for polling scripts.
func (r *PlainRenderer) RunStatus(w io.Writer) error {
	status, err := r.engine.GetRunStatus(r.projID)
	if err != nil {
		return err
	}
	if status.Run == nil {
		fmt.Fprintln(w, "idle")
		return nil
	}
	fmt.Fprintf(w, "%s tasks:%d/%d budget:$%.2f/$%.2f\n",
		status.Run.Status,
		status.Tasks.Done, status.Tasks.Total,
		status.Budget.SpentUSD, status.Budget.MaxUSD)
	return nil
}
