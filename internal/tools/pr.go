package tools

import (
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/geoffjay/graph-review/internal/github"
)

// PRRefStateKey holds the PR identifier ("owner/repo#number") in session
// state. The CLI sets it before running the pipeline when reviewing a PR.
const PRRefStateKey = "pr_ref"

// PRFilesInput is the input for the pr_files tool.
type PRFilesInput struct{}

// PRFilesOutput is the output of the pr_files tool.
type PRFilesOutput struct {
	Files []github.FileInfo `json:"files"`
}

// PRFiles lists the files changed by the PR with per-file patch stats.
func PRFiles(ctx agent.Context, _ PRFilesInput) (PRFilesOutput, error) {
	ref, num, err := prRefFromState(ctx)
	if err != nil {
		return PRFilesOutput{}, err
	}
	files, err := github.NewClient("").ListFiles(ctx, ref, num)
	if err != nil {
		return PRFilesOutput{}, err
	}
	return PRFilesOutput{Files: files}, nil
}

// PRCommentsInput is the input for the pr_comments tool.
type PRCommentsInput struct{}

// PRCommentsOutput is the output of the pr_comments tool.
type PRCommentsOutput struct {
	Comments []github.Comment `json:"comments"`
}

// PRComments lists line-anchored review comments left on the PR.
func PRComments(ctx agent.Context, _ PRCommentsInput) (PRCommentsOutput, error) {
	ref, num, err := prRefFromState(ctx)
	if err != nil {
		return PRCommentsOutput{}, err
	}
	comments, err := github.NewClient("").ListComments(ctx, ref, num)
	if err != nil {
		return PRCommentsOutput{}, err
	}
	return PRCommentsOutput{Comments: comments}, nil
}

// PRReviewsInput is the input for the pr_reviews tool.
type PRReviewsInput struct{}

// PRReviewsOutput is the output of the pr_reviews tool.
type PRReviewsOutput struct {
	Reviews []github.Review `json:"reviews"`
}

// PRReviews lists prior reviews submitted on the PR.
func PRReviews(ctx agent.Context, _ PRReviewsInput) (PRReviewsOutput, error) {
	ref, num, err := prRefFromState(ctx)
	if err != nil {
		return PRReviewsOutput{}, err
	}
	reviews, err := github.NewClient("").ListReviews(ctx, ref, num)
	if err != nil {
		return PRReviewsOutput{}, err
	}
	return PRReviewsOutput{Reviews: reviews}, nil
}

// NewPRTools returns the three PR-metadata function tools. The returned
// slice can be appended to llmagent.Config.Tools alongside the
// repo-inspection tools.
func NewPRTools() ([]tool.Tool, error) {
	files, err := functiontool.New(functiontool.Config{
		Name:        "pr_files",
		Description: "List the files changed by the pull request, with additions, deletions, and per-file patches.",
	}, PRFiles)
	if err != nil {
		return nil, fmt.Errorf("build pr_files tool: %w", err)
	}
	comments, err := functiontool.New(functiontool.Config{
		Name:        "pr_comments",
		Description: "List the line-anchored review comments left on the pull request so far.",
	}, PRComments)
	if err != nil {
		return nil, fmt.Errorf("build pr_comments tool: %w", err)
	}
	reviews, err := functiontool.New(functiontool.Config{
		Name:        "pr_reviews",
		Description: "List the reviews already submitted on the pull request (approve, request changes, comment).",
	}, PRReviews)
	if err != nil {
		return nil, fmt.Errorf("build pr_reviews tool: %w", err)
	}
	return []tool.Tool{files, comments, reviews}, nil
}

// prRefFromState reads the PR identifier from session state and splits it
// into a RepoRef and PR number. Returns an error when no PR ref is set,
// so the PR tools fail gracefully outside the PR subcommand.
func prRefFromState(ctx agent.Context) (github.RepoRef, int, error) {
	v, err := ctx.State().Get(PRRefStateKey)
	if err != nil {
		return github.RepoRef{}, 0, fmt.Errorf("pr tools require a PR ref in session state: %w", err)
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return github.RepoRef{}, 0, fmt.Errorf("pr_ref state is not a string")
	}
	return parsePRRef(s)
}

// parsePRRef splits "owner/repo#number" into a RepoRef and PR number.
func parsePRRef(s string) (github.RepoRef, int, error) {
	hash := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '#' {
			hash = i
			break
		}
	}
	if hash < 0 {
		return github.RepoRef{}, 0, fmt.Errorf("parse pr ref %q: missing #number", s)
	}
	ref, err := github.ParseRepoRef(s[:hash])
	if err != nil {
		return github.RepoRef{}, 0, err
	}
	var num int
	if _, err := fmt.Sscanf(s[hash+1:], "%d", &num); err != nil {
		return github.RepoRef{}, 0, fmt.Errorf("parse pr ref %q: %w", s, err)
	}
	return ref, num, nil
}
