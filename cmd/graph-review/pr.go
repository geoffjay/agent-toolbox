package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/geoffjay/graph-review/internal/github"
	"github.com/geoffjay/graph-review/internal/review"
	"github.com/geoffjay/graph-review/internal/tools"
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
		githubToken  string
		sessionID    string
		noClone      bool
		cloneRepo    string
		postComments bool
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
local checkout.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			ref, err := github.ParseRepoRef(args[0])
			if err != nil {
				return err
			}
			number, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid PR number %q: %w", args[1], err)
			}

			if postComments && githubToken == "" && os.Getenv("GITHUB_TOKEN") == "" {
				return fmt.Errorf("--post-comments requires --github-token or GITHUB_TOKEN env var")
			}

			client := github.NewClient(githubToken)

			fmt.Fprintln(os.Stderr, "fetching PR", ref.String()+"#"+args[1])
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
			if !noClone {
				cloneTarget := cloneRepo
				if cloneTarget == "" {
					shortSHA := pr.Head.SHA
					if len(shortSHA) > 7 {
						shortSHA = shortSHA[:7]
					}
					fmt.Fprintln(os.Stderr, "cloning", pr.Head.Repo.FullName, "at", shortSHA)
					dir, cleanup, err := client.CloneRepo(ctx, pr)
					if err != nil {
						return fmt.Errorf("clone PR repo: %w", err)
					}
					defer cleanup()
					cloneTarget = dir
				}
				abs, err := filepath.Abs(cloneTarget)
				if err != nil {
					return fmt.Errorf("resolve clone path: %w", err)
				}
				state[tools.RepoPathStateKey] = abs
			}

			label := fmt.Sprintf("reviewing PR %s#%d (%d bytes) with model %s",
				ref, number, len(diff), mf.modelName)
			if pr.Title != "" {
				label = fmt.Sprintf("reviewing PR %s#%d %q (%d bytes) with model %s",
					ref, number, pr.Title, len(diff), mf.modelName)
			}

			report, err := runPipeline(ctx, runPipelineInput{
				modelFlags:    mf,
				pipelineFlags: pf,
				diff:          diff,
				sessionID:     sessionID,
				state:         state,
				label:         label,
			})
			if err != nil {
				return err
			}

			if postComments {
				if strings.TrimSpace(report) == "" {
					fmt.Fprintln(os.Stderr, "no review output to post")
					return nil
				}
				findings := review.ParseFindings(report)
				req := review.BuildReviewRequest(report, findings)
				fmt.Fprintf(os.Stderr, "posting review (%s, %d inline comments) to %s#%d\n",
					req.Event, len(req.Comments), ref, number)
				resp, err := client.PostReview(ctx, ref, number, req)
				if err != nil {
					return fmt.Errorf("post review: %w", err)
				}
				fmt.Fprintf(os.Stderr, "review posted: %s\n", resp.HTMLURL)
			}

			return nil
		},
	}

	addModelFlags(cmd, &mf)
	addPipelineFlags(cmd, &pf)
	cmd.Flags().StringVar(&githubToken, "github-token", "", "GitHub API token (env GITHUB_TOKEN)")
	cmd.Flags().StringVar(&sessionID, "session", "", "Session ID for the runner (random by default)")
	cmd.Flags().BoolVar(&noClone, "no-clone", false, "Do not clone the PR repo; tools fall back to the working directory")
	cmd.Flags().StringVar(&cloneRepo, "clone-repo", "", "Use an existing local checkout instead of cloning")
	cmd.Flags().BoolVar(&postComments, "post-comments", false, "Post the review as a PR review with inline comments (requires --github-token)")

	return cmd
}
