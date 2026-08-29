package review

import (
	"testing"

	"github.com/geoffjay/graph-review/internal/github"
)

// twoHunkPatch has hunks covering new-file lines 10-11 (added) and
// 40 (context) / 41 (added). Line 5 and line 63 fall outside every hunk.
const twoHunkPatch = `@@ -8,3 +9,3 @@
 context nine
-removed ten
+added ten
+added eleven
@@ -38,4 +39,4 @@
 context thirty-nine
 context forty
-deleted forty-one
+added forty-one
`

func fileInfoWithPatch(name, patch string) github.FileInfo {
	return github.FileInfo{Filename: name, Patch: patch}
}

func TestFilterByDiffLines(t *testing.T) {
	files := []github.FileInfo{
		fileInfoWithPatch("a.go", twoHunkPatch),
		{Filename: "no-patch.bin"}, // binary/large diff: patch unavailable
	}
	tests := []struct {
		name    string
		finding Finding
		keep    bool
	}{
		{"added line", Finding{File: "a.go", Line: 10, Body: "x"}, true},
		{"context line", Finding{File: "a.go", Line: 40, Body: "x"}, true},
		{"line outside hunks", Finding{File: "a.go", Line: 63, Body: "x"}, false},
		{"line before first hunk", Finding{File: "a.go", Line: 5, Body: "x"}, false},
		{"deleted line number", Finding{File: "a.go", Line: 38, Body: "x"}, false},
		{"range within one hunk", Finding{File: "a.go", Line: 10, EndLine: 11, Body: "x"}, true},
		{"range spanning two hunks", Finding{File: "a.go", Line: 10, EndLine: 40, Body: "x"}, false},
		{"file without patch kept", Finding{File: "no-patch.bin", Line: 5, Body: "x"}, true},
		{"file not in PR kept (file filter's job)", Finding{File: "zzz.go", Line: 1, Body: "x"}, true},
		{"no location kept (never becomes a comment)", Finding{File: "a.go", Body: "x"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterByDiffLines([]Finding{tt.finding}, files)
			if tt.keep && len(got) != 1 {
				t.Errorf("finding dropped, want kept")
			}
			if !tt.keep && len(got) != 0 {
				t.Errorf("finding kept, want dropped")
			}
		})
	}
}

// TestFilterByDiffLinesRepro422 reproduces the failure shape from PR #6:
// a finding whose file is part of the PR but whose line is not in any
// diff hunk must be dropped, or the whole review POST fails with
// 422 "Line could not be resolved".
func TestFilterByDiffLinesRepro422(t *testing.T) {
	files := []github.FileInfo{fileInfoWithPatch("internal/agents/gate.go", twoHunkPatch)}
	findings := []Finding{
		{File: "internal/agents/gate.go", Line: 10, Body: "anchorable"},
		{File: "internal/agents/gate.go", Line: 63, Body: "not in the diff"},
	}
	got := FilterByDiffLines(findings, files)
	if len(got) != 1 || got[0].Body != "anchorable" {
		t.Fatalf("FilterByDiffLines = %+v, want only the anchorable finding", got)
	}
}
