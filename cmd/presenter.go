package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/geoffjay/graph-review/internal/agents"
	"github.com/geoffjay/graph-review/internal/ui"
)

// Presenter surfaces pipeline progress to the user and collects
// human-in-the-loop decisions. The bubbletea interface implements it for
// interactive terminals; plainPresenter reproduces the classic
// stdout/stderr behavior for pipes and CI.
type Presenter interface {
	// Start announces the run with a descriptive label.
	Start(label string)

	// Activity reports the current pipeline step (shown beside the
	// TUI spinner; ignored by the plain surface).
	Activity(text string)

	// Stream appends a chunk of agent output. The TUI streams every
	// agent; the plain surface only prints the summary agent's output
	// unless verbosity is raised.
	Stream(agent, text string)

	// Warn shows a warning block (shallow review, diagnostics).
	Warn(text string)

	// Note shows an informational line after the report.
	Note(text string)

	// Finish presents the final report.
	Finish(report string)

	// Gate pauses for a human decision on a pipeline gate.
	Gate(req ui.GateRequest) (map[string]any, error)

	// Confirm asks a yes/no question.
	Confirm(c ui.Confirmation) (bool, error)
}

// plainPresenter writes to stdout/stderr like a classic CLI: streaming
// summary output on stdout, everything else on stderr, prompts on stdin.
type plainPresenter struct {
	lf loggingFlags
}

func (p plainPresenter) Start(label string) {
	fmt.Fprintln(os.Stderr, label)
}

func (p plainPresenter) Activity(string) {}

func (p plainPresenter) Stream(agent, text string) {
	if agent == agents.SummaryAgentName || p.lf.level() <= slog.LevelInfo {
		fmt.Print(text)
	}
}

func (p plainPresenter) Warn(text string) {
	fmt.Fprintln(os.Stderr, text)
}

func (p plainPresenter) Note(text string) {
	fmt.Fprintln(os.Stderr, text)
}

func (p plainPresenter) Finish(report string) {
	// The plain surface streamed the report live; nothing more to show.
	if report != "" {
		fmt.Println()
	}
}

func (p plainPresenter) Gate(req ui.GateRequest) (map[string]any, error) {
	return promptGate(req)
}

func (p plainPresenter) Confirm(c ui.Confirmation) (bool, error) {
	fmt.Fprintln(os.Stderr, c.Title)
	if c.Detail != "" {
		fmt.Fprintln(os.Stderr, c.Detail)
	}
	stat, err := os.Stdin.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		return false, fmt.Errorf("cannot prompt for approval: stdin is not a terminal; re-run interactively or pass --assume-yes")
	}
	return readApproval(os.Stdin, os.Stderr)
}

// tuiPresenter adapts the Presenter interface onto the bubbletea surface.
type tuiPresenter struct {
	p *ui.Program
}

func (t tuiPresenter) Start(label string)        { t.p.Start(label) }
func (t tuiPresenter) Activity(text string)      { t.p.Activity(text) }
func (t tuiPresenter) Stream(agent, text string) { t.p.Stream(agent, text) }
func (t tuiPresenter) Warn(text string)          { t.p.Warn(text) }
func (t tuiPresenter) Note(text string)          { t.p.Note(text) }
func (t tuiPresenter) Finish(report string)      { t.p.Finish(report) }
func (t tuiPresenter) Gate(req ui.GateRequest) (map[string]any, error) {
	reply, err := t.p.Gate(req)
	if err != nil {
		return nil, fmt.Errorf("gate: %w", err)
	}
	return reply, nil
}

func (t tuiPresenter) Confirm(c ui.Confirmation) (bool, error) {
	ok, err := t.p.Confirm(c)
	if err != nil {
		return false, fmt.Errorf("confirm: %w", err)
	}
	return ok, nil
}

// isTerminal reports whether f is an interactive terminal.
func isTerminal(f *os.File) bool {
	stat, err := f.Stat()
	return err == nil && stat.Mode()&os.ModeCharDevice != 0
}

// dispatch runs work on the right presentation surface: the bubbletea
// interface when stdout is a terminal, or the plain surface otherwise
// (piped output, CI, or --plain). Logging is configured before the
// interface starts because bubbletea owns the terminal once running.
func dispatch(ctx context.Context, lf loggingFlags, plain bool, work func(ctx context.Context, p Presenter) error) error {
	interactive := !plain && isTerminal(os.Stdout)

	closer, err := setupLogging(lf, interactive)
	if err != nil {
		return err
	}
	defer closer()
	if !interactive {
		return work(ctx, plainPresenter{lf: lf})
	}
	if err := ui.Run(ctx, func(ctx context.Context, p *ui.Program) error {
		return work(ctx, tuiPresenter{p: p})
	}); err != nil {
		return fmt.Errorf("interactive run: %w", err)
	}
	return nil
}

// setupLogging installs the process-wide slog default. The plain surface
// logs to stderr; the TUI surface logs to debug.log because bubbletea
// occupies the terminal (the DEBUG env var additionally forces debug
// level, mirroring the bubbletea logging example).
func setupLogging(lf loggingFlags, tui bool) (func(), error) {
	if !tui {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lf.level()})))
		return func() {}, nil
	}
	f, err := tea.LogToFile("debug.log", "debug")
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	level := lf.level()
	if os.Getenv("DEBUG") != "" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: level})))
	return func() { _ = f.Close() }, nil
}

var _ io.Writer = os.Stderr
