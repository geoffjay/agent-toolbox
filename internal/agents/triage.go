package agents

import (
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/workflow"
)

// TriageAgentName is the workflow node and agent name for the triage step.
const TriageAgentName = "triage"

// Route categories emitted by the triage agent. These map to downstream
// reviewer nodes via workflow.StringRoute.
const (
	RouteStatic   = "static"
	RouteSecurity = "security"
	RouteBoth     = "both"
)

// DefaultTriageInstruction is the system instruction for the triage agent.
const DefaultTriageInstruction = `You are the triage step of a code review pipeline.

Given a unified diff, decide which specialist reviewers should examine it:

- "static"   : only static analysis is needed (style, formatting, common
              anti-patterns, minor bugs). Use this for small, low-risk
              changes with no security surface.
- "security" : only security review is needed (vulnerabilities, unsafe
              patterns, secret handling, privilege issues). Use this when
              the change touches auth, crypto, file/network IO, or input
              parsing but is otherwise mechanically simple.
- "both"     : both static and security review are needed. Use this for
              any non-trivial change, anything touching IO or input
              handling, or whenever you are unsure.

Answer with EXACTLY one lowercase word: static, security, or both. No
punctuation, no explanation.`

// NewTriageAgent builds the triage LLM agent that classifies a diff and
// routes to the appropriate reviewers.
func NewTriageAgent(m model.LLM, instruction string) (agent.Agent, error) {
	if instruction == "" {
		instruction = DefaultTriageInstruction
	}
	agent, err := llmagent.New(llmagent.Config{
		Name:                 TriageAgentName,
		Model:                m,
		Description:          "Classifies a diff and routes it to the appropriate reviewers.",
		Mode:                 llmagent.ModeSingleTurn,
		Instruction:          instruction,
		OutputKey:            "triage_category",
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{EnsureUserContent(TriageAgentName)},
	})
	if err != nil {
		return nil, fmt.Errorf("build triage agent: %w", err)
	}
	return agent, nil
}

// RouteByTriage is an EmittingFunctionNode handler that reads the triage
// agent's one-word classification and emits a routing event. Unknown or
// off-script replies fall through to "both" so nothing is skipped.
//
// It returns the original user message text so that downstream AgentNode
// reviewers (which feed on their predecessor's output, not on
// ctx.UserContent) receive the diff to review.
func RouteByTriage(ctx agent.Context, input any, emit func(*session.Event) error) (any, error) {
	category := strings.TrimRight(strings.ToLower(strings.TrimSpace(fmt.Sprint(input))), ".")
	switch category {
	case RouteStatic, RouteSecurity, RouteBoth:
	default:
		category = RouteBoth
	}
	ev := session.NewEvent(ctx, ctx.InvocationID())
	ev.Routes = []string{category}
	if err := emit(ev); err != nil {
		return nil, err
	}
	return userMessageText(ctx), nil
}

// userMessageText reads the original user text from ctx.UserContent.
func userMessageText(ctx agent.Context) string {
	uc := ctx.UserContent()
	if uc == nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range uc.Parts {
		sb.WriteString(p.Text)
	}
	return strings.TrimSpace(sb.String())
}

// TriageRouteNode returns the emitting function node that turns the triage
// agent output into a workflow route.
func TriageRouteNode() workflow.Node {
	return workflow.NewEmittingFunctionNode("route", RouteByTriage, workflow.NodeConfig{})
}
