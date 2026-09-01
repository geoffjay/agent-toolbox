// Package cmd implements the agent-toolbox command-line interface.
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var cliCmd = &cobra.Command{
	Use:   "agent-toolbox",
	Short: "AI-driven code review tool using agent graphs",
	Long: `agent-toolbox performs automated code reviews using cooperating
AI agents wired together as a graph.`,
}

// Execute runs the root command.
func Execute() {
	// Propagate SIGINT/SIGTERM into cmd.Context() so the pipeline, git
	// subprocesses, and HTTP calls all cancel cleanly on Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cliCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	cliCmd.AddCommand(reviewCmd())
}
