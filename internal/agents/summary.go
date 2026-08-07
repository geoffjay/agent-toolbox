package agents

import (
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/workflow"
)

// SummaryAgentName is the workflow node and agent name for the summary
// step.
const SummaryAgentName = "summary"

// DefaultSummaryInstruction is the system instruction for the summary
// agent.
const DefaultSummaryInstruction = `You are the summary step of a code review pipeline.

You are given the findings from one or two specialist reviewers (static
analysis and/or security) under clearly labeled sections. Your job is to
aggregate them into a single, human-readable review report.

Produce the report in this exact structure:

## Verdict
One of: Approve, Request changes, or Needs discussion — followed by a
one-sentence justification.

## Findings
List every distinct finding from the reviewers as a bullet, in severity
order (blocker, major, minor, nit, praise). Each bullet starts with the
file and line range, then the severity tag, then the description. De-
duplicate identical findings; if the two reviewers disagree on severity,
take the higher one and note the disagreement inline.

## Top concerns
Up to three bullets naming the issues most worth the author's attention.

Do not invent findings the reviewers did not report. If a reviewer section
is missing or says nothing was found, omit it. Keep the report under 500
words unless the diff is large.`

// NewSummaryAgent builds the summary LLM agent.
func NewSummaryAgent(m model.LLM, instruction string) (agent.Agent, error) {
	if instruction == "" {
		instruction = DefaultSummaryInstruction
	}
	return llmagent.New(llmagent.Config{
		Name:        SummaryAgentName,
		Model:       m,
		Description: "Aggregates reviewer findings into a single human-readable review report.",
		Mode:        llmagent.ModeSingleTurn,
		Instruction: instruction,
	})
}

// FormatFindings turns the JoinNode's map[nodeName]output into the prompt
// consumed by the summary agent. Missing or empty reviewer outputs are
// omitted rather than reported as "(no findings)".
func FormatFindings(_ agent.Context, gathered map[string]any) (string, error) {
	var sb strings.Builder
	for _, section := range []struct{ label, key string }{
		{"Static analysis", StaticAgentName},
		{"Security", SecurityAgentName},
	} {
		text := ""
		if v, ok := gathered[section.key]; ok && v != nil {
			if t := strings.TrimSpace(fmt.Sprint(v)); t != "" {
				text = t
			}
		}
		if text == "" {
			continue
		}
		fmt.Fprintf(&sb, "## %s\n%s\n\n", section.label, text)
	}
	if sb.Len() == 0 {
		return "Both reviewers reported no findings.", nil
	}
	return sb.String(), nil
}

// FormatFindingsNode returns the function node that formats the join
// output for the summary agent.
func FormatFindingsNode() workflow.Node {
	return workflow.NewFunctionNode("format_findings", FormatFindings, workflow.NodeConfig{})
}