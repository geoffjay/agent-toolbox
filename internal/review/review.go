// Package review parses the summary agent's review report into structured
// findings suitable for posting as inline GitHub PR review comments.
package review

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/geoffjay/graph-review/internal/github"
)

var findingRe = regexp.MustCompile(`(?m)^\s*[-*]\s+(.+)`)

var fileLineRe = regexp.MustCompile(`([^\s:` + "`" + `]+?\.[a-zA-Z0-9]+)(?::(\d+)(?:[-,](\d+))?)?`)

// Finding represents a single parsed review finding with optional
// file and line location.
type Finding struct {
	File    string
	Line    int
	EndLine int
	Body    string
}

// HasLocation reports whether the finding has a file and line anchor.
func (f Finding) HasLocation() bool {
	return f.File != "" && f.Line > 0
}

// ToComment converts the finding to a GitHub inline review comment.
func (f Finding) ToComment() github.ReviewComment {
	c := github.ReviewComment{
		Path: f.File,
		Side: "RIGHT",
		Body: f.Body,
	}
	if f.EndLine > 0 && f.EndLine != f.Line {
		c.Line = f.EndLine
		c.StartLine = f.Line
	} else {
		c.Line = f.Line
	}
	return c
}

// ParseFindings extracts inline-commentable findings from the summary
// report. It looks for bullet lines in the "## Findings" section that
// reference a file:line pattern.
func ParseFindings(report string) []Finding {
	var findings []Finding
	for _, line := range strings.Split(report, "\n") {
		if !findingRe.MatchString(line) {
			continue
		}
		text := strings.TrimSpace(findingRe.ReplaceAllString(line, "$1"))
		if text == "" {
			continue
		}

		loc := fileLineRe.FindStringSubmatch(text)
		if loc == nil {
			continue
		}

		f := Finding{Body: text}
		f.File = loc[1]
		if loc[2] != "" {
			fmt.Sscanf(loc[2], "%d", &f.Line)
		}
		if loc[3] != "" {
			fmt.Sscanf(loc[3], "%d", &f.EndLine)
		}
		if f.EndLine == 0 {
			f.EndLine = f.Line
		}
		findings = append(findings, f)
	}
	return findings
}

// ExtractVerdict scans the report for the verdict line and returns the
// GitHub review event ("APPROVED", "REQUEST_CHANGES", or "COMMENT").
func ExtractVerdict(report string) string {
	section := extractSection(report, "## Verdict")
	if section == "" {
		return "COMMENT"
	}
	lower := strings.ToLower(section)
	switch {
	case strings.Contains(lower, "approve") && !strings.Contains(lower, "request changes"):
		return "APPROVED"
	case strings.Contains(lower, "request changes"):
		return "REQUEST_CHANGES"
	case strings.Contains(lower, "needs discussion"):
		return "COMMENT"
	default:
		return "COMMENT"
	}
}

func extractSection(report, header string) string {
	lines := strings.Split(report, "\n")
	var sb strings.Builder
	capturing := false
	for _, line := range lines {
		if strings.TrimSpace(line) == header {
			capturing = true
			continue
		}
		if capturing {
			if strings.HasPrefix(strings.TrimSpace(line), "## ") {
				break
			}
			sb.WriteString(line + "\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

// BuildReviewRequest constructs a GitHub PostReviewRequest from the
// summary report body and parsed findings.
func BuildReviewRequest(report string, findings []Finding) *github.PostReviewRequest {
	req := &github.PostReviewRequest{
		Body:  report,
		Event: ExtractVerdict(report),
	}
	for _, f := range findings {
		if f.HasLocation() {
			req.Comments = append(req.Comments, f.ToComment())
		}
	}
	return req
}