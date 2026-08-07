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
	"github.com/geoffjay/graph-review/internal/tools"
	"google.golang.org/adk/v2/runner"
)

// modelFlags carries the OpenAI-compatible model options shared by every
// review subcommand.
type modelFlags struct {
	modelName string
	apiKey    string
	baseURL   string
}

// pipelineFlags carries the pipeline-level options shared by every review
// subcommand: per-agent instruction overrides and the no-tools toggle.
type pipelineFlags struct {
	triageInstruction    string
	staticInstruction    string
	securityInstruction  string
	summaryInstruction   string
	noTools              bool
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
			return runPipeline(ctx, runPipelineInput{
				modelFlags:  mf,
				pipelineFlags: pf,
				diff:        diff,
				sessionID:   sessionID,
				state:       state,
				label:       fmt.Sprintf("reviewing %d bytes of diff with model %s", len(diff), mf.modelName),
			})
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
	diff       string
	sessionID  string
	state      map[string]any
	label      string
}

// runPipeline builds the model, tools, graph, and runner from the given
// input, sends the diff as a user message, and streams the agent output
// to stdout. The state map is seeded into the runner via WithStateDelta
// so the tools can read repo_path / pr_ref at runtime.
func runPipeline(ctx context.Context, in runPipelineInput) error {
	m, err := agents.NewModel(ctx, agents.ModelConfig{
		ModelName: in.modelName,
		APIKey:    in.apiKey,
		BaseURL:   in.baseURL,
	})
	if err != nil {
		return fmt.Errorf("build model: %w", err)
	}

	var reviewTools []tool.Tool
	if !in.noTools {
		reviewTools, err = tools.NewTools()
		if err != nil {
			return fmt.Errorf("build tools: %w", err)
		}
		prTools, err := tools.NewPRTools()
		if err != nil {
			return fmt.Errorf("build PR tools: %w", err)
		}
		reviewTools = append(reviewTools, prTools...)
	}

	root, err := graph.New(ctx, graph.Config{
		Model:               m,
		Tools:               reviewTools,
		TriageInstruction:   in.triageInstruction,
		StaticInstruction:   in.staticInstruction,
		SecurityInstruction: in.securityInstruction,
		SummaryInstruction:  in.summaryInstruction,
	})
	if err != nil {
		return fmt.Errorf("build pipeline: %w", err)
	}

	r, err := runner.NewInMemory("graph-review", root)
	if err != nil {
		return fmt.Errorf("build runner: %w", err)
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

	runOpts := []runner.RunOption{
		runner.WithStateDelta(in.state),
	}
	for ev, err := range r.Run(ctx, userID, sessionID, msg, agentRunConfig(), runOpts...) {
		if err != nil {
			return err
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part.Text != "" && !part.Thought {
				fmt.Print(part.Text)
			}
		}
	}
	fmt.Println()
	return nil
}

// addModelFlags wires the OpenAI-compatible model flags onto a command.
func addModelFlags(cmd *cobra.Command, mf *modelFlags) {
	cmd.Flags().StringVarP(&mf.modelName, "model", "m", "", "OpenAI-compatible model name (env OPENAI_MODEL)")
	cmd.Flags().StringVar(&mf.apiKey, "api-key", "", "API key for the model endpoint (env OPENAI_API_KEY)")
	cmd.Flags().StringVar(&mf.baseURL, "base-url", "", "OpenAI-compatible base URL (env OPENAI_BASE_URL)")
}

// addPipelineFlags wires the shared pipeline flags onto a command.
func addPipelineFlags(cmd *cobra.Command, pf *pipelineFlags) {
	cmd.Flags().BoolVar(&pf.noTools, "no-tools", false, "Disable repo-inspection and PR tools on the reviewer agents")
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