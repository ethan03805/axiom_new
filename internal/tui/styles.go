// Package tui implements the Bubble Tea interactive terminal UI (Architecture Section 26.2).
// The TUI runs as a thin client over engine-emitted view models.
// It does NOT read SQLite directly — all state comes from the engine.
package tui

import "github.com/charmbracelet/lipgloss"

// Theme defines the visual theme for the TUI.
type Theme struct {
	Primary      lipgloss.Style
	Secondary    lipgloss.Style
	Accent       lipgloss.Style
	Muted        lipgloss.Style
	Error        lipgloss.Style
	Warning      lipgloss.Style
	Success      lipgloss.Style
	StatusBar    lipgloss.Style
	TaskRail     lipgloss.Style
	Composer     lipgloss.Style
	Overlay      lipgloss.Style
	ActionCard   lipgloss.Style
	ModeLabel    lipgloss.Style
	Separator    lipgloss.Style
}

// DefaultTheme returns the default Axiom theme.
func DefaultTheme() Theme {
	return Theme{
		Primary: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")),
		Secondary: lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")),
		Accent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")),
		Muted: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")),
		Error: lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")),
		Warning: lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")),
		Success: lipgloss.NewStyle().
			Foreground(lipgloss.Color("46")),
		StatusBar: lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("15")).
			Padding(0, 1),
		TaskRail: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color("8")).
			Padding(0, 1).
			Width(30),
		Composer: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(lipgloss.Color("8")).
			Padding(0, 1),
		Overlay: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("39")).
			Padding(1, 2),
		ActionCard: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("39")).
			Padding(0, 1).
			MarginTop(1).
			MarginBottom(1),
		ModeLabel: lipgloss.NewStyle().
			Background(lipgloss.Color("39")).
			Foreground(lipgloss.Color("15")).
			Padding(0, 1).
			Bold(true),
		Separator: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")),
	}
}
