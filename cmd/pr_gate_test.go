package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/geoffjay/graph-review/internal/ui"
)

// TestConfirmPostInteractivePTY exercises the human-approval gate on a real
// terminal. It only runs when stdin is a pty (e.g. under `script`); in plain
// test runs it skips so the suite stays CI-safe.
//
//	script -q /dev/null go test ./cmd -run TestConfirmPostInteractivePTY -v \
//	  < <(printf 'n\n')   # declines
//	script -q /dev/null go test ./cmd -run TestConfirmPostInteractivePTY -v \
//	  < <(printf 'y\n')   # approves
func TestConfirmPostInteractivePTY(t *testing.T) {
	ptyExpect := os.Getenv("PTY_EXPECT")
	if ptyExpect == "" {
		t.Skip("PTY_EXPECT not set; interactive gate test not requested")
	}
	stat, err := os.Stdin.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		t.Skip("stdin is not a terminal")
	}

	ok, err := plainPresenter{}.Confirm(ui.Confirmation{
		Title:  "post this review to geoffjay/graph-review#42?",
		Detail: "event: COMMENT (1 inline comment)\n  comment 1 — a.go:1",
	})
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	want := ptyExpect == "approve"
	if ok != want {
		t.Errorf("Confirm() = %v, want %v (PTY_EXPECT=%q)", ok, want, ptyExpect)
	}
}

// TestPlainConfirmPrintsBody guards the plain confirm surface: the human
// is shown the full text they are approving, not just the event summary.
// With a non-terminal stdin the prompt declines (fail closed), but the
// title, detail, and body are printed before that check.
func TestPlainConfirmPrintsBody(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	stderr := os.Stderr
	os.Stderr = w
	_, confirmErr := plainPresenter{}.Confirm(ui.Confirmation{
		Title:  "post this review to geoffjay/graph-review#42?",
		Detail: "event: COMMENT (1 inline comment)",
		Body:   "## Verdict\n\nLGTM with one nit.",
	})
	os.Stderr = stderr
	_ = w.Close()
	var out bytes.Buffer
	_, _ = io.Copy(&out, r)

	if confirmErr == nil {
		t.Error("Confirm() succeeded with non-terminal stdin; want fail-closed error")
	}
	for _, want := range []string{
		"post this review to geoffjay/graph-review#42?",
		"event: COMMENT (1 inline comment)",
		"## Verdict",
		"LGTM with one nit.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("confirm output missing %q:\n%s", want, out.String())
		}
	}
}
