package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// NewProgram creates a new Bubble Tea program for the TUI.
// This wraps tea.NewProgram with Axiom's preferred options:
// alternate screen buffer, mouse support, and window title.
func NewProgram(m *Model) *tea.Program {
	return tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
}
