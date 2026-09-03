package review

import (
	"fmt"
	"strconv"
	"strings"
)

// NumberedDiff renders a unified diff with the new-file line number
// prefixed to every added and context line, so a reviewer model can cite
// `path:line` without counting hunk headers by hand.
//
// Output shape, per diff line:
//
//	  |diff --git a/x.go b/x.go         (file header, not numbered)
//	  |index 57c3bce..c4f0024 100644
//	  |----------------------------------------
//	15|+	"…/internal/agents"           (added line, new-file #15)
//	 4| 	Context context.Context      (context line, new-file #4)
//	  |-	foo                          (deletion: no RIGHT-side number)
//	  |\ No newline at end of file     (marker: consumes no line)
//
// The gutter number is the line a GitHub inline comment anchors to
// (RIGHT side), right-aligned in a 4-cell gutter. Deletions and file
// headers carry a blank gutter; deletions consume no number.
//
// Hunk headers are replaced by a dash rule instead of rendered: their
// `+START,COUNT` numbers are the single largest source of citation drift
// (a model reads the count as a line number — observed live: the hunk
// `@@ -12,13 +13,16 @@` produced a finding citing line 16 for code at
// new-file line 15).
//
// The transformation is presentation-only: FilterByDiffLines and the rest
// of the pipeline consume the original diff text unchanged.
func NumberedDiff(diff string) string {
	if diff == "" {
		return ""
	}
	var b strings.Builder
	first := true
	// inHeader is true before the first hunk and between file sections
	// (after each "diff --git" line): every line there is a file header
	// and carries no number. This keeps a "+++ b/x" opening the next file
	// from being numbered as an addition, and a deleted line that happens
	// to start with "--" (rendered "---") from being mistaken for the
	// "--- a/x" header.
	inHeader := true
	n := 0 // current new-file line number; 0 = not in a hunk yet
	emit := func(s string) {
		if !first {
			b.WriteString("\n")
		}
		first = false
		b.WriteString(s)
	}
	for line := range strings.SplitSeq(diff, "\n") {
		if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
			inHeader = false
			n, _ = strconv.Atoi(m[1])
			emit(strings.Repeat("-", 40))
			continue
		}
		switch {
		case strings.HasPrefix(line, "diff --git"):
			inHeader = true
			emit("    |" + line)
		case inHeader:
			emit("    |" + line)
		case strings.HasPrefix(line, "+"), strings.HasPrefix(line, " "):
			// Added or context line: number it and consume it. Matches
			// patchHunks' classification exactly, so a number shown here
			// is always a line FilterByDiffLines can anchor.
			emit(fmt.Sprintf("%4d|%s", n, line))
			n++
		default:
			// Deletions, "\ No newline", and stray lines: no RIGHT-side
			// number, consumes none.
			emit("    |" + line)
		}
	}
	return b.String()
}
