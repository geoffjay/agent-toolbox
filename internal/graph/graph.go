// Package graph wires the review agents together into a single ADK
// workflow agent that orchestrates the code review pipeline.
//
// Graph shape:
//
//	START → triage (LLM) → route ─┬─ "static" ──┐
//	                              ├─ "security"─┼─> gather ─> format_findings ─> summary
//	                              └─ "both" ────┘
//
// The triage agent emits one of static, security, or both. Each reviewer
// edge uses a MultiRoute so "both" fans out to both reviewers while
// "static"/"security" light up only the relevant one. A JoinNode waits for
// the active reviewer(s), then format_findings and summary run.
package graph

import (
	"context"
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/workflowagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/workflow"

	"github.com/geoffjay/graph-review/internal/agents"
)

// Config holds the options for building the review graph. All LLM agents
// share the same model so a single model construction is reused.
type Config struct {
	Model model.LLM

	// Tools available to the reviewer agents (static and security).
	// When nil, the reviewers run without tools. The triage and summary
	// agents never receive tools — triage only classifies and summary
	// only aggregates reviewer output.
	Tools []tool.Tool

	// Optional instruction overrides, one per agent. When empty the
	// corresponding DefaultXxxInstruction from the agents package is used.
	TriageInstruction    string
	StaticInstruction    string
	SecurityInstruction  string
	SummaryInstruction   string
}

// New assembles the review pipeline as a workflow agent. The returned
// agent is the root of the graph and can be handed to a runner or launcher.
func New(_ context.Context, cfg Config) (agent.Agent, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("graph.Config.Model is required")
	}

	triage, err := agents.NewTriageAgent(cfg.Model, cfg.TriageInstruction)
	if err != nil {
		return nil, fmt.Errorf("build triage agent: %w", err)
	}
	static, err := agents.NewStaticAgent(cfg.Model, cfg.StaticInstruction, cfg.Tools)
	if err != nil {
		return nil, fmt.Errorf("build static agent: %w", err)
	}
	security, err := agents.NewSecurityAgent(cfg.Model, cfg.SecurityInstruction, cfg.Tools)
	if err != nil {
		return nil, fmt.Errorf("build security agent: %w", err)
	}
	summary, err := agents.NewSummaryAgent(cfg.Model, cfg.SummaryInstruction)
	if err != nil {
		return nil, fmt.Errorf("build summary agent: %w", err)
	}

	triageNode, err := workflow.NewAgentNode(triage, workflow.NodeConfig{})
	if err != nil {
		return nil, fmt.Errorf("build triage node: %w", err)
	}
	routeNode := agents.TriageRouteNode()
	staticNode, err := workflow.NewAgentNode(static, workflow.NodeConfig{})
	if err != nil {
		return nil, fmt.Errorf("build static node: %w", err)
	}
	securityNode, err := workflow.NewAgentNode(security, workflow.NodeConfig{})
	if err != nil {
		return nil, fmt.Errorf("build security node: %w", err)
	}
	gather := workflow.NewJoinNode("gather")
	format := agents.FormatFindingsNode()
	summaryNode, err := workflow.NewAgentNode(summary, workflow.NodeConfig{})
	if err != nil {
		return nil, fmt.Errorf("build summary node: %w", err)
	}

	// START → triage → route → {static, security, both}
	//
	// MultiRoute lets the "both" category fan out to both reviewers with a
	// single edge per target, avoiding duplicate (from,to) edges.
	eb := workflow.NewEdgeBuilder()
	eb.Add(workflow.Start, triageNode)
	eb.Add(triageNode, routeNode)
	eb.AddRoute(routeNode, staticNode, workflow.MultiRoute[string]{
		agents.RouteStatic, agents.RouteBoth,
	})
	eb.AddRoute(routeNode, securityNode, workflow.MultiRoute[string]{
		agents.RouteSecurity, agents.RouteBoth,
	})

	// reviewer(s) → gather → format → summary
	eb.AddFanIn(gather, staticNode, securityNode)
	eb.Add(gather, format)
	eb.Add(format, summaryNode)

	root, err := workflowagent.New(workflowagent.Config{
		Name:        "review_pipeline",
		Description: "Triage a diff, run the relevant reviewers, and summarize the findings.",
		Edges:       eb.Build(),
		SubAgents:   []agent.Agent{triage, static, security, summary},
	})
	if err != nil {
		return nil, fmt.Errorf("build review pipeline: %w", err)
	}
	return root, nil
}