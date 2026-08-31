// Package graph wires the review agents together into a single ADK
// workflow agent that orchestrates the code review pipeline.
//
// Graph shape:
//
//	START → triage (LLM) → route ─┬─ "static" ──┐
//	                              ├─ "security"─┼─> gather ─> format_findings ─> summary
//	                              └─ "both" ────┘
//
// With Config.FindingsGate the tail becomes a conditional cycle:
//
//	format_findings → findings_gate ─┬─ (default) → summary
//	                                └─ (revise) → static + security (loop back;
//	                                  gather re-fires and the gate runs again)
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
	"github.com/geoffjay/graph-review/internal/rules"
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
	// Repository rules (if any) are appended after the base instruction.
	// An override replaces the built-in guidance wholesale: the defaults
	// carry the ASD-STE100 style block, and a custom instruction keeps
	// STE output only if it includes those rules itself.
	TriageInstruction   string
	StaticInstruction   string
	SecurityInstruction string
	SummaryInstruction  string

	// RulesDir is the path to repository-specific rules (typically
	// .review/rules). When set, rules matching each agent are appended
	// to that agent's instruction. When empty, no rules are loaded.
	RulesDir string

	// FindingsGate, when true, inserts a human approval gate between
	// format_findings and summary. The gate pauses the run and waits
	// for an approve/revise/abort decision; revise loops the reviewers
	// back with the human's feedback (bounded by the gate's revision
	// cap). Requires an interactive runner surface (the CLI prompts
	// on the terminal) and fails closed without one.
	FindingsGate bool
}

// New assembles the review pipeline as a workflow agent. The returned
// agent is the root of the graph and can be handed to a runner or launcher.
func New(_ context.Context, cfg Config) (agent.Agent, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("graph.Config.Model is required")
	}

	triageInstr := cfg.TriageInstruction
	staticInstr := cfg.StaticInstruction
	securityInstr := cfg.SecurityInstruction
	summaryInstr := cfg.SummaryInstruction

	if cfg.RulesDir != "" {
		loaded, err := rules.Load(cfg.RulesDir)
		if err != nil {
			return nil, fmt.Errorf("load rules: %w", err)
		}
		if len(loaded) > 0 {
			triageInstr = rules.FormatInstruction(
				defaultOr(triageInstr, agents.DefaultTriageInstruction),
				rules.ForAgent(loaded, rules.AgentTriage),
			)
			staticInstr = rules.FormatInstruction(
				defaultOr(staticInstr, agents.DefaultStaticInstruction),
				rules.ForAgent(loaded, rules.AgentStatic),
			)
			securityInstr = rules.FormatInstruction(
				defaultOr(securityInstr, agents.DefaultSecurityInstruction),
				rules.ForAgent(loaded, rules.AgentSecurity),
			)
			summaryInstr = rules.FormatInstruction(
				defaultOr(summaryInstr, agents.DefaultSummaryInstruction),
				rules.ForAgent(loaded, rules.AgentSummary),
			)
		}
	}

	triage, err := agents.NewTriageAgent(cfg.Model, triageInstr)
	if err != nil {
		return nil, fmt.Errorf("build triage agent: %w", err)
	}
	static, err := agents.NewStaticAgent(cfg.Model, staticInstr, cfg.Tools)
	if err != nil {
		return nil, fmt.Errorf("build static agent: %w", err)
	}
	security, err := agents.NewSecurityAgent(cfg.Model, securityInstr, cfg.Tools)
	if err != nil {
		return nil, fmt.Errorf("build security agent: %w", err)
	}
	summary, err := agents.NewSummaryAgent(cfg.Model, summaryInstr)
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

	// reviewer(s) → gather → format → [findings gate] → summary
	//
	// With the findings gate enabled, format feeds the gate and the
	// gate's default route continues to summary. A revise decision
	// emits agents.RouteRevise, whose conditional edges loop back to the
	// reviewer nodes; the cycle is legal because every path through it
	// contains a routed edge, and the reviewers re-running re-fires
	// gather (a join re-evaluates its barrier on each predecessor
	// completion) so the revised findings re-enter the gate.
	eb.AddFanIn(gather, staticNode, securityNode)
	eb.Add(gather, format)
	if cfg.FindingsGate {
		gate := agents.FindingsGateNode()
		eb.Add(format, gate)
		eb.AddRoute(gate, summaryNode, workflow.Default)
		eb.AddRoute(gate, staticNode, workflow.StringRoute(agents.RouteRevise))
		eb.AddRoute(gate, securityNode, workflow.StringRoute(agents.RouteRevise))
	} else {
		eb.Add(format, summaryNode)
	}

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

func defaultOr(val, def string) string {
	if val != "" {
		return val
	}
	return def
}
