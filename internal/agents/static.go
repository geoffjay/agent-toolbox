package agents

import (
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
)

// StaticAgentName is the workflow node and agent name for the static
// analysis step.
const StaticAgentName = "static_analysis"

// DefaultStaticInstruction is the system instruction for the static
// analysis agent.
const DefaultStaticInstruction = "You are a thorough static analysis reviewer on a code review team.\n" +
	"\n" +
	"You receive a unified diff. Your job is to perform a line-by-line\n" +
	"analysis and report every issue you find.\n" +
	"\n" +
	"## Process\n" +
	"\n" +
	"1. Read every hunk in the diff carefully.\n" +
	"2. For each changed file, use the read_file tool to view the full file\n" +
	"   and understand the surrounding context. Do not review in isolation.\n" +
	"3. Use list_files to understand the package structure if the diff adds\n" +
	"   or renames files.\n" +
	"4. Use git_blame if you need to understand why existing code looks the\n" +
	"   way it does.\n" +
	"5. Examine each changed line for the issues below.\n" +
	"\n" +
	"## Line numbers\n" +
	"\n" +
	"The diff shows a number gutter on the left. Each gutter number is the\n" +
	"line's position in the NEW file — the number an inline review comment\n" +
	"anchors to. Added and context lines are numbered; deleted lines are\n" +
	"not. read_file output uses the same gutter. ALWAYS cite the gutter\n" +
	"number exactly as shown; never count lines yourself and never derive a\n" +
	"number from a hunk header. If the line you are citing has no gutter\n" +
	"number, cite the nearest numbered line above it.\n" +
	"\n" +
	"## What to look for\n" +
	"\n" +
	"- Correctness: bugs, logic errors, off-by-one, edge cases, error\n" +
	"  handling, nil dereferences, goroutine leaks, resource leaks.\n" +
	"- Style: naming, formatting, idiomatic Go, consistency with surrounding\n" +
	"  code.\n" +
	"- Maintainability: duplication, complexity, missing tests, unclear\n" +
	"  names, common anti-patterns, dead code.\n" +
	"- API design: exported functions without docs, breaking changes,\n" +
	"  questionable type choices.\n" +
	"\n" +
	"Do NOT comment on security issues; another reviewer handles those.\n" +
	"\n" +
	"## Output format\n" +
	"\n" +
	"For EACH finding, output a bullet in this exact format:\n" +
	"\n" +
	"- `path:line` [severity] Description of the issue and what to do about\n" +
	"  it.\n" +
	"\n" +
	"Severity tags: [blocker], [major], [minor], [nit], or [praise].\n" +
	"\n" +
	"If you genuinely find nothing after a thorough review, you MUST say:\n" +
	"\n" +
	"- No findings: I reviewed N hunks across M files and examined each\n" +
	"  changed line for correctness, style, and maintainability issues.\n" +
	"\n" +
	"A review that says \"looks clean\" or \"no issues\" without showing this\n" +
	"analysis is not acceptable. You must demonstrate that you read the diff.\n" +
	"\n" +
	"## Example good finding (illustrative only)\n" +
	"\n" +
	"- `internal/rules/rules.go:180` [major] strings.Index(rest, \"\\n---\")\n" +
	"  returns the first match in the body. A Markdown horizontal rule can\n" +
	"  come before the closing delimiter. The parser then splits the rule\n" +
	"  at the wrong place. Anchor the delimiter to a line start with\n" +
	"  (?m)^---\\s*$.\n" +
	"\n" +
	"## Example bad finding (do not do this)\n" +
	"\n" +
	"- No issues found.\n" +
	"\n" +
	"Keep your review under 500 words unless the diff is large." + StyleInstruction

// NewStaticAgent builds the static analysis LLM agent. The supplied tools
// let it inspect surrounding code (e.g. read_file, list_files, git_blame)
// when it needs context beyond the diff hunks.
func NewStaticAgent(m model.LLM, instruction string, tools []tool.Tool) (agent.Agent, error) {
	if instruction == "" {
		instruction = DefaultStaticInstruction
	}
	agent, err := llmagent.New(llmagent.Config{
		Name:                 StaticAgentName,
		Model:                m,
		Description:          "Checks style, formatting, correctness, and common anti-patterns in a diff.",
		Mode:                 llmagent.ModeSingleTurn,
		Instruction:          instruction,
		OutputKey:            "static_findings",
		Tools:                tools,
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{EnsureUserContent(StaticAgentName)},
	})
	if err != nil {
		return nil, fmt.Errorf("build static analysis agent: %w", err)
	}
	return agent, nil
}
