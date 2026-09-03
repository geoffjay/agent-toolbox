// Package tools provides function tools that the review agents can call to
// inspect the repository surrounding a diff: reading files, listing
// directories, and querying git history.
//
// Tools are rooted at a repo directory resolved at call time from the
// agent context state (key RepoPathStateKey, set by the CLI) or the
// process working directory as a fallback. All file paths are cleaned
// and checked to stay within the repo root.
package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// RepoPathStateKey is the session state key holding the absolute repo root
// path the tools operate in. The CLI sets it before running the pipeline.
const RepoPathStateKey = "repo_path"

// ErrOutsideRepo is returned when a requested path escapes the repo root.
var ErrOutsideRepo = errors.New("path escapes repo root")

// ReadFileInput is the input for the read_file tool.
type ReadFileInput struct {
	Path     string `json:"path" jsonschema:"repo-relative file path to read"`
	MaxLines int    `json:"max_lines,omitempty" jsonschema:"optional cap on returned lines; 0 means no limit"`
}

// ReadFileOutput is the output of the read_file tool.
type ReadFileOutput struct {
	// Content is the file with every line prefixed by its 1-based line
	// number in the same %4d| gutter the numbered diff uses, so a
	// reviewer citing `path:line` copies an authoritative number instead
	// of counting lines by hand.
	Content string `json:"content"`
	Lines   int    `json:"lines"`
}

// ReadFile reads a file from the repo. It is bound to the repo root and
// rejects paths that escape it.
func ReadFile(ctx agent.Context, in ReadFileInput) (ReadFileOutput, error) {
	root, err := repoRoot(ctx)
	if err != nil {
		return ReadFileOutput{}, err
	}
	abs, err := safeJoin(root, in.Path)
	if err != nil {
		return ReadFileOutput{}, err
	}
	b, err := os.ReadFile(abs) // #nosec G304 -- in.Path is joined under the repo root and rejected when it escapes (safeJoin).
	if err != nil {
		return ReadFileOutput{}, fmt.Errorf("read %s: %w", in.Path, err)
	}
	content := string(b)
	lines := strings.Count(content, "\n")
	if in.MaxLines > 0 {
		content = limitLines(content, in.MaxLines)
		if in.MaxLines < lines {
			lines = in.MaxLines
		}
	}
	return ReadFileOutput{Content: numberLines(content), Lines: lines}, nil
}

// numberLines prefixes every line of s with its 1-based line number in a
// %4d| gutter, matching the numbered-diff gutter the reviewers see, so
// numbers are interchangeable between the diff and full-file views.
func numberLines(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	n := 1
	for line := range strings.SplitSeq(strings.TrimSuffix(s, "\n"), "\n") {
		if n > 1 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%4d|%s", n, line)
		n++
	}
	return b.String()
}

// ListFilesInput is the input for the list_files tool.
type ListFilesInput struct {
	Dir string `json:"dir,omitempty" jsonschema:"repo-relative directory to list; defaults to repo root"`
}

// ListFilesOutput is the output of the list_files tool.
type ListFilesOutput struct {
	Entries []string `json:"entries"`
}

// ListFiles lists the immediate children of a directory in the repo.
func ListFiles(ctx agent.Context, in ListFilesInput) (ListFilesOutput, error) {
	root, err := repoRoot(ctx)
	if err != nil {
		return ListFilesOutput{}, err
	}
	dir := in.Dir
	if dir == "" {
		dir = "."
	}
	abs, err := safeJoin(root, dir)
	if err != nil {
		return ListFilesOutput{}, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return ListFilesOutput{}, fmt.Errorf("list %s: %w", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		out = append(out, name)
	}
	return ListFilesOutput{Entries: out}, nil
}

// GitBlameInput is the input for the git_blame tool.
type GitBlameInput struct {
	Path      string `json:"path" jsonschema:"repo-relative file path to blame"`
	StartLine int    `json:"start_line,omitempty" jsonschema:"1-based first line; 0 means start of file"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"1-based last line; 0 means end of file"`
}

// GitBlameOutput is the output of the git_blame tool.
type GitBlameOutput struct {
	Blame string `json:"blame"`
}

// GitBlame runs `git blame` on a file range and returns the raw output.
func GitBlame(ctx agent.Context, in GitBlameInput) (GitBlameOutput, error) {
	root, err := repoRoot(ctx)
	if err != nil {
		return GitBlameOutput{}, err
	}
	if _, err := safeJoin(root, in.Path); err != nil {
		return GitBlameOutput{}, err
	}
	rangeArg := fmt.Sprintf("%d,%d", lineOrZero(in.StartLine), lineOrZero(in.EndLine))
	out, err := runGit(ctx, root, "blame", "-L", rangeArg, "--", in.Path)
	if err != nil {
		return GitBlameOutput{}, fmt.Errorf("git blame %s: %w", in.Path, err)
	}
	return GitBlameOutput{Blame: out}, nil
}

// GitLogInput is the input for the git_log tool.
type GitLogInput struct {
	Path  string `json:"path,omitempty" jsonschema:"repo-relative file path; omit for whole-repo log"`
	Limit int    `json:"limit,omitempty" jsonschema:"max commits to return; defaults to 10"`
}

// GitLogOutput is the output of the git_log tool.
type GitLogOutput struct {
	Log string `json:"log"`
}

// GitLog runs `git log --oneline` for a file or the repo and returns it.
func GitLog(ctx agent.Context, in GitLogInput) (GitLogOutput, error) {
	root, err := repoRoot(ctx)
	if err != nil {
		return GitLogOutput{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	args := []string{"log", "--oneline", fmt.Sprintf("-%d", limit)}
	if in.Path != "" {
		if _, err := safeJoin(root, in.Path); err != nil {
			return GitLogOutput{}, err
		}
		args = append(args, "--", in.Path)
	}
	out, err := runGit(ctx, root, args...)
	if err != nil {
		return GitLogOutput{}, fmt.Errorf("git log: %w", err)
	}
	return GitLogOutput{Log: out}, nil
}

// NewTools returns the four repo-inspection function tools. The returned
// slice can be passed directly to llmagent.Config.Tools.
func NewTools() ([]tool.Tool, error) {
	read, err := functiontool.New(functiontool.Config{
		Name:        "read_file",
		Description: "Read a file from the repository, relative to the repo root. Use this to inspect code surrounding a diff hunk. Every returned line is prefixed with its 1-based line number; cite those numbers in findings.",
	}, ReadFile)
	if err != nil {
		return nil, fmt.Errorf("build read_file tool: %w", err)
	}
	list, err := functiontool.New(functiontool.Config{
		Name:        "list_files",
		Description: "List the immediate children of a directory in the repository. Use this to understand the layout around a touched file.",
	}, ListFiles)
	if err != nil {
		return nil, fmt.Errorf("build list_files tool: %w", err)
	}
	blame, err := functiontool.New(functiontool.Config{
		Name:        "git_blame",
		Description: "Run git blame on a file line range to see who last changed each line and when.",
	}, GitBlame)
	if err != nil {
		return nil, fmt.Errorf("build git_blame tool: %w", err)
	}
	log, err := functiontool.New(functiontool.Config{
		Name:        "git_log",
		Description: "Show recent commit history for a file or the whole repository as oneline log entries.",
	}, GitLog)
	if err != nil {
		return nil, fmt.Errorf("build git_log tool: %w", err)
	}
	return []tool.Tool{read, list, blame, log}, nil
}

// repoRoot resolves the repo root from the agent context state or the
// process working directory.
func repoRoot(ctx agent.Context) (string, error) {
	if v, err := ctx.State().Get(RepoPathStateKey); err == nil {
		if s, ok := v.(string); ok && s != "" {
			if _, err := os.Stat(s); err == nil {
				return s, nil
			}
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve repo root: %w", err)
	}
	return cwd, nil
}

// safeJoin joins rel onto root and verifies the result stays inside root.
func safeJoin(root, rel string) (string, error) {
	clean := filepath.Clean("/" + rel)
	abs := filepath.Join(root, clean)
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", ErrOutsideRepo
	}
	return abs, nil
}

// limitLines returns the first n lines of s, preserving the final newline.
func limitLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.SplitAfter(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "")
}

func lineOrZero(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// runGit runs a git command in dir, cancelled with ctx, and returns its
// stdout.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) // #nosec G204 -- git runs without a shell; args come from the tool's validated inputs.
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return string(out), nil
}
