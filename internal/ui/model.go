package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/geoffjay/graph-review/internal/agents"
)

// formKind discriminates which interaction an embedded huh form serves.
type formKind int

const (
	formNone formKind = iota
	formGate
	formConfirm
)

// chromeRows is the number of layout rows reserved outside the viewport:
// header, status line, and footer.
const chromeRows = 3

// formState carries the values of the embedded huh forms. It is
// heap-allocated and shared by pointer so that field bindings survive the
// value-copy semantics of tea.Model.Update.
type formState struct {
	decision string
	feedback string
	confirm  bool
}

// model is the bubbletea model for a whole review run.
type model struct {
	theme Theme

	width, height int
	dark          bool // terminal background, for glamour styling

	spinner  spinner.Model
	viewport viewport.Model

	label    string // run label shown in the header
	activity string // current pipeline activity shown beside the spinner
	busy     bool   // spinner running

	streaming *strings.Builder // styled streamed agent output (heap: the model is copied)
	lastAgent string           // agent whose text streamed last

	report   string // final report markdown
	rendered string // glamour-rendered report

	warns []string
	notes []string

	fs *formState

	form         *huh.Form
	formKind     formKind
	gateReply    chan map[string]any
	confirmReply chan bool

	done     bool  // report (or empty finish) delivered
	userQuit bool  // user pressed q/ctrl+c
	failed   error // terminal failure
}

func newModel() model {
	t := NewTheme()
	sp := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(t.Status),
	)
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(24))
	vp.SoftWrap = true

	return model{
		theme:     t,
		spinner:   sp,
		viewport:  vp,
		activity:  "starting",
		busy:      true,
		streaming: &strings.Builder{},
		fs:        &formState{},
	}
}

// Init starts the spinner animation and requests the terminal background
// color so the report renderer can pick a light or dark glamour style.
func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tea.RequestBackgroundColor)
}

// Update implements tea.Model.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		if m.done && m.report != "" {
			return m, renderReport(m.report, m.width, m.dark)
		}
		return m, nil

	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()

	case tea.KeyPressMsg:
		if m.form == nil {
			switch msg.String() {
			case "q", "ctrl+c":
				m.userQuit = true
				return m, tea.Quit
			}
		}

	case spinner.TickMsg:
		if m.form != nil || !m.busy {
			return m, nil // freeze the spinner while paused or finished
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case startMsg:
		m.label = msg.label

	case activityMsg:
		m.activity = msg.text
		m.busy = true

	case streamMsg:
		m.appendStream(msg.agent, msg.text)

	case warnMsg:
		m.warns = append(m.warns, msg.text)
		m.refreshViewport()

	case noteMsg:
		m.notes = append(m.notes, msg.text)
		m.busy = false

	case finishMsg:
		m.report = msg.report
		m.done = true
		m.busy = false
		if strings.TrimSpace(msg.report) == "" {
			return m, nil // nothing to render; keep the streaming view
		}
		return m, renderReport(m.report, m.width, m.dark)

	case reportRenderedMsg:
		if msg.err != nil {
			// Rendering is cosmetic; fall back to the raw markdown.
			m.rendered = msg.raw
		} else {
			m.rendered = msg.content
		}
		m.refreshViewport()
		m.viewport.GotoTop()
		return m, nil

	case failMsg:
		m.failed = msg.err
		m.busy = false

	case gateMsg:
		m.openForm(msg)
		return m, m.form.Init()

	case confirmMsg:
		m.openForm(msg)
		return m, m.form.Init()
	}

	// While a form is open it owns the keyboard: forward every message.
	if m.form != nil {
		_, cmd := m.form.Update(msg)
		if m.form.State != huh.StateNormal {
			return m.closeForm()
		}
		m.layout()
		return m, cmd
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// View implements tea.Model.
func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "graph-review"
	return v
}

func (m model) render() string {
	if m.width == 0 {
		return "starting…"
	}

	header := lipgloss.JoinHorizontal(lipgloss.Center,
		m.theme.Brand.Render(" graph-review "),
		m.theme.Label.Render(truncate(m.label, m.width-14)))

	var status string
	switch {
	case m.form != nil:
		status = m.theme.Note.Render("● waiting for your decision")
	case m.failed != nil:
		status = m.theme.Error.Render("✗ " + truncate(m.failed.Error(), m.width-2))
	case m.done && !m.busy:
		if strings.TrimSpace(m.report) == "" {
			status = m.theme.Warn.Render("⚠ review complete — no report produced")
		} else {
			status = m.theme.OK.Render("✓ review complete")
		}
		for _, n := range m.notes {
			status += "  " + m.theme.Note.Render(truncate(n, max(m.width-22-lipgloss.Width(status), 8)))
		}
	default:
		status = m.spinner.View() + " " + m.theme.Status.Render(truncate(m.activity, m.width-4))
	}

	body := m.viewport.View()

	if m.form != nil {
		return lipgloss.JoinVertical(lipgloss.Left, header, status, body, m.form.View())
	}

	help := m.theme.Help.Render("↑/↓ scroll · q quit")
	return lipgloss.JoinVertical(lipgloss.Left, header, status, body, help)
}

// layout sizes the viewport for the current window and form. It must be
// called after any change that affects the reserved rows.
func (m *model) layout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	h := m.height - chromeRows
	if m.form != nil {
		if fh := lipgloss.Height(m.form.View()); fh > 0 {
			h -= fh + 1
		}
	}
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(max(h, 0))
}

// appendStream adds one streamed chunk to the view in light gray, with a
// separator when the writing agent changes.
func (m *model) appendStream(agent, text string) {
	if agent != "" && agent != m.lastAgent {
		if m.streaming.Len() > 0 {
			m.streaming.WriteString("\n\n")
		}
		m.streaming.WriteString(agentSeparator(m.theme, agent, m.viewport.Width()))
		m.lastAgent = agent
	}
	m.streaming.WriteString(m.theme.Stream.Render(text))
	m.refreshViewport()
}

// refreshViewport pushes the current content into the viewport — the
// rendered report once finished, the streaming view otherwise, with any
// warnings appended — sticking to the bottom while output streams.
func (m *model) refreshViewport() {
	stick := m.viewport.AtBottom()
	var b strings.Builder
	if m.done && m.report != "" {
		b.WriteString(m.rendered)
	} else {
		b.WriteString(m.streaming.String())
	}
	for _, w := range m.warns {
		b.WriteString("\n\n")
		b.WriteString(m.theme.Warn.Render(indent(w)))
	}
	m.viewport.SetContent(b.String())
	if stick {
		m.viewport.GotoBottom()
	}
}

// openForm builds the huh form for a gate pause or confirmation and sizes
// it to the current window.
func (m *model) openForm(msg tea.Msg) {
	switch msg := msg.(type) {
	case gateMsg:
		m.formKind = formGate
		m.gateReply = msg.reply
		m.fs.decision = agents.DecisionApprove
		m.fs.feedback = ""
		m.form = huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(msg.req.Message).
					Options(
						huh.NewOption("Approve — accept the findings and continue to the summary", agents.DecisionApprove),
						huh.NewOption("Revise — send the findings back with feedback", agents.DecisionRevise),
						huh.NewOption("Abort — stop the review", agents.DecisionAbort),
					).
					Value(&m.fs.decision),
			),
			huh.NewGroup(
				huh.NewText().
					Title("Feedback for the reviewers").
					Description("what should change in the findings?").
					Lines(5).
					Value(&m.fs.feedback).
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return fmt.Errorf("revise requires feedback")
						}
						return nil
					}),
			).WithHideFunc(func() bool { return m.fs.decision != agents.DecisionRevise }),
		)
	case confirmMsg:
		m.formKind = formConfirm
		m.confirmReply = msg.reply
		m.fs.confirm = false
		m.form = huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(msg.c.Title).
					Description(msg.c.Detail).
					Affirmative("Post").
					Negative("Don't post").
					Value(&m.fs.confirm),
			),
		)
	default:
		return
	}

	if m.width > 0 {
		m.form.WithWidth(min(m.width-2, 78))
		m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	}
	m.layout()
}

// closeForm resolves the completed (or aborted) form and hands the answer
// back to the pipeline goroutine.
func (m model) closeForm() (tea.Model, tea.Cmd) {
	aborted := m.form.State == huh.StateAborted
	switch m.formKind {
	case formGate:
		reply := map[string]any{"decision": agents.DecisionAbort}
		if !aborted {
			reply = map[string]any{"decision": m.fs.decision}
			if m.fs.decision == agents.DecisionRevise {
				reply["feedback"] = m.fs.feedback
			}
		}
		m.gateReply <- reply
	case formConfirm:
		m.confirmReply <- !aborted && m.fs.confirm
	default:
		// formNone: no form is open, so there is nothing to resolve.
		return m, nil
	}
	m.form = nil
	m.formKind = formNone
	m.layout()
	return m, nil
}

// agentSeparator renders a rule line announcing an agent's section.
func agentSeparator(t Theme, agent string, width int) string {
	label := " " + agent + " "
	rule := strings.Repeat("─", max(width-lipgloss.Width(label)-2, 3))
	return t.Agent.Render(label + rule)
}

// indent prefixes every line of s with two spaces.
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

// truncate shortens s to at most w printable cells.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	lo, hi := 0, len(r)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if lipgloss.Width(string(r[:mid])) <= w-1 {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(r[:lo]) + "…"
}
