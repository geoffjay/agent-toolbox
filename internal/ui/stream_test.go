package ui

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"testing"
)

// TestPlainStreamReportAgentFilter guards the plain surface's stdout gate:
// at the default level only the report agent's stream reaches stdout, and
// raising verbosity to info lets every agent's stream through.
func TestPlainStreamReportAgentFilter(t *testing.T) {
	capture := func(p plainPresenter, agent, text string) string {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("stdout pipe: %v", err)
		}
		orig := os.Stdout
		os.Stdout = w
		p.Stream(agent, text)
		os.Stdout = orig
		_ = w.Close()
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		_ = r.Close()
		return buf.String()
	}

	quiet := plainPresenter{level: slog.LevelWarn, reportAgent: "summary"}
	if got := capture(quiet, "summary", "report body"); got != "report body" {
		t.Errorf("report agent at default level = %q, want the streamed text", got)
	}
	if got := capture(quiet, "triage", "chatter"); got != "" {
		t.Errorf("non-report agent at default level = %q, want silence", got)
	}

	verbose := plainPresenter{level: slog.LevelInfo, reportAgent: "summary"}
	if got := capture(verbose, "triage", "chatter"); got != "chatter" {
		t.Errorf("non-report agent at info level = %q, want the streamed text", got)
	}
}
