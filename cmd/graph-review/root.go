package main

import (
	"github.com/spf13/cobra"
)

func rootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph-review",
		Short: "AI-driven code review tool using agent graphs",
		Long: `graph-review performs automated code reviews using cooperating
AI agents wired together as a graph.`,
	}
	return cmd
}