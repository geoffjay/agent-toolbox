package ui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
)

// reportRenderedMsg carries the glamour-rendered report back into the
// model. raw holds the unrendered markdown so a rendering failure can
// degrade to plain text instead of losing the report.
type reportRenderedMsg struct {
	content string
	raw     string
	err     error
}

// renderReport renders markdown through glamour in a tea.Cmd so a large
// report never blocks the render loop. The style adapts to the terminal
// background color and the word wrap to the viewport width.
func renderReport(report string, width int, dark bool) tea.Cmd {
	return func() tea.Msg {
		style := "dark"
		if !dark {
			style = "light"
		}
		if width < 20 {
			width = 80
		}
		r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle(style),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return reportRenderedMsg{raw: report, err: err}
		}
		out, err := r.Render(report)
		if err != nil {
			return reportRenderedMsg{raw: report, err: err}
		}
		return reportRenderedMsg{content: out, raw: report}
	}
}
