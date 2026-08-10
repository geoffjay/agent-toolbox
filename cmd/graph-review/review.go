package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	"github.com/geoffjay/graph-review/internal/agents"
	"github.com/geoffjay/graph-review/internal/graph"
	"github.com/geoffjay/graph-review/internal/rules"
	"github.com/geoffjay/graph-review/internal/tools"
	"google.golang.org/adk/v2/runner"
)

// modelFlags carries the model options shared by every review subcommand.
type modelFlags struct {
	provider  string
	modelName string
	apiKey    string
	baseURL   string
}

// pipelineFlags carries the pipeline-level options shared by every review
// subcommand: per-agent instruction overrides and the no-tools toggle.
type pipelineFlags struct {
	triageInstruction   string
	staticInstruction   string
	securityInstruction string
	summaryInstruction  string
	noTools             bool
	rulesDir            string
}

func reviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Run the code review pipeline",
		Long: `Run the code review pipeline over a diff or a GitHub pull request.

Subcommands:
  review [file]              Review a unified diff from a file or stdin.
  review pr <owner/repo> <n> Review a GitHub pull request by number.`,
	}
	cmd.AddCommand(reviewDiffCmd())
	cmd.AddCommand(reviewPRCmd())
	return cmd
}

// reviewDiffCmd builds the `review [file]` subcommand.
func reviewDiffCmd() *cobra.Command {
	var (
		mf    modelFlags
		pf    pipelineFlags
		repoPath string
		sessionID string
	)

	cmd := &cobra.Command{
		Use:   "diff [file]",
		Short: "Review a unified diff from a file or stdin",
		Long: `Run the code review pipeline over a unified diff.

The diff is read from the file given as an argument, or from stdin when
the argument is "-" or omitted. The reviewer agents can call
repo-inspection tools (read_file, list_files, git_blame, git_log) rooted
at --repo (default: working directory). Use --no-tools to disable them.`,
		Aliases: []string{"file"},
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			diff, err := readDiff(args)
			if err != nil {
				return err
			}
			if strings.TrimSpace(diff) == "" {
				return fmt.Errorf("no diff input to review")
			}

			absRepo, err := filepath.Abs(repoPath)
			if err != nil {
				return fmt.Errorf("resolve repo path: %w", err)
			}

			state := map[string]any{
				tools.RepoPathStateKey: absRepo,
			}
			_, err = runPipeline(ctx, runPipelineInput{
				modelFlags:    mf,
				pipelineFlags: pf,
				diff:          diff,
				sessionID:     sessionID,
				state:         state,
				label:         fmt.Sprintf("reviewing %d bytes of diff with model %s", len(diff), mf.modelName),
				repoRoot:      absRepo,
			})
			return err
		},
	}

	addModelFlags(cmd, &mf)
	addPipelineFlags(cmd, &pf)
	cmd.Flags().StringVar(&repoPath, "repo", ".", "Repository root the review tools operate in")
	cmd.Flags().StringVar(&sessionID, "session", "", "Session ID for the runner (random by default)")

	return cmd
}

// runPipelineInput bundles the inputs to runPipeline.
type runPipelineInput struct {
	modelFlags
	pipelineFlags
	diff      string
	sessionID string
	state     map[string]any
	label     string
	repoRoot  string
}

// runPipeline builds the model, tools, graph, and runner from the given
// input, sends the diff as a user message, and streams the agent output
// to stdout. The state map is seeded into the runner via WithStateDelta
// so the tools can read repo_path / pr_ref at runtime. It returns the
// full text output of the pipeline (concatenated non-thought text parts).
func runPipeline(ctx context.Context, in runPipelineInput) (string, error) {
	m, err := agents.NewModel(ctx, agents.ModelConfig{
		Provider:  agents.Provider(in.provider),
		ModelName: in.modelName,
		APIKey:    in.apiKey,
		BaseURL:   in.baseURL,
	})
	if err != nil {
		return "", fmt.Errorf("build model: %w", err)
	}

	var reviewTools []tool.Tool
	if !in.noTools {
		reviewTools, err = tools.NewTools()
		if err != nil {
			return "", fmt.Errorf("build tools: %w", err)
		}
		prTools, err := tools.NewPRTools()
		if err != nil {
			return "", fmt.Errorf("build PR tools: %w", err)
		}
		reviewTools = append(reviewTools, prTools...)
	}

	rulesDir := in.rulesDir
	if rulesDir == "" && in.repoRoot != "" {
		rulesDir = rules.FindRulesDir(in.repoRoot)
	}

	root, err := graph.New(ctx, graph.Config{
		Model:               m,
		Tools:               reviewTools,
		TriageInstruction:   in.triageInstruction,
		StaticInstruction:   in.staticInstruction,
		SecurityInstruction: in.securityInstruction,
		SummaryInstruction:  in.summaryInstruction,
		RulesDir:            rulesDir,
	})
	if err != nil {
		return "", fmt.Errorf("build pipeline: %w", err)
	}

	r, err := runner.NewInMemory("graph-review", root)
	if err != nil {
		return "", fmt.Errorf("build runner: %w", err)
	}

	userID := "graph-review-cli"
	sessionID := in.sessionID
	if sessionID == "" {
		sessionID = "review-" + time.Now().Format("20060102-150405")
	}

	msg := &genai.Content{
		Parts: []*genai.Part{
			{Text: "Please review the following diff:\n\n```diff\n" + in.diff + "\n```"},
		},
		Role: "user",
	}

	fmt.Fprintln(os.Stderr, in.label)

	var output strings.Builder

	runOpts := []runner.RunOption{
		runner.WithStateDelta(in.state),
	}
	for ev, err := range r.Run(ctx, userID, sessionID, msg, agentRunConfig(), runOpts...) {
		if err != nil {
			return output.String(), err
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part.Text != "" && !part.Thought {
				fmt.Print(part.Text)
				if ev.Author == agents.SummaryAgentName {
					output.WriteString(part.Text)
				}
			}
		}
	}
	fmt.Println()

	report := output.String()
	if warnShallowReview(report, in.diff) {
		fmt.Fprintln(os.Stderr, "\nWARNING: the review produced no findings for a non-trivial diff.")
		fmt.Fprintln(os.Stderr, "This may indicate the model did not thoroughly analyze the code.")
		fmt.Fprintln(os.Stderr, "Consider using a stronger model or reviewing manually.")
	}
	return report, nil
}

// warnShallowReview returns true if the report has no findings and the
// diff is non-trivial (more than 10 changed lines, excluding file headers
// and index lines).
func warnShallowReview(report, diff string) bool {
	if countDiffLines(diff) <= 10 {
		return false
	}
	findings := extractFindingsSection(report)
	if findings == "" {
		return true
	}
	lower := strings.ToLower(findings)
	return strings.Contains(lower, "no findings") ||
		strings.Contains(lower, "none reported") ||
		strings.Contains(lower, "no issues")
}

func countDiffLines(diff string) int {
	count := 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			count++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			count++
		}
	}
	return count
}

func extractFindingsSection(report string) string {
	lines := strings.Split(report, "\n")
	var sb strings.Builder
	capturing := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "## Findings" {
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

// addModelFlags wires the model flags onto a command.
func addModelFlags(cmd *cobra.Command, mf *modelFlags) {
	cmd.Flags().StringVar(&mf.provider, "provider", "", "Model provider: openai or anthropic (env OPENAI_API_KEY/ANTHROPIC_API_KEY)")
	cmd.Flags().StringVarP(&mf.modelName, "model", "m", "", "Model name (env OPENAI_MODEL or ANTHROPIC_MODEL)")
	cmd.Flags().StringVar(&mf.apiKey, "api-key", "", "API key for the model endpoint (env OPENAI_API_KEY or ANTHROPIC_API_KEY)")
	cmd.Flags().StringVar(&mf.baseURL, "base-url", "", "Base URL for the endpoint (env OPENAI_BASE_URL or ANTHROPIC_BASE_URL)")
}

// addPipelineFlags wires the shared pipeline flags onto a command.
func addPipelineFlags(cmd *cobra.Command, pf *pipelineFlags) {
	cmd.Flags().BoolVar(&pf.noTools, "no-tools", false, "Disable repo-inspection and PR tools on the reviewer agents")
	cmd.Flags().StringVar(&pf.rulesDir, "rules-dir", "", "Path to repository rules directory (default: .review/rules relative to repo root)")
	cmd.Flags().StringVar(&pf.triageInstruction, "triage-instruction", "", "Override the triage agent instruction")
	cmd.Flags().StringVar(&pf.staticInstruction, "static-instruction", "", "Override the static analysis agent instruction")
	cmd.Flags().StringVar(&pf.securityInstruction, "security-instruction", "", "Override the security agent instruction")
	cmd.Flags().StringVar(&pf.summaryInstruction, "summary-instruction", "", "Override the summary agent instruction")
}

func readDiff(args []string) (string, error) {
	if len(args) == 0 || args[0] == "-" {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	b, err := os.ReadFile(args[0])
	return string(b), err
}

func agentRunConfig() agent.RunConfig {
	return agent.RunConfig{}
}