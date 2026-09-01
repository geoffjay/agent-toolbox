// Package ui presents agent-toolbox runs in a bubbletea terminal user
// interface: streaming agent output, progress spinners, human-in-the-loop
// forms, and the final rendered markdown report.
package ui

import "charm.land/lipgloss/v2"

// Theme holds the lipgloss styles used across the interface. Keeping them
// in one place makes the palette adjustable and keeps the view code free
// of inline styling.
type Theme struct {
	// Brand styles the application title in the header.
	Brand lipgloss.Style

	// Label styles the run label next to the title (dim).
	Label lipgloss.Style

	// Status styles the activity text beside the spinner.
	Status lipgloss.Style

	// Stream styles streaming agent output: light gray so live model
	// chatter reads as background text rather than final results.
	Stream lipgloss.Style

	// Agent marks a new agent's section in the streaming view.
	Agent lipgloss.Style

	// OK styles success status lines.
	OK lipgloss.Style

	// Warn styles warning blocks (shallow review, diagnostics).
	Warn lipgloss.Style

	// Error styles failure status lines.
	Error lipgloss.Style

	// Note styles post-run informational notes.
	Note lipgloss.Style

	// Help styles the footer key hints.
	Help lipgloss.Style
}

// NewTheme returns the default theme.
func NewTheme() Theme {
	return Theme{
		Brand: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#8f5ed7")),
		Label: lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")),
		Status: lipgloss.NewStyle().
			Foreground(lipgloss.Color("249")),
		Stream: lipgloss.NewStyle().
			Foreground(lipgloss.Color("247")),
		Agent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")),
		OK: lipgloss.NewStyle().
			Foreground(lipgloss.Color("42")),
		Warn: lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")),
		Error: lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")),
		Note: lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")),
		Help: lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")),
	}
}
