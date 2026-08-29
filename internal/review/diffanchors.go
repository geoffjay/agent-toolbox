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

// maxPatchLines bounds patchHunks against a hostile or pathologically
// large PR patch: the per-file patch text comes from the GitHub API and
// is not size-limited by the fetcher, so parsing stops once this many
// lines have been scanned. The cap sits well beyond any realistic
// reviewable diff, so legitimate anchors are never dropped, while an
// attacker cannot force an unbounded scan or an unbounded per-line map.
const maxPatchLines = 50_000

// patchHunks parses a unified diff patch into its anchorable hunks.
// Lines outside any hunk (file headers, index lines) are ignored.
func patchHunks(patch string) []diffHunk {
	var hunks []diffHunk
	var cur *diffHunk
	line := 0
	// SplitSeq streams the patch line by line instead of allocating a
	// slice of every line up front, and scanned caps the total work.
	scanned := 0
	for raw := range strings.SplitSeq(patch, "\n") {
		if scanned++; scanned > maxPatchLines {
			break
		}
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
		case strings.HasPrefix(raw, "+"), strings.HasPrefix(raw, " "):
			cur.lines[line] = true
			line++
		default:
			// Deleted lines ("-") are not anchorable on the RIGHT side;
			// "\ No newline at end of file" and similar consume no line.
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
