package tui

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/session"
	"github.com/openaxiom/axiom/internal/state"
)

// OverlayKind identifies the current overlay surface.
type OverlayKind int

const (
	OverlayNone OverlayKind = iota
	OverlayHelp
	OverlaySlashPalette
	OverlayDiff
	OverlaySRS
	OverlayECO
)

// TranscriptEntry is a single line in the transcript viewport.
type TranscriptEntry struct {
	Role    string
	Kind    string
	Content string
}

// startupMsg is sent after Init to trigger startup summary loading.
type startupMsg struct{}

// eventMsg wraps an engine event for the Bubble Tea update loop.
type eventMsg events.EngineEvent

// runStartedMsg is delivered back to Update when an async StartRun call
// succeeds. It carries the newly created run so the transcript and action
// card can be refreshed synchronously on the Bubble Tea goroutine.
type runStartedMsg struct{ run *state.ProjectRun }

// runStartFailedMsg is delivered back to Update when an async StartRun call
// fails. The TUI renders the error in the transcript; it must not silently
// swallow the error per the Architecture §28.2 clean-tree contract.
type runStartFailedMsg struct{ err error }

// Model is the main Bubble Tea model for the Axiom TUI (Section 26.2).
type Model struct {
	engine    *engine.Engine
	session   *session.Manager
	cfg       *config.Config
	projectID string
	log       *slog.Logger
	theme     Theme

	// Layout
	width  int
	height int

	// Session state
	sess     *state.UISession
	startup  *session.StartupSummaryData
	mode     state.SessionMode

	// Components
	composer  textarea.Model
	viewport viewport.Model

	// Transcript
	transcript []TranscriptEntry

	// Overlay
	overlay OverlayKind

	// Task summary
	tasks  engine.TaskSummary
	budget engine.BudgetSummary

	// Event subscription
	eventCh <-chan events.EngineEvent
	subID   uint64

	// Input history
	inputHistory []string
	historyIdx   int

	// pendingCmd is set by slash-command handlers that need to emit an
	// asynchronous tea.Cmd (e.g. /new "<prompt>" must trigger StartRun off
	// the Bubble Tea goroutine). submitInput drains this field after the
	// handler returns so the command is dispatched exactly once.
	pendingCmd tea.Cmd

	// pendingShellLikePrompt buffers a bootstrap-mode prompt that looks
	// like a shell-form axiom command (e.g. "srs show"). The TUI warns
	// the operator that they may have meant to type "/srs show" and
	// waits for a second Enter to confirm treating it as a project
	// prompt. The second submit clears this field and proceeds to
	// StartRun as usual.
	pendingShellLikePrompt string

	// pendingNewRunConfirm is set when /new has been issued while an
	// active run exists. The operator must re-issue /new "<prompt>"
	// to confirm replacement; the second invocation passes Force=true
	// to StartRun. This two-step gate prevents a single /new from
	// silently clobbering orchestrator work.
	pendingNewRunConfirm bool

	ready bool
}

// shellLikeCommands is the set of prompt first-words that look like
// bare shell-form invocations of the `axiom` CLI. When bootstrap-mode
// input starts with one of these followed by whitespace, submitInput
// shows a "did you mean /<command>?" hint on the first submit and
// requires a second Enter to confirm.
var shellLikeCommands = map[string]struct{}{
	"srs":     {},
	"status":  {},
	"export":  {},
	"task":    {},
	"tasks":   {},
	"run":     {},
	"cancel":  {},
	"pause":   {},
	"resume":  {},
	"eco":     {},
	"budget":  {},
	"tokens":  {},
	"session": {},
	"diff":    {},
	"help":    {},
}

// looksLikeShellCommand returns true if s begins with a known axiom
// subcommand followed by whitespace (or is exactly the subcommand). The
// check is case-insensitive and only matches the first whitespace-
// delimited token so longer prompts like "srs looks great" still
// trigger the hint while phrases like "srsly implement" do not.
func looksLikeShellCommand(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	// Split on the first whitespace character.
	first := trimmed
	if idx := strings.IndexAny(trimmed, " \t"); idx >= 0 {
		first = trimmed[:idx]
	}
	_, ok := shellLikeCommands[strings.ToLower(first)]
	return ok
}

// NewModel creates a new TUI model. The model subscribes to engine events
// and uses the Session UX Manager for all session operations.
func NewModel(eng *engine.Engine, mgr *session.Manager, cfg *config.Config, projectID string, log *slog.Logger) *Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message or / for commands..."
	ta.CharLimit = 4096
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.Focus()

	vp := viewport.New(80, 20)

	ch, subID := eng.Bus().Subscribe(nil)

	return &Model{
		engine:    eng,
		session:   mgr,
		cfg:       cfg,
		projectID: projectID,
		log:       log,
		theme:     DefaultTheme(),
		composer:  ta,
		viewport:  vp,
		eventCh:   ch,
		subID:     subID,
		historyIdx: -1,
	}
}

// Init starts the TUI by requesting the window size and loading startup data.
func (m *Model) Init() tea.Cmd {
	return func() tea.Msg {
		return startupMsg{}
	}
}

// Update handles messages in the Bubble Tea event loop.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLayout()
		return m, nil

	case startupMsg:
		m.handleStartupSummary()
		return m, m.listenForEvents()

	case eventMsg:
		m.handleEvent(events.EngineEvent(msg))
		return m, m.listenForEvents()

	case runStartedMsg:
		// StartRun succeeded. Report the outcome in the transcript and
		// refresh the action card so the operator sees the mode transition
		// (draft_srs is still bootstrap — the mode label only changes when
		// the external orchestrator submits an SRS draft — but the run
		// metadata line makes the state visible immediately).
		m.appendTranscript("system", "system_card",
			fmt.Sprintf("Run created: %s on branch %s. Waiting for external orchestrator to submit SRS draft.",
				msg.run.ID[:8], msg.run.WorkBranch))
		m.refreshAfterStateChange()
		return m, nil

	case runStartFailedMsg:
		// Distinguish "active run already exists" from generic
		// StartRun failures. The former is a confirmation gate — the
		// operator must explicitly use /new to replace the prior run
		// — and we surface the existing run's ID and status so the
		// operator can make an informed choice.
		var activeErr *engine.ActiveRunExistsError
		if errors.As(msg.err, &activeErr) {
			m.appendTranscript("system", "system_card",
				fmt.Sprintf(
					"A run already exists (%s, %s). "+
						"Use /new to confirm replacement, or /resume to continue it. "+
						"The prior run's draft files remain on disk — use 'axiom export' to audit before replacing.",
					activeErr.RunID, activeErr.Status))
			return m, nil
		}
		m.appendTranscript("system", "system_card",
			fmt.Sprintf("Failed to start run: %v. Commit or stash changes and try again.", msg.err))
		return m, nil

	case tea.KeyMsg:
		if m.overlay != OverlayNone {
			return m.updateOverlay(msg)
		}

		switch msg.Type {
		case tea.KeyCtrlC:
			m.engine.Bus().Unsubscribe(m.subID)
			return m, tea.Quit

		case tea.KeyEsc:
			if m.overlay != OverlayNone {
				m.overlay = OverlayNone
				return m, nil
			}

		case tea.KeyEnter:
			input := strings.TrimSpace(m.composer.Value())
			if input != "" {
				cmd := m.submitInput(input)
				m.composer.Reset()
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			return m, tea.Batch(cmds...)

		case tea.KeyUp:
			if len(m.inputHistory) > 0 {
				if m.historyIdx < len(m.inputHistory)-1 {
					m.historyIdx++
				}
				m.composer.SetValue(m.inputHistory[m.historyIdx])
				return m, nil
			}

		case tea.KeyDown:
			if m.historyIdx > 0 {
				m.historyIdx--
				m.composer.SetValue(m.inputHistory[m.historyIdx])
			} else if m.historyIdx == 0 {
				m.historyIdx = -1
				m.composer.Reset()
			}
			return m, nil
		}

		// Pass to textarea
		var cmd tea.Cmd
		m.composer, cmd = m.composer.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}

	return m, nil
}

// View renders the full TUI layout.
func (m *Model) View() string {
	if !m.ready || m.width == 0 {
		return "Loading..."
	}

	if m.overlay != OverlayNone {
		return m.renderOverlay()
	}

	statusBar := m.renderStatusBar()
	composer := m.renderComposer()
	taskRail := m.renderTaskRail()

	// Calculate available height for transcript
	statusBarHeight := lipgloss.Height(statusBar)
	composerHeight := lipgloss.Height(composer)
	transcriptHeight := m.height - statusBarHeight - composerHeight
	if transcriptHeight < 1 {
		transcriptHeight = 1
	}

	// Calculate widths
	railWidth := 0
	if m.cfg.CLI.ShowTaskRail && m.width > 60 {
		railWidth = 30
	}
	mainWidth := m.width - railWidth

	transcriptContent := m.renderTranscript(mainWidth, transcriptHeight)

	// Combine main content
	var mainArea string
	if railWidth > 0 {
		rail := lipgloss.NewStyle().Width(railWidth).Height(transcriptHeight).Render(taskRail)
		main := lipgloss.NewStyle().Width(mainWidth).Height(transcriptHeight).Render(transcriptContent)
		mainArea = lipgloss.JoinHorizontal(lipgloss.Top, main, rail)
	} else {
		mainArea = lipgloss.NewStyle().Width(m.width).Height(transcriptHeight).Render(transcriptContent)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		statusBar,
		mainArea,
		composer,
	)
}

// --- Internal helpers ---

func (m *Model) recalcLayout() {
	railWidth := 0
	if m.cfg.CLI.ShowTaskRail && m.width > 60 {
		railWidth = 30
	}

	composerWidth := m.width - 2
	if composerWidth < 10 {
		composerWidth = 10
	}
	m.composer.SetWidth(composerWidth)

	vpWidth := m.width - railWidth
	vpHeight := m.height - 4 // status bar + composer
	if vpHeight < 1 {
		vpHeight = 1
	}
	m.viewport.Width = vpWidth
	m.viewport.Height = vpHeight
}

func (m *Model) handleStartupSummary() {
	summary, err := m.session.StartupSummary(m.projectID)
	if err != nil {
		m.log.Error("failed to get startup summary", "error", err)
		return
	}
	m.startup = summary
	m.ready = true
	m.mode = summary.Mode
	m.tasks = summary.Tasks
	m.budget = summary.Budget

	// Create or resume session
	sess, err := m.session.ResumeOrCreateSession(m.projectID)
	if err != nil {
		m.log.Error("failed to create/resume session", "error", err)
		return
	}
	m.sess = sess

	// Add startup action card to transcript
	m.appendTranscript("system", "system_card", summary.ActionCard)

	// Add suggestions
	suggestions := m.session.PromptSuggestions(m.projectID)
	if len(suggestions) > 0 {
		m.appendTranscript("system", "ephemeral", "Suggestions: "+strings.Join(suggestions, " | "))
	}
}

func (m *Model) handleEvent(ev events.EngineEvent) {
	switch ev.Type {
	case events.TaskProjectionUpdated:
		// Refresh task summary
		status, err := m.engine.GetRunStatus(m.projectID)
		if err == nil && status.Run != nil {
			m.tasks = status.Tasks
			m.budget = status.Budget
		}
	case events.SessionModeChanged:
		if modeStr, ok := ev.Details["mode"].(string); ok {
			m.mode = state.SessionMode(modeStr)
		}
	case events.ApprovalRequested:
		desc := "Approval requested"
		if d, ok := ev.Details["description"].(string); ok {
			desc = d
		}
		m.appendTranscript("system", "approval", desc)
	case events.DiffPreviewReady:
		m.appendTranscript("system", "event", "Diff preview available. Use /diff to view.")
	case events.SRSSubmitted:
		m.appendTranscript("system", "event",
			"SRS draft submitted by the orchestrator. Use /srs to review, then /approve or /reject.")
		m.refreshAfterStateChange()
	case events.SRSApproved:
		m.appendTranscript("system", "event", "SRS approved. Run transitioning to active.")
		m.refreshAfterStateChange()
	case events.SRSRejected:
		m.appendTranscript("system", "event", "SRS rejected. Run returned to draft_srs.")
		m.refreshAfterStateChange()
	case events.RunCreated:
		// RunCreated is emitted both by StartRun and CreateRun; the TUI
		// path triggers a refresh regardless so the status bar and action
		// card reflect the new run immediately. refreshAfterStateChange
		// is idempotent when the mode is unchanged.
		m.refreshAfterStateChange()
	default:
		// Log other events as collapsed entries
		if !events.IsViewModelEvent(ev.Type) {
			m.appendTranscript("system", "event", fmt.Sprintf("[%s]", ev.Type))
		}
	}
}

func (m *Model) listenForEvents() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.eventCh
		if !ok {
			return nil
		}
		return eventMsg(ev)
	}
}

func (m *Model) appendTranscript(role, kind, content string) {
	m.transcript = append(m.transcript, TranscriptEntry{
		Role:    role,
		Kind:    kind,
		Content: content,
	})

	// Persist to session if available
	if m.sess != nil {
		if _, err := m.session.AddTranscriptMessage(m.sess.ID, role, kind, content); err != nil {
			m.log.Warn("failed to persist transcript message", "error", err)
		}
	}
}

// submitInput dispatches an input line per Architecture §26.2.3:
//   - A leading `/` routes to the slash-command handler.
//   - A leading `!` echoes an honest "not yet routed" shell hint.
//   - Otherwise the input is mode-aware: in bootstrap it creates a run via
//     Engine.StartRun; in approval it nudges the operator toward /srs +
//     /approve/reject; in execution and postrun it explains the surface.
//
// This method is the single most important fix for Issue 08. Prior to this,
// regular input in bootstrap mode was silently swallowed into the transcript
// while the operator waited for an engine call that never came.
func (m *Model) submitInput(input string) tea.Cmd {
	if strings.HasPrefix(input, "/") {
		result := m.handleSlashCommand(input)
		if result != "" {
			m.appendTranscript("system", "system_card", result)
		}
		// Slash handlers may have returned an async command to execute.
		if cmd := m.pendingCmd; cmd != nil {
			m.pendingCmd = nil
			return cmd
		}
		return nil
	}

	if strings.HasPrefix(input, "!") {
		// Shell mode: display the command intent. Shell-mode execution is
		// explicitly deferred — the placeholder here is phrased as "not yet
		// routed" rather than "handled by the engine" so operators are not
		// misled into thinking their command ran.
		shellCmd := strings.TrimPrefix(input, "!")
		m.appendTranscript("user", "shell", "$ "+shellCmd)
		m.appendTranscript("system", "event", "Shell execution is not yet routed; use a separate terminal.")
		return nil
	}

	// Echo user message + record history (always, regardless of mode).
	m.appendTranscript("user", "user", input)
	if m.sess != nil {
		_ = m.session.RecordInput(m.projectID, m.sess.ID, "prompt", input)
	}
	m.inputHistory = append([]string{input}, m.inputHistory...)
	m.historyIdx = -1

	// Dispatch by mode — this is the §26.2.3 "route to engine subsystem"
	// contract. Previously this method only appended to the transcript,
	// which made the TUI misleading for non-technical operators (Issue 08).
	switch m.mode {
	case state.SessionBootstrap:
		// Guard against bare shell-form commands. A user who has been
		// typing `axiom srs show` on the CLI will naturally drop the
		// slash when moving into the TUI. The first submit shows a
		// "did you mean /<cmd>?" hint and buffers the prompt; the
		// second Enter treats the same text as a project prompt.
		if m.pendingShellLikePrompt == "" && looksLikeShellCommand(input) {
			first := strings.Fields(input)[0]
			m.pendingShellLikePrompt = input
			m.appendTranscript("system", "system_card",
				fmt.Sprintf(
					"Hmm, %q looks like a shell command. Did you mean /%s? "+
						"Press Enter again to treat it as a project prompt, "+
						"or type /%s to run the command.",
					input, first, first))
			return nil
		}
		// A second Enter on the same text confirms the bootstrap
		// prompt. Clear the buffer so subsequent inputs reset the
		// guard.
		m.pendingShellLikePrompt = ""
		return m.startRunFromPrompt(input)
	case state.SessionApproval:
		m.appendTranscript("system", "system_card",
			"To act on the SRS, use /srs to view the draft, then /approve or /reject \"feedback\".")
		return nil
	case state.SessionExecution:
		m.appendTranscript("system", "ephemeral",
			"User clarifications during execution are not yet routed to the orchestrator. "+
				"Use /status, /tasks, /pause, or /cancel to observe or control the run.")
		return nil
	case state.SessionPostrun:
		m.appendTranscript("system", "ephemeral",
			"The run is complete. Use /diff to review changes, or start a new run with /new \"<prompt>\".")
		return nil
	default:
		return nil
	}
}

// startRunFromPrompt returns a tea.Cmd that invokes Engine.StartRun on a
// background goroutine and delivers the outcome back via runStartedMsg /
// runStartFailedMsg. StartRun enforces the clean-tree contract internally;
// the TUI must NOT expose an --allow-dirty bypass — dirty-tree recovery
// remains a deliberately inconvenient CLI-only escape hatch (Issue 06).
func (m *Model) startRunFromPrompt(prompt string) tea.Cmd {
	return m.startRunFromPromptWithForce(prompt, false)
}

// startRunFromPromptWithForce is the Force-aware variant used by the
// /new confirmation gate. When force is true, StartRun will replace
// any existing in-flight run rather than returning
// ActiveRunExistsError. Bare bootstrap-mode prompts always pass
// force=false so a stray keystroke can never clobber an active run.
func (m *Model) startRunFromPromptWithForce(prompt string, force bool) tea.Cmd {
	return func() tea.Msg {
		run, err := m.engine.StartRun(engine.StartRunOptions{
			ProjectID:  m.projectID,
			Prompt:     prompt,
			BaseBranch: "main",
			Source:     "tui",
			Force:      force,
		})
		if err != nil {
			return runStartFailedMsg{err: err}
		}
		return runStartedMsg{run: run}
	}
}

// refreshAfterStateChange re-reads the startup summary and mode from the
// session manager after any handler that mutates run state. This keeps the
// status bar, action card, and task rail coherent with the underlying
// engine state without requiring each handler to duplicate the refresh
// logic. If the mode has not changed, the handler skips re-appending an
// action card entry to avoid cosmetic double-rendering (see Issue 08 §6.1).
func (m *Model) refreshAfterStateChange() {
	previousMode := m.mode
	summary, err := m.session.StartupSummary(m.projectID)
	if err != nil {
		m.log.Warn("failed to refresh startup summary after state change", "error", err)
		m.mode = m.session.DetermineMode(m.projectID)
		return
	}
	m.startup = summary
	m.mode = summary.Mode
	m.tasks = summary.Tasks
	m.budget = summary.Budget
	if previousMode != m.mode {
		m.appendTranscript("system", "system_card", summary.ActionCard)
	}
}

// handleSlashCommand processes a slash command and returns response text.
// Handlers may also set m.pendingCmd to emit an asynchronous tea.Cmd.
//
// Previously five of the twelve declared commands (/new, /resume, /eco,
// /diff, /theme) returned canned sentences without touching the engine.
// The Issue 08 fix wires all of them (except /theme, which is removed).
func (m *Model) handleSlashCommand(cmd string) string {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return ""
	}
	command := strings.ToLower(parts[0])
	args := parts[1:]

	switch command {
	case "/status":
		return m.cmdStatus()
	case "/help":
		return m.cmdHelp()
	case "/tasks":
		return m.cmdTasks()
	case "/budget":
		return m.cmdBudget()
	case "/clear":
		m.transcript = nil
		return ""
	case "/new":
		return m.cmdNewRun(cmd, args)
	case "/resume":
		return m.cmdResumeRun()
	case "/pause":
		return m.cmdPause()
	case "/cancel":
		return m.cmdCancel()
	case "/srs":
		return m.cmdSRS()
	case "/approve":
		return m.cmdApproveSRS()
	case "/reject":
		// /reject preserves inline quoting. Re-derive the raw argument
		// string from the original command so quoted feedback like
		// /reject "needs section 4.2" is passed through verbatim.
		return m.cmdRejectSRS(rawArgs(cmd, "/reject"))
	case "/eco":
		return m.cmdECO()
	case "/diff":
		return m.cmdDiff()
	default:
		return fmt.Sprintf("Unknown command: %s. Type /help for available commands.", command)
	}
}

// rawArgs returns the argument portion of a slash command line with the
// prefix trimmed and surrounding whitespace normalized. It preserves
// embedded whitespace inside quoted arguments, unlike strings.Fields.
func rawArgs(line, prefix string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(strings.ToLower(trimmed), prefix) {
		return ""
	}
	return strings.TrimSpace(trimmed[len(prefix):])
}

func (m *Model) cmdStatus() string {
	status, err := m.engine.GetRunStatus(m.projectID)
	if err != nil {
		return fmt.Sprintf("Error getting status: %v", err)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Project: %s\n", status.ProjectName))
	b.WriteString(fmt.Sprintf("Root:    %s\n", status.RootDir))
	b.WriteString(fmt.Sprintf("Mode:    %s\n", m.mode))

	if status.Run != nil {
		b.WriteString(fmt.Sprintf("Run:     %s (%s)\n", status.Run.ID[:8], status.Run.Status))
		b.WriteString(fmt.Sprintf("Branch:  %s\n", status.Run.WorkBranch))
		b.WriteString(fmt.Sprintf("Budget:  $%.2f / $%.2f", status.Budget.SpentUSD, status.Budget.MaxUSD))
		if status.Budget.WarnReached {
			b.WriteString(fmt.Sprintf(" [WARN: %d%%]", status.Budget.WarnPercent))
		}
		b.WriteString("\n")
		if status.Tasks.Total > 0 {
			b.WriteString(fmt.Sprintf("Tasks:   %d total, %d done, %d running, %d queued",
				status.Tasks.Total, status.Tasks.Done, status.Tasks.InProgress, status.Tasks.Queued))
		}
	} else {
		b.WriteString("Run:     idle (no active run)\n")
		b.WriteString(fmt.Sprintf("Budget:  $%.2f configured", m.cfg.Budget.MaxUSD))
	}

	return b.String()
}

func (m *Model) cmdHelp() string {
	return `Available commands:

  Bootstrap:
    /new "<prompt>"  — Start a new run (or type the prompt directly)

  Approval:
    /srs             — View the SRS draft
    /approve         — Approve the SRS draft
    /reject "<fb>"   — Reject the SRS draft with feedback

  Execution:
    /tasks           — Show task list
    /diff            — Preview changes on the work branch
    /budget          — Show budget details
    /pause           — Pause active execution
    /cancel          — Cancel active execution

  Postrun:
    /diff            — Review final changes
    /resume          — Resume a paused run
    /new "<prompt>"  — Start a new run

  Always:
    /status          — Show project status
    /eco             — List ECOs for the active run
    /help            — Show this help
    /clear           — Clear transcript

Shortcuts:
  /  — Slash command
  !  — Shell mode (not yet routed; use a separate terminal)
  Ctrl+C — Quit`
}

func (m *Model) cmdTasks() string {
	if m.tasks.Total == 0 {
		return "No tasks. Start a run with 'axiom run \"<prompt>\"'."
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Tasks: %d total\n", m.tasks.Total))
	if m.tasks.Done > 0 {
		b.WriteString(fmt.Sprintf("  Done:        %d\n", m.tasks.Done))
	}
	if m.tasks.InProgress > 0 {
		b.WriteString(fmt.Sprintf("  In progress: %d\n", m.tasks.InProgress))
	}
	if m.tasks.Queued > 0 {
		b.WriteString(fmt.Sprintf("  Queued:      %d\n", m.tasks.Queued))
	}
	if m.tasks.WaitingLock > 0 {
		b.WriteString(fmt.Sprintf("  Waiting:     %d\n", m.tasks.WaitingLock))
	}
	if m.tasks.Failed > 0 {
		b.WriteString(fmt.Sprintf("  Failed:      %d\n", m.tasks.Failed))
	}
	if m.tasks.Blocked > 0 {
		b.WriteString(fmt.Sprintf("  Blocked:     %d\n", m.tasks.Blocked))
	}
	return b.String()
}

func (m *Model) cmdBudget() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Budget: $%.2f / $%.2f\n", m.budget.SpentUSD, m.budget.MaxUSD))
	b.WriteString(fmt.Sprintf("Remaining: $%.2f\n", m.budget.RemainingUSD))
	if m.budget.WarnReached {
		b.WriteString(fmt.Sprintf("WARNING: %d%% threshold reached", m.budget.WarnPercent))
	}
	return b.String()
}

func (m *Model) cmdPause() string {
	if m.mode != state.SessionExecution {
		return "No active run to pause."
	}
	status, err := m.engine.GetRunStatus(m.projectID)
	if err != nil || status.Run == nil {
		return "No active run to pause."
	}
	if err := m.engine.PauseRun(status.Run.ID); err != nil {
		return fmt.Sprintf("Failed to pause: %v", err)
	}
	m.mode = state.SessionExecution
	return "Run paused. Use /resume or 'axiom resume' to continue."
}

func (m *Model) cmdCancel() string {
	if m.mode != state.SessionExecution {
		return "No active run to cancel."
	}
	status, err := m.engine.GetRunStatus(m.projectID)
	if err != nil || status.Run == nil {
		return "No active run to cancel."
	}
	if err := m.engine.CancelRun(status.Run.ID); err != nil {
		return fmt.Sprintf("Failed to cancel: %v", err)
	}
	m.mode = state.SessionPostrun
	return "Run cancelled."
}

// cmdSRS returns a view of the SRS state. Unlike the previous
// placeholder that returned a status sentence, this handler actually
// reads the draft from disk (awaiting-approval case) or the finalized
// SRS (active/paused case), so operators can review the content
// without leaving the TUI. Full overlay rendering is deferred.
func (m *Model) cmdSRS() string {
	run, err := m.engine.DB().GetActiveRun(m.projectID)
	if err != nil {
		return "No active run. Use /new \"<prompt>\" to start one."
	}

	switch run.Status {
	case state.RunDraftSRS:
		// Some orchestrators write the draft file early; prefer real content
		// if present, otherwise report the waiting state honestly.
		draft, readErr := m.engine.ReadSRSDraft(run.ID)
		if readErr != nil {
			return "Run " + run.ID[:8] + " is in draft_srs. " +
				"Waiting for external orchestrator to submit an SRS draft."
		}
		return "--- SRS Draft (not yet submitted for approval) ---\n" + draft

	case state.RunAwaitingSRSApproval:
		draft, readErr := m.engine.ReadSRSDraft(run.ID)
		if readErr != nil {
			return "SRS draft is awaiting approval but the draft file was not found. " +
				"Check .axiom/drafts/ or use 'axiom srs show' for diagnostics."
		}
		return "--- SRS Draft (awaiting approval) ---\n" + draft +
			"\n\nUse /approve or /reject \"<feedback>\" to decide."

	case state.RunActive, state.RunPaused:
		// Read the finalized SRS if it exists on disk.
		data, readErr := os.ReadFile(filepath.Join(m.engine.RootDir(), ".axiom", "srs.md"))
		if readErr != nil {
			return "SRS has been approved and is locked (file not readable here)."
		}
		return "--- Approved SRS ---\n" + string(data)

	default:
		return "No SRS available for run status " + string(run.Status) + "."
	}
}

// cmdNewRun handles /new. Two forms are supported:
//
//  1. /new <prompt words...>  — treats the remainder after "/new " as the
//     prompt and triggers StartRun. This mirrors the §26.2.6 expectation
//     that /new maps to a bootstrap start.
//  2. /new (bare)             — emits a hint telling the operator to type
//     their prompt on the next line. This keeps the dual-form UX that
//     some operators will expect (type /new first, then the prompt).
//
// If a run is already in progress, /new requires an explicit two-step
// confirmation (first /new arms the replacement gate, second /new with
// a prompt actually passes Force=true to StartRun). This protects
// in-flight orchestrator work from stray keystrokes.
func (m *Model) cmdNewRun(cmd string, args []string) string {
	inProgress := m.mode != state.SessionBootstrap

	// First-touch confirmation gate. A stray /new while a run is in
	// progress arms the gate; the next /new with a prompt actually
	// replaces the run.
	if inProgress && !m.pendingNewRunConfirm {
		m.pendingNewRunConfirm = true
		return "A run is already in progress. Re-issue /new \"<prompt>\" to confirm replacement, " +
			"or use /cancel to abandon it first. Use 'axiom export' to audit the prior run's state before replacing."
	}

	if len(args) == 0 {
		return "Type your prompt below and press Enter, or use /new \"<prompt>\" to submit inline."
	}
	prompt := strings.TrimSpace(rawArgs(cmd, "/new"))
	// Operators may quote the prompt — strip surrounding quotes so the
	// engine receives the human-readable text.
	prompt = strings.TrimSpace(strings.Trim(prompt, "\""))
	if prompt == "" {
		return "Type your prompt below and press Enter, or use /new \"<prompt>\" to submit inline."
	}
	// Echo the operator's intent into the transcript for consistency with
	// the regular-text path, then defer the engine call to the async Cmd.
	m.appendTranscript("user", "user", prompt)
	if m.sess != nil {
		_ = m.session.RecordInput(m.projectID, m.sess.ID, "prompt", prompt)
	}
	force := inProgress && m.pendingNewRunConfirm
	m.pendingNewRunConfirm = false
	m.pendingCmd = m.startRunFromPromptWithForce(prompt, force)
	return ""
}

// cmdResumeRun handles /resume. It mirrors `axiom resume` at
// internal/cli/run.go and does NOT implement cross-session resume — that
// is `axiom session resume <id>` and lives outside the TUI scope.
func (m *Model) cmdResumeRun() string {
	run, err := m.engine.DB().GetLatestRunByProject(m.projectID)
	if err != nil {
		return "No runs found for this project. Use /new \"<prompt>\" to start one."
	}
	if run.Status != state.RunPaused {
		return fmt.Sprintf("No paused run to resume. Latest run is %s (%s).",
			run.ID[:8], run.Status)
	}
	if err := m.engine.ResumeRun(run.ID); err != nil {
		return fmt.Sprintf("Failed to resume run: %v", err)
	}
	m.refreshAfterStateChange()
	return fmt.Sprintf("Run %s resumed.", run.ID[:8])
}

// cmdApproveSRS handles /approve. The run must be in
// awaiting_srs_approval — any other status is an error we surface rather
// than silently ignore.
func (m *Model) cmdApproveSRS() string {
	run, err := m.engine.DB().GetActiveRun(m.projectID)
	if err != nil {
		return "No active run to approve."
	}
	if run.Status != state.RunAwaitingSRSApproval {
		return fmt.Sprintf("Cannot approve: run is in %s (must be awaiting_srs_approval).",
			run.Status)
	}
	if err := m.engine.ApproveSRS(run.ID); err != nil {
		return fmt.Sprintf("Failed to approve SRS: %v", err)
	}
	m.refreshAfterStateChange()
	return fmt.Sprintf("SRS approved. Run %s is now active.", run.ID[:8])
}

// cmdRejectSRS handles /reject "<feedback>". The feedback is required
// and must be non-empty — otherwise the handler emits a usage error
// instead of transitioning the run.
func (m *Model) cmdRejectSRS(rawArg string) string {
	feedback := strings.TrimSpace(strings.Trim(rawArg, "\""))
	if feedback == "" {
		return "Reject requires feedback. Usage: /reject \"Your feedback here\""
	}
	run, err := m.engine.DB().GetActiveRun(m.projectID)
	if err != nil {
		return "No active run to reject."
	}
	if run.Status != state.RunAwaitingSRSApproval {
		return fmt.Sprintf("Cannot reject: run is in %s (must be awaiting_srs_approval).",
			run.Status)
	}
	if err := m.engine.RejectSRS(run.ID, feedback); err != nil {
		return fmt.Sprintf("Failed to reject SRS: %v", err)
	}
	m.refreshAfterStateChange()
	return fmt.Sprintf("SRS rejected. Run %s returned to draft_srs for revision.", run.ID[:8])
}

// cmdECO lists ECOs for the active run. ECO approval/rejection is
// explicitly deferred — identity tracking for approvedBy is out of scope
// per Issue 08 §4.5; the CLI pointer in the last line makes the CLI path
// discoverable without pretending the TUI already supports it.
func (m *Model) cmdECO() string {
	run, err := m.engine.DB().GetActiveRun(m.projectID)
	if err != nil {
		return "No active run. ECOs are scoped to an active or paused run."
	}
	entries, err := m.engine.DB().ListECOsByRun(run.ID)
	if err != nil {
		return fmt.Sprintf("Failed to list ECOs: %v", err)
	}
	if len(entries) == 0 {
		return fmt.Sprintf("No ECOs proposed for run %s.", run.ID[:8])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ECOs for run %s:\n", run.ID[:8])
	for _, e := range entries {
		fmt.Fprintf(&b, "  %s  %s  %s\n", e.ECOCode, e.Status, e.Category)
	}
	b.WriteString("\nUse 'axiom eco approve <code>' or 'axiom eco reject <code>' from the CLI to decide.")
	return b.String()
}

// cmdDiff computes `git diff <base>...<head>` for the active run and
// returns a truncated preview. Full diff overlay rendering is deferred.
func (m *Model) cmdDiff() string {
	run, err := m.engine.DB().GetActiveRun(m.projectID)
	if err != nil {
		return "No active run. Use /new \"<prompt>\" to start one."
	}
	git := m.engine.Git()
	if git == nil {
		return "Git service is not wired in this engine build."
	}
	diff, err := git.DiffRange(m.engine.RootDir(), run.BaseBranch, run.WorkBranch)
	if err != nil {
		return fmt.Sprintf("Failed to compute diff: %v", err)
	}
	if diff == "" {
		return fmt.Sprintf("No diff between %s and %s.", run.BaseBranch, run.WorkBranch)
	}
	const maxBytes = 4096
	if len(diff) > maxBytes {
		return fmt.Sprintf("--- Diff %s..%s (truncated) ---\n%s\n… (%d more bytes — use the diff overlay for the full view)",
			run.BaseBranch, run.WorkBranch, diff[:maxBytes], len(diff)-maxBytes)
	}
	return fmt.Sprintf("--- Diff %s..%s ---\n%s", run.BaseBranch, run.WorkBranch, diff)
}

// --- Rendering ---

func (m *Model) renderStatusBar() string {
	left := m.theme.StatusBar.Render(
		fmt.Sprintf(" %s ", m.startup.ProjectName),
	)

	modeLabel := m.theme.ModeLabel.Render(
		fmt.Sprintf(" %s ", strings.ToUpper(string(m.mode))),
	)

	var rightParts []string
	if m.startup.Branch != "" {
		rightParts = append(rightParts, m.startup.Branch)
	}
	if m.startup.RunStatus != "" {
		rightParts = append(rightParts, m.startup.RunStatus)
	}
	rightParts = append(rightParts, fmt.Sprintf("$%.2f/$%.2f", m.budget.SpentUSD, m.budget.MaxUSD))

	right := m.theme.StatusBar.Render(strings.Join(rightParts, " | "))

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(modeLabel) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}

	return m.theme.StatusBar.Width(m.width).Render(
		lipgloss.JoinHorizontal(lipgloss.Top,
			left,
			modeLabel,
			strings.Repeat(" ", gap),
			right,
		),
	)
}

func (m *Model) renderTranscript(width, height int) string {
	if len(m.transcript) == 0 {
		return m.theme.ActionCard.Width(width - 4).Render(m.startup.ActionCard)
	}

	var lines []string
	for _, entry := range m.transcript {
		prefix := ""
		style := m.theme.Primary
		switch entry.Role {
		case "user":
			prefix = "> "
			style = m.theme.Accent
		case "assistant":
			prefix = "  "
			style = m.theme.Primary
		case "system":
			switch entry.Kind {
			case "system_card":
				prefix = "  "
				style = m.theme.Secondary
			case "approval":
				prefix = "! "
				style = m.theme.Warning
			case "event":
				prefix = "  "
				style = m.theme.Muted
			case "ephemeral":
				prefix = "  "
				style = m.theme.Muted
			default:
				prefix = "  "
				style = m.theme.Muted
			}
		}
		lines = append(lines, style.Render(prefix+entry.Content))
	}

	content := strings.Join(lines, "\n")

	// If content exceeds height, show only the last `height` lines
	rendered := strings.Split(content, "\n")
	if len(rendered) > height {
		rendered = rendered[len(rendered)-height:]
	}
	return strings.Join(rendered, "\n")
}

func (m *Model) renderTaskRail() string {
	var b strings.Builder
	b.WriteString(m.theme.Accent.Bold(true).Render("Tasks"))
	b.WriteString("\n")

	if m.tasks.Total == 0 {
		b.WriteString(m.theme.Muted.Render("No tasks"))
		return m.theme.TaskRail.Render(b.String())
	}

	items := []struct {
		label string
		count int
		style lipgloss.Style
	}{
		{"Done", m.tasks.Done, m.theme.Success},
		{"Running", m.tasks.InProgress, m.theme.Accent},
		{"Queued", m.tasks.Queued, m.theme.Secondary},
		{"Waiting", m.tasks.WaitingLock, m.theme.Muted},
		{"Failed", m.tasks.Failed, m.theme.Error},
		{"Blocked", m.tasks.Blocked, m.theme.Warning},
	}

	for _, item := range items {
		if item.count > 0 {
			b.WriteString(item.style.Render(fmt.Sprintf("  %s: %d", item.label, item.count)))
			b.WriteString("\n")
		}
	}

	b.WriteString(m.theme.Separator.Render(strings.Repeat("─", 26)))
	b.WriteString("\n")

	// Budget summary
	b.WriteString(m.theme.Muted.Render(fmt.Sprintf("  $%.2f/$%.2f", m.budget.SpentUSD, m.budget.MaxUSD)))

	return m.theme.TaskRail.Render(b.String())
}

func (m *Model) renderComposer() string {
	modeIndicator := m.theme.Muted.Render(fmt.Sprintf("[%s]", m.mode))
	hint := m.theme.Muted.Render("/ commands  ! shell  @ mention")

	composerView := m.composer.View()

	return m.theme.Composer.Width(m.width - 2).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			composerView,
			lipgloss.JoinHorizontal(lipgloss.Top, modeIndicator, "  ", hint),
		),
	)
}

func (m *Model) updateOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.overlay = OverlayNone
	}
	return m, nil
}

func (m *Model) renderOverlay() string {
	switch m.overlay {
	case OverlayHelp:
		content := m.cmdHelp()
		return m.theme.Overlay.Width(m.width - 10).Render(content)
	default:
		// Unimplemented overlay — render base view without recursion
		saved := m.overlay
		m.overlay = OverlayNone
		v := m.View()
		m.overlay = saved
		return v
	}
}
