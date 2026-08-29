package review

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/geoffjay/graph-review/internal/github"
)

// hunkHeaderRe matches unified diff hunk headers like
// "@@ -12,4 +13,6 @@ optional section".
var hunkHeaderRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// diffHunk is the set of RIGHT-side (new-file) line numbers a GitHub
// inline review comment can anchor to within one hunk: context lines
// and additions. Deletions are not anchorable — they have no position
// in the new file.
type diffHunk struct {
	lines map[int]bool
}

func (h diffHunk) contains(n int) bool { return h.lines[n] }

// containsRange reports whether both endpoints of a line range fall in
// this hunk. GitHub requires start_line and line to resolve within the
// same hunk; a range spanning two hunks is rejected with 422.
func (h diffHunk) containsRange(a, b int) bool {
	if a > b {
		a, b = b, a
	}
	return h.lines[a] && h.lines[b]
}

// patchHunks parses a unified diff patch into its anchorable hunks.
// Lines outside any hunk (file headers, index lines) are ignored.
func patchHunks(patch string) []diffHunk {
	var hunks []diffHunk
	var cur *diffHunk
	line := 0
	for _, raw := range strings.Split(patch, "\n") {
		if m := hunkHeaderRe.FindStringSubmatch(raw); m != nil {
			start, _ := strconv.Atoi(m[1])
			hunks = append(hunks, diffHunk{lines: map[int]bool{}})
			cur = &hunks[len(hunks)-1]
			line = start
			continue
		}
		if cur == nil {
			continue // file header ("diff --git", "index", "---", "+++")
		}
		switch {
		case strings.HasPrefix(raw, "+"):
			cur.lines[line] = true
			line++
		case strings.HasPrefix(raw, " "):
			cur.lines[line] = true
			line++
		case strings.HasPrefix(raw, "-"):
			// deleted line: not anchorable on the RIGHT side
		default:
			// "\ No newline at end of file" and similar: no line consumed
		}
	}
	return hunks
}

// FilterByDiffLines drops findings whose line anchor cannot be resolved
// against the PR's file patches. The GitHub review API rejects any
// comment whose line (or range endpoints) is not a RIGHT-side line of
// the file's diff hunks, which fails the whole POST with 422 ("Line
// could not be resolved").
//
// Findings on files whose patch is unavailable (binary or oversized
// diffs, pre-rename paths) are kept — they cannot be validated, and the
// posting path degrades gracefully if GitHub still rejects them.
func FilterByDiffLines(findings []Finding, files []github.FileInfo) []Finding {
	patches := make(map[string]string, len(files))
	for _, f := range files {
		if f.Patch != "" {
			patches[f.Filename] = f.Patch
		}
	}
	kept := findings[:0]
	for _, f := range findings {
		patch, ok := patches[f.File]
		if !ok || !f.HasLocation() {
			kept = append(kept, f)
			continue
		}
		anchorable := false
		for _, hunk := range patchHunks(patch) {
			if f.EndLine > 0 && f.EndLine != f.Line {
				if hunk.containsRange(f.Line, f.EndLine) {
					anchorable = true
					break
				}
			} else if hunk.contains(f.Line) {
				anchorable = true
				break
			}
		}
		if anchorable {
			kept = append(kept, f)
		}
	}
	return kept
}
