package cmd

import (
	"bufio"
	"fmt"
	"io"
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
		assumeYes    bool
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

Submitting a review with --post-comments first shows the full review body
and inline comments and asks for confirmation on the terminal; nothing is
posted unless you approve. Pass --assume-yes to post unattended (CI).`,
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
			var repoRoot string
			switch {
			case noClone:
				// Tools fall back to the working directory.
				repoRoot, _ = os.Getwd()
			case cloneRepo != "":
				abs, err := filepath.Abs(cloneRepo)
				if err != nil {
					return fmt.Errorf("resolve clone path: %w", err)
				}
				repoRoot = abs
			default:
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
				repoRoot = dir
			}
			if !noClone {
				state[tools.RepoPathStateKey] = repoRoot
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
				repoRoot:      repoRoot,
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
				// Drop findings anchored outside the PR diff; the API
				// would reject the whole review over them.
				if files, err := client.ListFiles(ctx, ref, number); err != nil {
					fmt.Fprintln(os.Stderr, "warning: could not list PR files; posting findings unvalidated:", err)
				} else {
					before := len(findings)
					findings = review.FilterByFiles(findings, files)
					if dropped := before - len(findings); dropped > 0 {
						fmt.Fprintf(os.Stderr, "dropping %d finding(s) whose file is not part of the PR diff\n", dropped)
					}
				}
				req := review.BuildReviewRequest(report, findings)
				confirmed, err := confirmPost(req, ref, number, assumeYes)
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Fprintln(os.Stderr, "review not posted")
					return nil
				}
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
	cmd.Flags().BoolVar(&assumeYes, "assume-yes", false, "Skip the human confirmation prompt before posting (for CI)")

	return cmd
}

// confirmPost shows the exact review that would be submitted and requires
// explicit human approval before posting. With assumeYes it skips the
// prompt (for CI). When stdin is not an interactive terminal and assumeYes
// is not set, it fails closed rather than posting unattended.
func confirmPost(req *github.PostReviewRequest, ref github.RepoRef, number int, assumeYes bool) (bool, error) {
	fmt.Fprintf(os.Stderr, "review to submit to %s#%d\n", ref, number)
	fmt.Fprintf(os.Stderr, "  event: %s (%d inline comment(s))\n", req.Event, len(req.Comments))
	fmt.Fprintln(os.Stderr, "  body:")
	fmt.Fprintln(os.Stderr, req.Body)
	for i, c := range req.Comments {
		fmt.Fprintf(os.Stderr, "  comment %d — %s:%d:\n%s\n", i+1, c.Path, c.Line, c.Body)
	}
	if assumeYes {
		return true, nil
	}
	stat, err := os.Stdin.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		return false, fmt.Errorf("cannot prompt for approval: stdin is not a terminal; re-run interactively or pass --assume-yes")
	}
	return readApproval(os.Stdin, os.Stderr)
}

// readApproval prompts on out and reads a yes/no answer from in. Any
// answer other than y/yes declines; an unreadable input (EOF with no
// answer) is an error so that a closed pipe can never be read as silent
// approval.
func readApproval(in io.Reader, out io.Writer) (bool, error) {
	fmt.Fprint(out, "post this review? [y/N]: ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
