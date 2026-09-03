package review

import (
	"strings"
	"testing"

	"github.com/geoffjay/agent-toolbox/internal/github"
)

// pr10LikeDiff mirrors the hunk shape from the live PR whose posted
// review drifted: a file header with deletions before the first hunk and
const pr10LikeDiff = "diff --git a/internal/ui/presenter.go b/internal/ui/presenter.go\n" +
	"index 57c3bce..c4f0024 100644\n" +
	"--- a/internal/ui/presenter.go\n" +
	"+++ b/internal/ui/presenter.go\n" +
	"@@ -1,6 +1,7 @@\n" +
	"-package cmd\n" +
	"+package ui\n" +
	" \n" +
	" import (\n" +
	"+	\"bufio\"\n" +
	" 	\"context\"\n" +
	" 	\"fmt\"\n" +
	"@@ -12,13 +13,16 @@ import (\n" +
	" 	\"time\"\n" +
	" \n" +
	" 	\"github.com/geoffjay/graph-review/internal/agents\"\n" +
	"-	\"github.com/geoffjay/graph-review/internal/ui\"\n" +
	" )\n" +
	" \n" +
	" // Presenter surfaces pipeline progress to the user.\n"

func numberedLine(t *testing.T, out string, want string) {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if line == want {
			return
		}
	}
	t.Errorf("numbered diff missing line %q:\n%s", want, out)
}

func TestNumberedDiffNumbersRightSideLines(t *testing.T) {
	out := NumberedDiff(pr10LikeDiff)

	// The first hunk starts at new-file line 1; the deleted "-package
	// cmd" before it consumes no number.
	numberedLine(t, out, "   1|+package ui")
	// Context lines carry their own numbers.
	numberedLine(t, out, "   3| import (")
	numberedLine(t, out, "   5| 	\"context\"")
	// Deletions stay unnumbered and consume nothing.
	numberedLine(t, out, "    |-package cmd")
	// The second hunk restarts numbering at 13 per its header.
	numberedLine(t, out, "  13| 	\"time\"")
	// The +13,16 header must not be rendered (models misread the count).
	if strings.Contains(out, "@@") {
		t.Errorf("numbered diff still contains a hunk header:\n%s", out)
	}
	// The line the live review cited as 16 is the closing paren; the
	// import is 15. Both must be visible with the right numbers.
	numberedLine(t, out, "  15| 	\"github.com/geoffjay/graph-review/internal/agents\"")
	numberedLine(t, out, "  16| )")
}

func TestNumberedDiffMultiFileAndBlankContext(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n" +
		"--- a/a.go\n" +
		"+++ b/a.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" package a\n" +
		"+\n" +
		" \n" +
		"diff --git a/b.go b/b.go\n" +
		"--- a/b.go\n" +
		"+++ b/b.go\n" +
		"@@ -7,2 +7,2 @@\n" +
		" package b\n" +
		"-old\n" +
		"+new\n"
	out := NumberedDiff(diff)

	// File headers of the second file are not numbered as additions
	// (a "+++ b/b.go" after a hunk must not consume a number).
	numberedLine(t, out, "    |diff --git a/b.go b/b.go")
	// The second file's hunk restarts numbering from its own header.
	numberedLine(t, out, "   7| package b")
	// Blank context and blank added lines are numbered too.
	numberedLine(t, out, "   2|+")
	numberedLine(t, out, "   3| ")
	// A deletion in the second file stays unnumbered.
	numberedLine(t, out, "    |-old")
	numberedLine(t, out, "   8|+new")
}

func TestNumberedDiffNoNewlineMarker(t *testing.T) {
	diff := "--- a/x.txt\n" +
		"+++ b/x.txt\n" +
		"@@ -1,2 +1,2 @@\n" +
		" a\n" +
		"-b\n" +
		"+c\n" +
		"\\ No newline at end of file\n"
	out := NumberedDiff(diff)
	numberedLine(t, out, "   1| a")
	numberedLine(t, out, "    |-b")
	numberedLine(t, out, "   2|+c")
	numberedLine(t, out, "    |\\ No newline at end of file")
}

func TestNumberedDiffEmpty(t *testing.T) {
	if got := NumberedDiff(""); got != "" {
		t.Errorf("NumberedDiff(\"\") = %q, want empty", got)
	}
}

// TestNumberedDiffGutterMatchesAnchors is the cross-invariant: every
// line number NumberedDiff displays must be accepted by
// FilterByDiffLines on the same patch. If the two disagree, a reviewer
// citing a shown number gets its finding silently dropped (or worse,
// posts an unresolvable anchor).
func TestNumberedDiffGutterMatchesAnchors(t *testing.T) {
	patch := pr10LikeDiff[strings.Index(pr10LikeDiff, "@@"):]
	shown := map[int]bool{}
	for line := range strings.SplitSeq(NumberedDiff(patch), "\n") {
		m := strings.Index(line, "|")
		if m <= 0 {
			continue
		}
		n := 0
		ok := true
		for _, c := range line[:m] {
			if c == ' ' {
				continue
			}
			if c < '0' || c > '9' {
				ok = false
				break
			}
			n = n*10 + int(c-'0')
		}
		if ok && n > 0 {
			shown[n] = true
		}
	}
	if len(shown) == 0 {
		t.Fatal("no numbered lines parsed from the fixture")
	}
	files := []github.FileInfo{fileInfoWithPatch("internal/ui/presenter.go", patch)}
	for n := range shown {
		finding := Finding{File: "internal/ui/presenter.go", Line: n, EndLine: n}
		if got := FilterByDiffLines([]Finding{finding}, files); len(got) != 1 {
			t.Errorf("line %d shown in the gutter is not anchorable per FilterByDiffLines", n)
		}
	}
}
