package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// GateRequest describes a human-in-the-loop gate pause: the pipeline is
// blocked until the human answers.
type GateRequest struct {
	// Message is the question shown to the human.
	Message string

	// Payload carries the request body (e.g. the formatted findings).
	Payload string
}

// Confirmation describes a yes/no request such as posting a review.
type Confirmation struct {
	Title  string
	Detail string

	// Body carries the full text the decision is about (e.g. the review
	// body being posted). The plain surface prints it before prompting;
	// the TUI surface already shows the report in its viewport.
	Body string
}

// Messages exchanged between the pipeline goroutine and the model.
type (
	startMsg    struct{ label string }
	activityMsg struct{ text string }
	streamMsg   struct{ agent, text string }
	warnMsg     struct{ text string }
	noteMsg     struct{ text string }
	finishMsg   struct{ report string }
	failMsg     struct{ err error }

	gateMsg struct {
		req   GateRequest
		reply chan map[string]any
	}

	confirmMsg struct {
		c     Confirmation
		reply chan bool
	}
)

// Program drives the terminal interface for one run. The pipeline runs on
// a separate goroutine and calls the Program methods to surface progress;
// Gate and Confirm block until the human answers.
type Program struct {
	prog *tea.Program
	dead chan struct{} // closed when the program exits
}

// Run starts the terminal interface and executes work on a background
// goroutine with a context that is canceled when the interface exits
// (either the user quits or the parent context is done).
//
// work reports its outcome through the returned error: on failure the
// interface shows the error and Run returns it. When the run is
// interrupted — the user quits early or the parent context is canceled —
// the resulting context.Canceled is swallowed so a deliberate interruption
// is not reported as a failure.
func Run(ctx context.Context, work func(ctx context.Context, p *Program) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	p := &Program{dead: make(chan struct{})}
	p.prog = tea.NewProgram(newModel(), tea.WithContext(ctx))

	var workErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		workErr = work(ctx, p)
		if workErr != nil {
			p.Fail(workErr)
		}
	}()

	final, runErr := p.prog.Run()
	close(p.dead)
	cancel() // stop the work goroutine if it is still running
	<-done

	if runErr != nil &&
		!errors.Is(runErr, tea.ErrProgramKilled) &&
		!errors.Is(runErr, tea.ErrInterrupted) {
		return fmt.Errorf("run interface: %w", runErr)
	}

	if workErr != nil {
		if errors.Is(workErr, context.Canceled) {
			return nil // deliberate interruption, not a failure
		}
		return fmt.Errorf("review work: %w", workErr)
	}

	// The report was displayed in the interface; print it once more so it
	// survives in the terminal scrollback after the alt screen closes.
	if fm, ok := final.(model); ok && strings.TrimSpace(fm.report) != "" {
		// Quitting can land between Finish and the glamour render; fall
		// back to the raw markdown so the report is not lost.
		content := fm.rendered
		if content == "" {
			content = fm.report
		}
		printReport(content)
	}
	return nil
}

// Start announces the run with a descriptive label.
func (p *Program) Start(label string) {
	p.prog.Send(startMsg{label: label})
}

// Activity reports what the pipeline is currently doing; it appears
// beside the spinner.
func (p *Program) Activity(text string) {
	p.prog.Send(activityMsg{text: text})
}

// Stream appends a chunk of agent output to the scrolling view.
func (p *Program) Stream(agent, text string) {
	p.prog.Send(streamMsg{agent: agent, text: text})
}

// Warn shows a warning block alongside the final report.
func (p *Program) Warn(text string) {
	p.prog.Send(warnMsg{text: text})
}

// Note shows an informational note in the completed view.
func (p *Program) Note(text string) {
	p.prog.Send(noteMsg{text: text})
}

// Finish presents the final report rendered as markdown.
func (p *Program) Finish(report string) {
	p.prog.Send(finishMsg{report: report})
}

// Fail presents a terminal failure.
func (p *Program) Fail(err error) {
	p.prog.Send(failMsg{err: err})
}

// Gate blocks until the human answers the given request. An aborted form
// maps to the abort decision. It only errors when the interface exits
// before an answer is delivered.
func (p *Program) Gate(req GateRequest) (map[string]any, error) {
	reply := make(chan map[string]any, 1)
	p.prog.Send(gateMsg{req: req, reply: reply})
	select {
	case answer := <-reply:
		return answer, nil
	case <-p.dead:
		return nil, fmt.Errorf("review interrupted while waiting for input (%q)", req.Message)
	}
}

// Confirm blocks until the human answers a yes/no request. An aborted
// form declines. It only errors when the interface exits before an answer
// is delivered.
func (p *Program) Confirm(c Confirmation) (bool, error) {
	reply := make(chan bool, 1)
	p.prog.Send(confirmMsg{c: c, reply: reply})
	select {
	case ok := <-reply:
		return ok, nil
	case <-p.dead:
		return false, fmt.Errorf("review interrupted while waiting for confirmation (%q)", c.Title)
	}
}

// printReport writes the rendered report to stdout, preserving it in the
// scrollback after the interface closes.
func printReport(rendered string) {
	s := strings.TrimRight(rendered, "\n")
	if s == "" {
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, s)
}
