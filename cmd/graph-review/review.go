package main

import (
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

func reviewCmd() *cobra.Command {
	var (
		modelName           string
		apiKey              string
		baseURL             string
		sessionID           string
		repoPath            string
		noTools             bool
		// Per-agent instruction overrides.
		triageInstruction   string
		staticInstruction   string
		securityInstruction string
		summaryInstruction  string
	)

	cmd := &cobra.Command{
		Use:   "review [file]",
		Short: "Run the code review pipeline on a diff",
		Long: `Run the code review pipeline over a unified diff.

The pipeline triages the diff, dispatches the relevant reviewers (static
analysis and/or security), and summarizes their findings into a single
report.

The diff is read from the file given as an argument, or from stdin when
the argument is "-" or omitted. The pipeline uses an OpenAI-compatible
model configured via the flags below or the OPENAI_MODEL, OPENAI_API_KEY
and OPENAI_BASE_URL environment variables.

The reviewer agents can call repo-inspection tools (read_file,
list_files, git_blame, git_log) rooted at --repo (default: working
directory). Use --no-tools to disable them.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			diff, err := readDiff(args)
			if err != nil {
				return err
			}
			if strings.TrimSpace(diff) == "" {
				return fmt.Errorf("no diff input to review")
			}

		root, err := filepath.Abs(repoPath)
		if err != nil {
			return fmt.Errorf("resolve repo path: %w", err)
		}
		repoPath = root

			m, err := agents.NewModel(ctx, agents.ModelConfig{
				ModelName: modelName,
				APIKey:    apiKey,
				BaseURL:   baseURL,
			})
			if err != nil {
				return fmt.Errorf("build model: %w", err)
			}

			var reviewTools []tool.Tool
			if !noTools {
				reviewTools, err = tools.NewTools()
				if err != nil {
					return fmt.Errorf("build tools: %w", err)
				}
			}

			agent, err := graph.New(ctx, graph.Config{
				Model:               m,
				Tools:               reviewTools,
				TriageInstruction:   triageInstruction,
				StaticInstruction:   staticInstruction,
				SecurityInstruction: securityInstruction,
				SummaryInstruction:  summaryInstruction,
			})
			if err != nil {
				return fmt.Errorf("build pipeline: %w", err)
			}

			r, err := runner.NewInMemory("graph-review", agent)
			if err != nil {
				return fmt.Errorf("build runner: %w", err)
			}

			userID := "graph-review-cli"
			if sessionID == "" {
				sessionID = "review-" + time.Now().Format("20060102-150405")
			}

			msg := &genai.Content{
				Parts: []*genai.Part{
					{Text: "Please review the following diff:\n\n```diff\n" + diff + "\n```"},
				},
				Role: "user",
			}

			fmt.Fprintln(os.Stderr, "reviewing", len(diff), "bytes of diff with model", modelName)

			runOpts := []runner.RunOption{
				runner.WithStateDelta(map[string]any{
					tools.RepoPathStateKey: repoPath,
				}),
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
		},
	}

	cmd.Flags().StringVarP(&modelName, "model", "m", "", "OpenAI-compatible model name (env OPENAI_MODEL)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key for the model endpoint (env OPENAI_API_KEY)")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "OpenAI-compatible base URL (env OPENAI_BASE_URL)")
	cmd.Flags().StringVar(&sessionID, "session", "", "Session ID for the runner (random by default)")
	cmd.Flags().StringVar(&repoPath, "repo", ".", "Repository root the review tools operate in")
	cmd.Flags().BoolVar(&noTools, "no-tools", false, "Disable repo-inspection tools on the reviewer agents")
	cmd.Flags().StringVar(&triageInstruction, "triage-instruction", "", "Override the triage agent instruction")
	cmd.Flags().StringVar(&staticInstruction, "static-instruction", "", "Override the static analysis agent instruction")
	cmd.Flags().StringVar(&securityInstruction, "security-instruction", "", "Override the security agent instruction")
	cmd.Flags().StringVar(&summaryInstruction, "summary-instruction", "", "Override the summary agent instruction")

	return cmd
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