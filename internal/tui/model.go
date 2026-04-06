package tui

import (
	"fmt"
	"log/slog"
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

	ready bool
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

func (m *Model) submitInput(input string) tea.Cmd {
	if strings.HasPrefix(input, "/") {
		result := m.handleSlashCommand(input)
		if result != "" {
			m.appendTranscript("system", "system_card", result)
		}
		return nil
	}

	if strings.HasPrefix(input, "!") {
		// Shell mode: display the command intent
		shellCmd := strings.TrimPrefix(input, "!")
		m.appendTranscript("user", "shell", "$ "+shellCmd)
		m.appendTranscript("system", "event", "Shell execution is handled by the engine.")
		return nil
	}

	// Regular user message
	m.appendTranscript("user", "user", input)

	// Record in input history
	if m.sess != nil {
		_ = m.session.RecordInput(m.projectID, m.sess.ID, "prompt", input)
	}

	// Prepend to local history for up-arrow recall
	m.inputHistory = append([]string{input}, m.inputHistory...)
	m.historyIdx = -1

	return nil
}

// handleSlashCommand processes a slash command and returns response text.
func (m *Model) handleSlashCommand(cmd string) string {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return ""
	}
	command := strings.ToLower(parts[0])

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
		return "Start a new session by describing what you want to build."
	case "/resume":
		return "Use 'axiom session resume <id>' to resume a specific session."
	case "/pause":
		return m.cmdPause()
	case "/cancel":
		return m.cmdCancel()
	case "/srs":
		return m.cmdSRS()
	case "/eco":
		return "ECO review: No pending ECOs."
	case "/diff":
		return "No diffs available. Run tasks to generate changes."
	case "/theme":
		return "Theme switching is not yet available."
	default:
		return fmt.Sprintf("Unknown command: %s. Type /help for available commands.", command)
	}
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
  /status  — Show project status and resources
  /tasks   — Show task list
  /budget  — Show budget details
  /pause   — Pause active execution
  /cancel  — Cancel active execution
  /srs     — View SRS
  /eco     — View ECOs
  /diff    — Preview latest changes
  /new     — Start a new bootstrap session
  /resume  — Resume an existing session
  /clear   — Clear transcript
  /theme   — Switch display theme
  /help    — Show this help

Shortcuts:
  /  — Open slash command palette
  !  — Enter shell mode
  @  — File mention autocomplete
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

func (m *Model) cmdSRS() string {
	switch m.mode {
	case state.SessionApproval:
		return "SRS is awaiting your review. Approve or reject."
	case state.SessionExecution, state.SessionPostrun:
		return "SRS has been approved and is locked."
	default:
		return "No SRS yet. Start a run to generate one."
	}
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
