package review

import (
	"reflect"
	"testing"

	"github.com/geoffjay/graph-review/internal/github"
)

func TestParseFindings(t *testing.T) {
	report := `## Verdict
Approve — the change is sound.

- ` + "`main.go:1`" + ` [nit] Verdict-section bullets must not become inline comments.

## Findings

- ` + "`internal/rules/rules.go:180`" + ` [major] Closing delimiter search matches any occurrence of the delimiter.
- ` + "`cmd/main.go:10-20`" + ` [minor] Range finding with start and end line.
- No findings: nothing to anchor.
- A bullet with no file reference at all.
- ` + "`readme.md:0`" + ` [nit] Zero line numbers are invalid anchors.

## Top concerns

- ` + "`summary.md:99`" + ` [nit] Top-concern bullets must not become inline comments either.
`
	got := ParseFindings(report)
	want := []Finding{
		{File: "internal/rules/rules.go", Line: 180, EndLine: 180, Body: "`internal/rules/rules.go:180` [major] Closing delimiter search matches any occurrence of the delimiter."},
		{File: "cmd/main.go", Line: 10, EndLine: 20, Body: "`cmd/main.go:10-20` [minor] Range finding with start and end line."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseFindings()\n got: %+v\nwant: %+v", got, want)
	}
}

func TestParseFindingsEmptySection(t *testing.T) {
	for _, report := range []string{
		"",
		"no sections at all",
		"## Verdict\nApprove.\n",
		"## Findings\n\n## Top concerns\nNone.\n",
	} {
		if got := ParseFindings(report); len(got) != 0 {
			t.Errorf("ParseFindings(%q) = %+v, want empty", report, got)
		}
	}
}

func TestParseFindingsReversedRange(t *testing.T) {
	got := ParseFindings("## Findings\n\n- `a.go:20-10` [minor] Reversed range clamps to start line.\n")
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Line != 20 || got[0].EndLine != 20 {
		t.Errorf("range = %d-%d, want 20-20 (end clamped to start)", got[0].Line, got[0].EndLine)
	}
}

func TestExtractVerdict(t *testing.T) {
	tests := []struct {
		name   string
		report string
		want   string
	}{
		{"approve", "## Verdict\nApprove — changes are correct.\n\n## Findings\n- `a.go:1` [nit] Something.\n", "APPROVED"},
		{"request changes", "## Verdict\nRequest changes due to a blocker.\n\n## Findings\n- `a.go:1` [blocker] Broken.\n", "REQUEST_CHANGES"},
		{"needs discussion", "## Verdict\nNeeds discussion — the approach is unclear.\n\n## Findings\n- `a.go:1` [nit] Question.\n", "COMMENT"},
		{"empty findings", "## Verdict\nApprove.\n\n## Findings\nNo findings reported.\n", "COMMENT"},
		{"missing verdict", "## Findings\n- `a.go:1` [nit] Something.\n", "COMMENT"},
		{"empty report", "", "COMMENT"},
		{"both phrases means request changes wins", "## Verdict\nApprove. Do not request changes lightly.\n\n## Findings\n- `a.go:1` [nit] Something.\n", "REQUEST_CHANGES"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractVerdict(tt.report); got != tt.want {
				t.Errorf("ExtractVerdict() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildReviewRequest(t *testing.T) {
	findings := []Finding{
		{File: "a.go", Line: 5, Body: "issue one"},
		{File: "", Line: 0, Body: "not located"},
	}
	req := BuildReviewRequest("## Verdict\nRequest changes.\n\n## Findings\n- `a.go:5` issue one.\n", findings)
	if req.Event != "REQUEST_CHANGES" {
		t.Errorf("Event = %q, want REQUEST_CHANGES", req.Event)
	}
	if len(req.Comments) != 1 {
		t.Fatalf("len(Comments) = %d, want 1 (unlocated findings dropped)", len(req.Comments))
	}
	if req.Comments[0].Path != "a.go" || req.Comments[0].Line != 5 || req.Comments[0].Side != "RIGHT" {
		t.Errorf("unexpected comment: %+v", req.Comments[0])
	}
	if req.Body == "" {
		t.Error("Body should carry the full report")
	}
}

func TestToCommentRanges(t *testing.T) {
	single := Finding{File: "a.go", Line: 7, EndLine: 7, Body: "b"}
	if c := single.ToComment(); c.Line != 7 || c.StartLine != 0 {
		t.Errorf("single-line comment = %+v, want Line 7, no StartLine", c)
	}
	multi := Finding{File: "a.go", Line: 7, EndLine: 9, Body: "b"}
	if c := multi.ToComment(); c.StartLine != 7 || c.Line != 9 {
		t.Errorf("range comment = %+v, want StartLine 7, Line 9", c)
	}
}

func TestFilterByFiles(t *testing.T) {
	files := []github.FileInfo{
		{Filename: "internal/rules/rules.go"},
		{Filename: "renamed.go", PreviousFilename: "old.go"},
	}
	findings := []Finding{
		{File: "internal/rules/rules.go", Line: 10, Body: "kept"},
		{File: "old.go", Line: 5, Body: "kept via rename source"},
		{File: "not-in-diff.go", Line: 1, Body: "dropped"},
		{File: "", Line: 0, Body: "dropped"},
	}
	got := FilterByFiles(findings, files)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	if got[0].Body != "kept" || got[1].Body != "kept via rename source" {
		t.Errorf("kept the wrong findings: %+v", got)
	}
	if len(FilterByFiles(nil, files)) != 0 {
		t.Error("nil findings should stay nil-length")
	}
	if len(FilterByFiles(findings, nil)) != 0 {
		t.Error("no changed files means no comments")
	}
}
