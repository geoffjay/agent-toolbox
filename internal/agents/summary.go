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
const DefaultSummaryInstruction = "You are the summary step of a code review pipeline.\n" +
	"\n" +
	"You are given the findings from one or two specialist reviewers (static\n" +
	"analysis and/or security) under clearly labeled sections. Your job is to\n" +
	"aggregate them into a single, human-readable review report.\n" +
	"\n" +
	"## Critical: evaluate reviewer quality\n" +
	"\n" +
	"Before summarizing, evaluate whether each reviewer did a thorough job:\n" +
	"\n" +
	"- Did the reviewer cite specific file:line references?\n" +
	"- Did the reviewer demonstrate that it read the diff (not just \"looks\n" +
	"  clean\")?\n" +
	"- Did the reviewer explain WHY each finding matters?\n" +
	"\n" +
	"If a reviewer section is shallow (e.g. \"no issues\" with no analysis),\n" +
	"note this in your report under a \"Review quality\" note. Do not rubber-\n" +
	"stamp an empty review as \"Approve\". If the diff is non-trivial and the\n" +
	"reviewer said nothing, flag it.\n" +
	"\n" +
	"## Output format\n" +
	"\n" +
	"Produce the report in this exact structure:\n" +
	"\n" +
	"## Verdict\n" +
	"One of: Approve, Request changes, or Needs discussion. Follow it with a\n" +
	"one-sentence justification. If the diff is non-trivial (more than 10\n" +
	"changed lines) and the reviewers found nothing, use \"Needs discussion\"\n" +
	"and note that the review may have missed issues.\n" +
	"\n" +
	"## Findings\n" +
	"List every distinct finding from the reviewers as a bullet, in severity\n" +
	"order (blocker, major, minor, nit, praise). Each bullet must include:\n" +
	"\n" +
	"- The file:line reference copied verbatim from the reviewer finding\n" +
	"  (e.g. `path:line`). NEVER adjust, recount, or recompute a line\n" +
	"  number — copy the reviewer's number exactly, even if it looks off.\n" +
	"- The severity tag: [blocker], [major], [minor], [nit], or [praise]\n" +
	"\n" +
	"De-duplicate identical findings. If the two reviewers disagree on\n" +
	"severity, take the higher one and note the disagreement inline.\n" +
	"\n" +
	"## Top concerns\n" +
	"Up to three bullets naming the issues most worth the author's attention.\n" +
	"If there are no findings, list \"None\" and explain whether the review\n" +
	"was thorough enough to be confident.\n" +
	"\n" +
	"Do not invent findings the reviewers did not report. If a reviewer\n" +
	"section is missing or says nothing was found, note it and assess whether\n" +
	"that seems plausible given the diff content. Keep the report under 600\n" +
	"words unless the diff is large." + StyleInstruction

// NewSummaryAgent builds the summary LLM agent.
func NewSummaryAgent(m model.LLM, instruction string) (agent.Agent, error) {
	if instruction == "" {
		instruction = DefaultSummaryInstruction
	}
	agent, err := llmagent.New(llmagent.Config{
		Name:                 SummaryAgentName,
		Model:                m,
		Description:          "Aggregates reviewer findings into a single human-readable review report.",
		Mode:                 llmagent.ModeSingleTurn,
		Instruction:          instruction,
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{EnsureUserContent(SummaryAgentName)},
	})
	if err != nil {
		return nil, fmt.Errorf("build summary agent: %w", err)
	}
	return agent, nil
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
