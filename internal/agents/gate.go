package agents

import (
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/workflow"
)

// FindingsGateName is the workflow node name for the human approval gate
// that sits between format_findings and summary.
const FindingsGateName = "findings_gate"

// RouteRevise is the routing key the findings gate emits when the human
// declines the findings and sends them back for revision. The graph wires
// it as a conditional edge from the gate back to the reviewer nodes, which
// makes the approve/revise cycle legal (conditional edges only).
const RouteRevise = "revise"

// Gate decisions returned by the human.
const (
	DecisionApprove = "approve"
	DecisionRevise  = "revise"
	DecisionAbort   = "abort"
)

// MaxFindingsRevisions bounds the approve/revise loop. The engine blocks
// only unconditional cycles; a conditional loop can otherwise run forever,
// so the gate counts its own revisions and fails loudly past this cap.
const MaxFindingsRevisions = 3

// findingsRevisionsStateKey is the session state key holding the number of
// revisions requested so far in this run.
const findingsRevisionsStateKey = "findings_gate_revisions"

// FindingsGate is an EmittingFunctionNode handler that pauses the pipeline
// after the reviewers' findings are formatted and asks a human to approve
// them. The reply must be a map with a "decision" key (approve, revise, or
// abort) and an optional "feedback" string; a plain decision string is also
// accepted.
//
//	approve → the findings flow on to the summary agent (default route)
//	revise  → the gate emits RouteRevise and returns a new reviewer prompt
//	          (original diff + prior findings + human feedback); the routed
//	          edges loop back to the reviewer nodes
//	abort   → the run fails with a descriptive error
//
// More than MaxFindingsRevisions revisions fail the run. The node runs
// under RerunOnResume so the reply is visible here on re-entry.
func FindingsGate(ctx agent.Context, input any, emit func(*session.Event) error) (any, error) {
	reply, err := workflow.ResumeOrRequestInput(ctx, emit, session.RequestInput{
		// Unique per run: the runner correlates the resume response by
		// this ID, and reusing a fixed literal breaks later pauses in
		// the same session.
		InterruptID: FindingsGateName + "-" + ctx.InvocationID(),
		Message:     "Approve the reviewer findings, or send them back for revision?",
		Payload:     input,
	})
	if err != nil {
		return nil, err
	}

	decision, feedback, err := parseGateDecision(reply)
	if err != nil {
		return nil, err
	}

	switch decision {
	case DecisionAbort:
		return nil, fmt.Errorf("findings gate: review aborted by human")
	case DecisionRevise:
		revisions, err := bumpRevisions(ctx)
		if err != nil {
			return nil, err
		}
		if revisions > MaxFindingsRevisions {
			return nil, fmt.Errorf("findings gate: revision cap of %d exceeded", MaxFindingsRevisions)
		}
		ev := session.NewEvent(ctx, ctx.InvocationID())
		ev.Routes = []string{RouteRevise}
		if err := emit(ev); err != nil {
			return nil, err
		}
		return RevisePrompt(userMessageText(ctx), fmt.Sprint(input), feedback), nil
	default:
		// approve: findings flow to the next node unchanged
		return input, nil
	}
}

// FindingsGateNode returns the findings approval gate node.
func FindingsGateNode() workflow.Node {
	rerun := true
	return workflow.NewEmittingFunctionNode(FindingsGateName, FindingsGate,
		workflow.NodeConfig{RerunOnResume: &rerun})
}

// parseGateDecision extracts the decision and feedback from a human reply.
// It accepts a map with "decision"/"feedback" keys (the shape the CLI
// sends) or a bare decision string. Anything else fails closed.
func parseGateDecision(reply any) (decision, feedback string, err error) {
	switch v := reply.(type) {
	case map[string]any:
		d, _ := v["decision"].(string)
		f, _ := v["feedback"].(string)
		decision, feedback = strings.ToLower(strings.TrimSpace(d)), strings.TrimSpace(f)
	case string:
		decision = strings.ToLower(strings.TrimSpace(v))
	default:
		return "", "", fmt.Errorf("findings gate: unreadable reply %T", reply)
	}
	switch decision {
	case DecisionApprove, DecisionRevise, DecisionAbort:
		return decision, feedback, nil
	default:
		return "", "", fmt.Errorf("findings gate: unknown decision %q (want approve, revise, or abort)", decision)
	}
}

// bumpRevisions increments and returns the per-run revision counter in
// session state. The value may round-trip through JSON as a float, so the
// stored type is normalized on read.
func bumpRevisions(ctx agent.Context) (int, error) {
	var n int
	if v, err := ctx.State().Get(findingsRevisionsStateKey); err == nil {
		switch c := v.(type) {
		case int:
			n = c
		case int64:
			n = int(c)
		case float64:
			n = int(c)
		}
	}
	n++
	if err := ctx.State().Set(findingsRevisionsStateKey, n); err != nil {
		return 0, fmt.Errorf("findings gate: record revision: %w", err)
	}
	return n, nil
}

// RevisePrompt composes the reviewer prompt for a revision round: the
// original review request (which carries the diff), the prior findings,
// and the human's feedback, clearly delimited so the reviewers — whose
// instructions assume they receive a unified diff — keep their structure.
//
// The human feedback and prior findings are concatenated directly into
// the reviewer LLM prompt, a prompt-injection surface. This is safe only
// because both inputs are operator-trusted: feedback is typed by the
// local operator at the interactive gate, and findings are produced by
// this pipeline's own reviewers. Do not reuse this with untrusted input.
func RevisePrompt(userMessage, findings, feedback string) string {
	var b strings.Builder
	b.WriteString(userMessage)
	b.WriteString("\n\n## Prior review round\n\nThe findings below were rejected by the human reviewer. Re-review the diff and produce a corrected findings report, addressing the feedback.\n\n")
	b.WriteString(findings)
	b.WriteString("\n\n## Human feedback\n\n")
	b.WriteString(feedback)
	return b.String()
}
