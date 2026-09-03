package agents

import (
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/workflow"
)

// TriageAgentName is the workflow node and agent name for the triage step.
const TriageAgentName = "triage"

// Route categories the triage agent may classify a diff into. The
// reviewer orchestrator selects nodes from them.
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

// TriageCategoryStateKey is the session state key holding the normalized
// triage category. The reviewer orchestrator reads it on every
// activation — including findings-gate revise rounds, when it is reached
// again through the gate's loop-back edge — to know which reviewers to
// run.
const TriageCategoryStateKey = "triage_category"

// NormalizeTriageCategory maps a triage reply to a routing category.
// Unknown or off-script replies fall through to "both" so nothing is
// skipped.
func NormalizeTriageCategory(reply string) string {
	category := strings.TrimRight(strings.ToLower(strings.TrimSpace(reply)), ".")
	switch category {
	case RouteStatic, RouteSecurity, RouteBoth:
		return category
	default:
		return RouteBoth
	}
}

// TriageCategory reads the triage agent's one-word classification,
// records it in session state, and returns the original user message
// text so the downstream reviewer orchestrator (which feeds on its
// predecessor's output, not on ctx.UserContent) receives the diff to
// review.
func TriageCategory(ctx agent.Context, input any) (string, error) {
	category := NormalizeTriageCategory(fmt.Sprint(input))
	if err := ctx.State().Set(TriageCategoryStateKey, category); err != nil {
		return "", fmt.Errorf("record triage category: %w", err)
	}
	return userMessageText(ctx), nil
}

// TriageCategoryNode returns the function node that records the triage
// category and passes the review request to the reviewers.
func TriageCategoryNode() workflow.Node {
	return workflow.NewFunctionNode("route", TriageCategory, workflow.NodeConfig{})
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
