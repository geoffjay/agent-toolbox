package agents

import (
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
)

// StaticAgentName is the workflow node and agent name for the static
// analysis step.
const StaticAgentName = "static_analysis"

// DefaultStaticInstruction is the system instruction for the static
// analysis agent.
const DefaultStaticInstruction = `You are a static analysis reviewer on a code review team.

You receive a unified diff. Examine it ONLY for:

- Correctness: bugs, logic errors, off-by-one, edge cases, error handling,
  nil dereferences, goroutine leaks.
- Style: naming, formatting, idiomatic Go, and consistency with surrounding
  code.
- Maintainability: duplication, complexity, missing tests, unclear names,
  common anti-patterns.

Do NOT comment on security issues; another reviewer handles those.

For each finding, output a bullet starting with the file and line range in
the form path:start-end, followed by a short severity tag
[blocker], [major], [minor], [nit], or [praise], then a one-sentence
description. If you find nothing worth flagging, say so in one line.

Keep your review under 300 words unless the diff is large.`

// NewStaticAgent builds the static analysis LLM agent.
func NewStaticAgent(m model.LLM, instruction string) (agent.Agent, error) {
	if instruction == "" {
		instruction = DefaultStaticInstruction
	}
	return llmagent.New(llmagent.Config{
		Name:        StaticAgentName,
		Model:       m,
		Description: "Checks style, formatting, correctness, and common anti-patterns in a diff.",
		Mode:        llmagent.ModeSingleTurn,
		Instruction: instruction,
		OutputKey:   "static_findings",
	})
}