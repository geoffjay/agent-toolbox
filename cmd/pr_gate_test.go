package cmd

import (
	"os"
	"testing"

	"github.com/geoffjay/graph-review/internal/github"
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

	req := &github.PostReviewRequest{
		Body:  "unit-test review body",
		Event: "COMMENT",
		Comments: []github.ReviewComment{
			{Path: "a.go", Line: 1, Body: "nit: comment body"},
		},
	}
	ok, err := confirmPost(req, github.RepoRef{Owner: "geoffjay", Repo: "graph-review"}, 42, false)
	if err != nil {
		t.Fatalf("confirmPost() error = %v", err)
	}
	want := ptyExpect == "approve"
	if ok != want {
		t.Errorf("confirmPost() = %v, want %v (PTY_EXPECT=%q)", ok, want, ptyExpect)
	}
}
