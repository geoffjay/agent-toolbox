package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

	// Milestone reports a high-signal progress line (fetching the PR,
	// cloning, posting). The plain surface prints it to stderr; the
	// TUI shows it like any other activity.
	Milestone(text string)

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
	fmt.Fprintln(os.Stderr, sanitize(label))
}

func (p plainPresenter) Milestone(text string) {
	fmt.Fprintln(os.Stderr, sanitize(text))
}

func (p plainPresenter) Activity(string) {}

func (p plainPresenter) Stream(agent, text string) {
	if agent == agents.SummaryAgentName || p.lf.level() <= slog.LevelInfo {
		fmt.Print(sanitize(text))
	}
}

func (p plainPresenter) Warn(text string) {
	fmt.Fprintln(os.Stderr, sanitize(text))
}

func (p plainPresenter) Note(text string) {
	fmt.Fprintln(os.Stderr, sanitize(text))
}

func (p plainPresenter) Finish(report string) {
	// The plain surface streamed the report live; nothing more to show.
	if report != "" {
		fmt.Println()
	}
}

func (p plainPresenter) Gate(req ui.GateRequest) (map[string]any, error) {
	req.Message, req.Payload = sanitize(req.Message), sanitize(req.Payload)
	return promptGate(req)
}

func (p plainPresenter) Confirm(c ui.Confirmation) (bool, error) {
	c.Title, c.Detail, c.Body = sanitize(c.Title), sanitize(c.Detail), sanitize(c.Body)
	fmt.Fprintln(os.Stderr, c.Title)
	if c.Detail != "" {
		fmt.Fprintln(os.Stderr, c.Detail)
	}
	if c.Body != "" {
		fmt.Fprintln(os.Stderr, c.Body)
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

func (t tuiPresenter) Start(label string)        { t.p.Start(sanitize(label)) }
func (t tuiPresenter) Milestone(text string)     { t.p.Activity(sanitize(text)) }
func (t tuiPresenter) Activity(text string)      { t.p.Activity(sanitize(text)) }
func (t tuiPresenter) Stream(agent, text string) { t.p.Stream(sanitize(agent), sanitize(text)) }
func (t tuiPresenter) Warn(text string)          { t.p.Warn(sanitize(text)) }
func (t tuiPresenter) Note(text string)          { t.p.Note(sanitize(text)) }
func (t tuiPresenter) Finish(report string)      { t.p.Finish(sanitize(report)) }
func (t tuiPresenter) Gate(req ui.GateRequest) (map[string]any, error) {
	req.Message, req.Payload = sanitize(req.Message), sanitize(req.Payload)
	reply, err := t.p.Gate(req)
	if err != nil {
		return nil, fmt.Errorf("gate: %w", err)
	}
	return reply, nil
}

func (t tuiPresenter) Confirm(c ui.Confirmation) (bool, error) {
	c.Title, c.Detail, c.Body = sanitize(c.Title), sanitize(c.Detail), sanitize(c.Body)
	ok, err := t.p.Confirm(c)
	if err != nil {
		return false, fmt.Errorf("confirm: %w", err)
	}
	return ok, nil
}

// sanitize strips terminal control runes from s — C0 controls, DEL, and
// C1 controls — keeping \n, \r, and \t. Every untrusted string rendered on
// a surface crosses it first: PR titles, streamed findings, and gate
// payloads are attacker-influenced, and a crafted PR could otherwise
// inject escape sequences (e.g. an OSC 52 clipboard rewrite) into the
// reviewer's terminal. Multibyte text is preserved.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return r
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			return -1
		default:
			return r
		}
	}, s)
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

	defer setupLogging(lf, interactive)()
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
// logs to stderr; the TUI surface logs to a per-run file under the user
// cache dir because bubbletea occupies the terminal. Log output can
// carry the reviewed diffs and file contents, so the file is private
// (0600), unique per run, and old runs are pruned; the -v/--debug flags
// alone control the level.
func setupLogging(lf loggingFlags, tui bool) func() {
	if !tui {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lf.level()})))
		return func() {}
	}
	w := io.Discard
	closer := func() {}
	if f, path, err := openRunLog(); err != nil {
		// Logging is diagnostics, not the product: run without it
		// rather than fail before any review work starts.
		fmt.Fprintf(os.Stderr, "warning: continuing without a log file: %v\n", err)
	} else {
		w, closer = f, func() { _ = f.Close() }
		fmt.Fprintf(os.Stderr, "log: %s\n", path)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lf.level()})))
	return closer
}

// logKeep bounds how many per-run log files are retained; the oldest
// are pruned so the log directory cannot grow without end.
const logKeep = 5

// openRunLog creates the per-run TUI log file in
// <UserCacheDir>/graph-review/log and prunes older runs.
func openRunLog() (*os.File, string, error) {
	dir, err := userLogDir()
	if err != nil {
		return nil, "", fmt.Errorf("resolve log dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create log dir: %w", err)
	}
	// The zero-padded pid keeps the whole name sorting lexically in
	// creation order — including same-second runs with different pid
	// widths — so pruning drops the oldest, never the run in flight.
	path := filepath.Join(dir, "run-"+time.Now().UTC().Format("20060102T150405")+"-"+fmt.Sprintf("%08d", os.Getpid())+".log")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- path is built from the cache dir, a timestamp, and the pid.
	if err != nil {
		return nil, "", fmt.Errorf("open log file: %w", err)
	}
	pruneRunLogs(dir)
	return f, path, nil
}

// userLogDir is the per-user directory holding TUI run logs.
func userLogDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve cache dir: %w", err)
	}
	return filepath.Join(base, "graph-review", "log"), nil
}

// pruneRunLogs removes the oldest run logs beyond logKeep. Best effort:
// a pruning failure only leaves an extra file behind.
func pruneRunLogs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "run-") || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names[:max(len(names)-logKeep, 0)] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}
