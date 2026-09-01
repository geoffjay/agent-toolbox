package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/geoffjay/graph-review/internal/agents"
)

// press builds a printable-character key press.
func press(r rune) tea.Msg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// special builds a special-key press (enter, arrows, …).
func special(code rune) tea.Msg {
	return tea.KeyPressMsg{Code: code}
}

// ctrl builds a ctrl-modified key press.
func ctrl(r rune) tea.Msg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

// executeCmd runs cmd (batches included), collecting the messages it
// produces. Commands that do not finish promptly — cursor-blink and
// spinner animation timers — are skipped so driving a model cannot
// sleep or loop; field and group progression commands return instantly.
func executeCmd(cmd tea.Cmd, out *[]tea.Msg) {
	if cmd == nil {
		return
	}
	msgCh := make(chan tea.Msg, 1)
	go func() { msgCh <- cmd() }()
	select {
	case msg := <-msgCh:
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				executeCmd(c, out)
			}
		default:
			*out = append(*out, msg)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

// drive pipes messages through the model, executing the commands each
// update returns so internal messages (field and group progression) flow
// exactly as they would inside a running program.
func drive(m model, msgs ...tea.Msg) model {
	queue := append([]tea.Msg(nil), msgs...)
	for steps := 0; len(queue) > 0 && steps < 500; steps++ {
		msg := queue[0]
		queue = queue[1:]
		tm, cmd := m.Update(msg)
		m = tm.(model)
		var out []tea.Msg
		executeCmd(cmd, &out)
		queue = append(queue, out...)
	}
	return m
}

// sized gives the model a terminal size so forms lay out deterministically.
func sized(m model) model {
	return drive(m, tea.WindowSizeMsg{Width: 100, Height: 40})
}

func TestGateFormPreselectsAbort(t *testing.T) {
	m := sized(newModel())
	reply := make(chan map[string]any, 1)
	m = drive(m, gateMsg{
		req:   GateRequest{Message: "Approve the findings?", Payload: "findings…"},
		reply: reply,
	})

	if m.form == nil {
		t.Fatal("gate form not opened")
	}
	got := m.View().Content
	if !strings.Contains(got, "Approve the findings?") {
		t.Errorf("form view missing gate message:\n%s", got)
	}
	if content := m.viewport.GetContent(); !strings.Contains(content, "findings…") {
		t.Errorf("gate payload not surfaced for the decision:\n%s", content)
	}

	// A reflexive enter submits the preselected safe default: abort,
	// matching the plain surface's explicit-typed-decision gate.
	m = drive(m, special(tea.KeyEnter))

	select {
	case answer := <-reply:
		if answer["decision"] != agents.DecisionAbort {
			t.Errorf("decision = %v, want abort", answer["decision"])
		}
	default:
		t.Fatal("no gate reply after submitting the default")
	}
	if m.form != nil {
		t.Error("form still open after submit")
	}
}

func TestGateFormApproveRequiresExplicitChoice(t *testing.T) {
	m := sized(newModel())
	reply := make(chan map[string]any, 1)
	m = drive(m, gateMsg{
		req:   GateRequest{Message: "Approve the findings?"},
		reply: reply,
	})

	// Approve sits two options above the abort default.
	m = drive(m, special(tea.KeyUp), special(tea.KeyUp), special(tea.KeyEnter))

	select {
	case answer := <-reply:
		if answer["decision"] != agents.DecisionApprove {
			t.Errorf("decision = %v, want approve", answer["decision"])
		}
		if _, ok := answer["feedback"]; ok {
			t.Errorf("approve answer carried feedback: %v", answer)
		}
	default:
		t.Fatal("no gate reply after submitting approve")
	}
}

func TestGateFormReviseCollectsFeedback(t *testing.T) {
	m := sized(newModel())
	reply := make(chan map[string]any, 1)
	m = drive(m, gateMsg{
		req:   GateRequest{Message: "Approve the findings?"},
		reply: reply,
	})

	// Up moves the cursor from the abort default to "Revise"; enter
	// reveals the feedback field.
	m = drive(m, special(tea.KeyUp), special(tea.KeyEnter))
	if m.form == nil {
		t.Fatal("form closed before feedback was submitted")
	}
	if !strings.Contains(m.form.View(), "Feedback for the reviewers") {
		t.Fatalf("feedback field not shown after selecting revise:\n%s", m.form.View())
	}

	// Type the feedback and submit with enter.
	msgs := []tea.Msg{}
	for _, r := range "please check the error handling" {
		msgs = append(msgs, press(r))
	}
	msgs = append(msgs, special(tea.KeyEnter))
	m = drive(m, msgs...)

	select {
	case answer := <-reply:
		if answer["decision"] != agents.DecisionRevise {
			t.Fatalf("decision = %v, want revise", answer["decision"])
		}
		if answer["feedback"] != "please check the error handling" {
			t.Errorf("feedback = %q, want typed text", answer["feedback"])
		}
	default:
		t.Fatal("no gate reply after submitting revise")
	}
}

func TestGateFormAbortOnCtrlC(t *testing.T) {
	m := sized(newModel())
	reply := make(chan map[string]any, 1)
	m = drive(m, gateMsg{
		req:   GateRequest{Message: "Approve the findings?"},
		reply: reply,
	})

	m = drive(m, ctrl('c'))

	select {
	case answer := <-reply:
		if answer["decision"] != agents.DecisionAbort {
			t.Errorf("decision = %v, want abort", answer["decision"])
		}
	default:
		t.Fatal("no gate reply after aborting the form")
	}
}

func TestConfirmFormAcceptsAndDeclines(t *testing.T) {
	cases := []struct {
		name string
		keys []tea.Msg
		want bool
	}{
		{
			name: "y accepts",
			keys: []tea.Msg{press('y'), special(tea.KeyEnter)},
			want: true,
		},
		{
			name: "plain enter declines",
			keys: []tea.Msg{special(tea.KeyEnter)},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sized(newModel())
			reply := make(chan bool, 1)
			m = drive(m, confirmMsg{
				c:     Confirmation{Title: "post this review?", Detail: "event: COMMENT"},
				reply: reply,
			})
			if m.form == nil {
				t.Fatal("confirm form not opened")
			}

			m = drive(m, tc.keys...)

			select {
			case ok := <-reply:
				if ok != tc.want {
					t.Errorf("confirm = %v, want %v", ok, tc.want)
				}
			default:
				t.Fatal("no confirm reply after submit")
			}
		})
	}
}

// TestQuitKey guards the quit key: without a form it quits the program;
// with a form open the keystroke belongs to the form instead.
func TestQuitKey(t *testing.T) {
	m := sized(newModel())

	_, cmd := m.Update(press('q'))
	var out []tea.Msg
	executeCmd(cmd, &out)
	quit := false
	for _, msg := range out {
		if _, ok := msg.(tea.QuitMsg); ok {
			quit = true
		}
	}
	if !quit {
		t.Fatalf("q without a form did not quit (messages: %#v)", out)
	}

	mf := sized(newModel())
	reply := make(chan map[string]any, 1)
	mf = drive(mf, gateMsg{req: GateRequest{Message: "gate?"}, reply: reply})

	tm, cmd := mf.Update(press('q'))
	if f := tm.(model); f.form == nil {
		t.Fatal("q closed the gate form instead of being captured by it")
	}
	out = nil
	executeCmd(cmd, &out)
	for _, msg := range out {
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Fatal("q with an open form quit the program")
		}
	}
}

func TestStreamAndFinishFlow(t *testing.T) {
	m := sized(newModel())
	m = drive(m, startMsg{label: "reviewing a diff"})
	m = drive(m, streamMsg{agent: "triage", text: "looking at the diff"})
	m = drive(m, streamMsg{agent: "triage", text: " more text"})

	content := m.viewport.GetContent()
	if !strings.Contains(content, "looking at the diff") || !strings.Contains(content, "more text") {
		t.Errorf("streamed text missing from view:\n%s", content)
	}
	if !strings.Contains(content, " triage ") {
		t.Errorf("agent separator missing: %q", content)
	}

	// A second agent gets its own separator.
	m = drive(m, streamMsg{agent: "static", text: "static analysis…"})
	if !strings.Contains(m.viewport.GetContent(), " static ") {
		t.Errorf("static separator missing:\n%s", m.viewport.GetContent())
	}

	// Warnings appear under the streaming view.
	m = drive(m, warnMsg{text: "WARNING: shallow review"})
	if !strings.Contains(m.viewport.GetContent(), "WARNING: shallow review") {
		t.Error("warning not shown in streaming view")
	}

	// Finish renders the report through glamour in a command.
	tm, renderCmd := m.Update(finishMsg{report: "## Verdict\n\nApprove."})
	m = tm.(model)
	if !m.done {
		t.Fatal("finish did not mark the model done")
	}
	if renderCmd == nil {
		t.Fatal("finish returned no render command")
	}
	msg := renderCmd()
	rr, ok := msg.(reportRenderedMsg)
	if !ok {
		t.Fatalf("render command produced %T, want reportRenderedMsg", msg)
	}
	if rr.err != nil {
		t.Fatalf("glamour render failed: %v", rr.err)
	}
	if !strings.Contains(rr.content, "Approve.") {
		t.Errorf("rendered content missing report body: %q", rr.content)
	}

	// Feeding the rendered message swaps the viewport content.
	m = drive(m, msg)
	if !strings.Contains(m.viewport.GetContent(), "Approve.") {
		t.Errorf("rendered report not shown in viewport:\n%s", m.viewport.GetContent())
	}
	if strings.Contains(m.viewport.GetContent(), "looking at the diff") {
		t.Error("streaming text leaked into the report view")
	}

	// The empty-report case keeps the streaming view: a fresh model that
	// finishes without a report neither renders nor replaces it.
	fresh := sized(newModel())
	fresh = drive(fresh, streamMsg{agent: "triage", text: "looking at the diff"})
	ftm, fcmd := fresh.Update(finishMsg{})
	fresh = ftm.(model)
	if fcmd != nil {
		t.Error("empty report requested a render command")
	}
	if !strings.Contains(fresh.viewport.GetContent(), "looking at the diff") {
		t.Errorf("empty report replaced the streaming view:\n%s", fresh.viewport.GetContent())
	}
}

// TestStreamSeparatorOwnLine guards the agent separator: the rule is a
// complete line, so the agent's first text must not be glued onto it.
func TestStreamSeparatorOwnLine(t *testing.T) {
	m := sized(newModel())
	m = drive(m, streamMsg{agent: "triage", text: "both"})

	for line := range strings.SplitSeq(m.viewport.GetContent(), "\n") {
		if strings.Contains(line, " triage ") && strings.Contains(line, "both") {
			t.Errorf("separator and streamed text share a line: %q", line)
		}
	}
}

// TestStreamDoesNotPadLines guards streamed chunks against lipgloss block
// alignment: styling a multi-line chunk in one Render call pads every line
// to the widest one, and that padding soft-wraps into a blank line after
// each row once the widest line exceeds the viewport.
func TestStreamDoesNotPadLines(t *testing.T) {
	m := sized(newModel())
	m = drive(m, streamMsg{
		agent: "review_pipeline",
		text:  "short\na much longer line of streamed text",
	})
	m = drive(m, warnMsg{text: "warning line one\nwarning line two, the longest"})

	for line := range strings.SplitSeq(m.viewport.GetContent(), "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("line padded with trailing spaces: %q", line)
		}
	}
}

// TestSpinnerTickChainSurvivesPause guards the tick chain: a dropped
// spinner tick freezes the animation for the rest of the run.
func TestSpinnerTickChainSurvivesPause(t *testing.T) {
	m := sized(newModel())
	m.busy = true

	tm, cmd := m.Update(m.spinner.Tick())
	m = tm.(model)
	if cmd == nil {
		t.Fatal("busy model dropped the spinner tick")
	}

	// While a gate form is open the chain must continue.
	reply := make(chan map[string]any, 1)
	m = drive(m, gateMsg{req: GateRequest{Message: "gate?"}, reply: reply})
	tm, cmd = m.Update(m.spinner.Tick())
	m = tm.(model)
	if cmd == nil {
		t.Fatal("paused model dropped the spinner tick")
	}

	// And after the run finishes.
	m = drive(m, finishMsg{})
	tm, cmd = m.Update(m.spinner.Tick())
	m = tm.(model)
	if cmd == nil {
		t.Fatal("finished model dropped the spinner tick")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"", 4, ""},
		{"hello", 0, ""},
	}
	for _, tc := range cases {
		if got := truncate(tc.in, tc.w); got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
	}
}
