package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/geoffjay/graph-review/internal/agents"
	"github.com/geoffjay/graph-review/internal/github"
	"github.com/geoffjay/graph-review/internal/review"
	"github.com/geoffjay/graph-review/internal/tools"
	"github.com/geoffjay/graph-review/internal/ui"
)

// reviewPRCmd builds the `review pr <owner/repo> <number>` subcommand.
// It fetches the PR diff from the GitHub REST API, clones the head repo
// into a temp dir so the repo-inspection tools have code to look at, and
// seeds the PR ref into session state so the pr_files/pr_comments/
// pr_reviews tools work.
func reviewPRCmd() *cobra.Command {
	var (
		mf           modelFlags
		pf           pipelineFlags
		lf           loggingFlags
		githubToken  string
		sessionID    string
		noClone      bool
		cloneRepo    string
		postComments bool
		assumeYes    bool
		plain        bool
	)

	cmd := &cobra.Command{
		Use:   "pr <owner/repo> <number>",
		Short: "Review a GitHub pull request by number",
		Long: `Fetch a GitHub pull request's diff and run the code review pipeline over it.

The PR is identified by owner/repo and number, e.g.:

  graph-review review pr geoffjay/graph-review 42

Authentication uses --github-token or the GITHUB_TOKEN env var. Without a
token, only public repos work and the API is rate-limited to 60 req/hr.

By default the head repo is shallow-cloned into a temp directory so the
repo-inspection tools (read_file, git_blame, git_log) can examine code
beyond the diff hunks. Use --no-clone to skip the clone (tools then fall
back to the working directory) or --clone-repo to point at an existing
local checkout.

Submitting a review with --post-comments first shows the review and
inline comments and asks for confirmation; nothing is posted unless you
approve. Pass --assume-yes to post unattended (CI).

Progress is shown in a terminal interface when stdout is a terminal;
pass --plain for the classic streaming output.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			ref, err := github.ParseRepoRef(args[0])
			if err != nil {
				return fmt.Errorf("parse repository ref: %w", err)
			}
			number, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid PR number %q: %w", args[1], err)
			}

			if postComments && githubToken == "" && os.Getenv("GITHUB_TOKEN") == "" {
				return fmt.Errorf("--post-comments requires --github-token or GITHUB_TOKEN env var")
			}

			client := github.NewClient(githubToken)

			work := func(ctx context.Context, p ui.Presenter) error {
				return reviewPR(ctx, client, ref, number, runPipelineInput{
					modelFlags:    mf,
					pipelineFlags: pf,
					sessionID:     sessionID,
					noClone:       noClone,
				}, prOptions{
					cloneRepo:    cloneRepo,
					postComments: postComments,
					assumeYes:    assumeYes,
				}, p)
			}
			// TODO(reusable-pipelines step 4): the report agent is
			// hardcoded to the review summary. Replace SummaryAgentName
			// with the active pipeline's ReportAgent() once the pipeline
			// registry lands.
			return ui.Dispatch(ctx, lf.level(), plain, agents.SummaryAgentName, work)
		},
	}

	addModelFlags(cmd, &mf)
	addLoggingFlags(cmd, &lf)
	addPipelineFlags(cmd, &pf)
	addUIFlags(cmd, &plain)
	cmd.Flags().StringVar(&githubToken, "github-token", "", "GitHub API token (env GITHUB_TOKEN)")
	cmd.Flags().StringVar(&sessionID, "session", "", "Session ID for the runner (random by default)")
	cmd.Flags().BoolVar(&noClone, "no-clone", false, "Do not clone the PR repo; tools fall back to the working directory")
	cmd.Flags().StringVar(&cloneRepo, "clone-repo", "", "Use an existing local checkout instead of cloning")
	cmd.Flags().BoolVar(&postComments, "post-comments", false, "Post the review as a PR review with inline comments (requires --github-token)")
	cmd.Flags().BoolVar(&assumeYes, "assume-yes", false, "Skip the human confirmation prompt before posting (for CI)")
	return cmd
}

// prOptions carries the fetch-and-post toggles that configure reviewPR
// but are not part of the pipeline input itself.
type prOptions struct {
	cloneRepo    string // --clone-repo: use an existing checkout instead of cloning
	postComments bool   // --post-comments: post the review after confirmation
	assumeYes    bool   // --assume-yes: skip the confirmation prompt (CI)
}

// reviewPR fetches the PR (and clones the repo when needed), runs the
// pipeline over its diff, presents the report, and optionally posts it
// as a GitHub review after human confirmation.
func reviewPR(ctx context.Context, client *github.Client, ref github.RepoRef, number int, in runPipelineInput, opts prOptions, p ui.Presenter) error {
	p.Milestone(fmt.Sprintf("fetching PR %s#%d", ref, number))
	pr, err := client.GetPR(ctx, ref, number)
	if err != nil {
		return fmt.Errorf("fetch PR: %w", err)
	}

	diff, err := client.GetDiff(ctx, ref, number)
	if err != nil {
		return fmt.Errorf("fetch PR diff: %w", err)
	}
	if strings.TrimSpace(diff) == "" {
		return fmt.Errorf("PR %s#%d has no diff", ref, number)
	}

	state := map[string]any{
		tools.PRRefStateKey: fmt.Sprintf("%s#%d", ref, number),
	}

	// Resolve a repo root for the repo-inspection tools.
	var repoRoot string
	switch {
	case in.noClone:
		// Tools fall back to the working directory.
		repoRoot, _ = os.Getwd()
	case opts.cloneRepo != "":
		abs, err := filepath.Abs(opts.cloneRepo)
		if err != nil {
			return fmt.Errorf("resolve clone path: %w", err)
		}
		repoRoot = abs
	default:
		shortSHA := pr.Head.SHA
		if len(shortSHA) > 7 {
			shortSHA = shortSHA[:7]
		}
		p.Milestone(fmt.Sprintf("cloning %s at %s", pr.Head.Repo.FullName, shortSHA))
		dir, cleanup, err := client.CloneRepo(ctx, pr)
		if err != nil {
			return fmt.Errorf("clone PR repo: %w", err)
		}
		defer cleanup()
		repoRoot = dir
	}
	if !in.noClone {
		state[tools.RepoPathStateKey] = repoRoot
	}

	label := fmt.Sprintf("reviewing PR %s#%d (%d bytes) with model %s",
		ref, number, len(diff), in.modelName)
	if pr.Title != "" {
		label = fmt.Sprintf("reviewing PR %s#%d %q (%d bytes) with model %s",
			ref, number, pr.Title, len(diff), in.modelName)
	}
	in.diff = diff
	in.state = state
	in.label = label
	in.repoRoot = repoRoot
	report, err := runPipeline(ctx, in, p)
	if err != nil {
		return fmt.Errorf("run pipeline: %w", err)
	}
	p.Finish(report)

	if opts.postComments {
		if err := postReview(ctx, client, ref, number, report, opts.assumeYes, p); err != nil {
			return fmt.Errorf("post review: %w", err)
		}
	}
	return nil
}

// postReview filters the pipeline's findings against the PR's diff
// (dropping any whose file or line cannot be anchored), prompts for human
// confirmation, and posts the review. If the PR file list cannot be
// fetched it warns and does not post anything.
func postReview(ctx context.Context, client *github.Client, ref github.RepoRef, number int, report string, assumeYes bool, p ui.Presenter) error {
	if strings.TrimSpace(report) == "" {
		p.Note("no review output to post")
		return nil
	}
	findings := review.ParseFindings(report)
	// Drop findings anchored outside the PR diff; the API
	// would reject the whole review over them.
	files, err := client.ListFiles(ctx, ref, number)
	if err != nil {
		p.Warn(fmt.Sprintf("warning: could not list PR files; not posting the review: %v", err))
		return nil
	}
	before := len(findings)
	findings = review.FilterByFiles(findings, files)
	if dropped := before - len(findings); dropped > 0 {
		p.Warn(fmt.Sprintf("dropping %d finding(s) whose file is not part of the PR diff", dropped))
	}
	before = len(findings)
	findings = review.FilterByDiffLines(findings, files)
	if dropped := before - len(findings); dropped > 0 {
		p.Warn(fmt.Sprintf("dropping %d finding(s) whose line is not part of the PR diff hunks", dropped))
	}
	req := review.BuildReviewRequest(report, findings)
	confirmed := assumeYes
	if !confirmed {
		confirmed, err = p.Confirm(postConfirmation(ref, number, req))
		if err != nil {
			return fmt.Errorf("confirm post: %w", err)
		}
	}
	if !confirmed {
		p.Note("review not posted")
		return nil
	}
	p.Milestone("posting review to GitHub")
	resp, err := client.PostReview(ctx, ref, number, req)
	if err != nil {
		// A residual anchor rejection (422 "Line could not be
		// resolved") should not lose the human-approved review:
		// post the body without inline comments rather than fail.
		var apiErr *github.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnprocessableEntity && len(req.Comments) > 0 {
			p.Warn("warning: GitHub rejected an inline comment anchor; posting the review without inline comments")
			req.Comments = nil
			resp, err = client.PostReview(ctx, ref, number, req)
		}
	}
	if err != nil {
		return fmt.Errorf("submit review: %w", err)
	}
	p.Note(fmt.Sprintf("review posted: %s", resp.HTMLURL))
	return nil
}

// postConfirmation builds the confirmation shown before a review is
// submitted to GitHub.
func postConfirmation(ref github.RepoRef, number int, req *github.PostReviewRequest) ui.Confirmation {
	var b strings.Builder
	fmt.Fprintf(&b, "event: %s", req.Event)
	if len(req.Comments) == 1 {
		b.WriteString(" (1 inline comment)")
	} else {
		fmt.Fprintf(&b, " (%d inline comments)", len(req.Comments))
	}
	for i, c := range req.Comments {
		fmt.Fprintf(&b, "\n  comment %d — %s:%d", i+1, c.Path, c.Line)
	}
	return ui.Confirmation{
		Title:  fmt.Sprintf("post this review to %s#%d?", ref, number),
		Detail: b.String(),
		Body:   req.Body,
	}
}
