package agents

import (
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
)

// SecurityAgentName is the workflow node and agent name for the security
// review step.
const SecurityAgentName = "security"

// DefaultSecurityInstruction is the system instruction for the security
// agent.
const DefaultSecurityInstruction = `You are a security reviewer on a code review team.

You receive a unified diff. Examine it ONLY for:

- Vulnerabilities: injection (SQL, command, XSS), path traversal, SSRF,
  unsafe deserialization, missing authorization checks.
- Unsafe patterns: crypto misuse, weak randomness, hardcoded secrets,
  credentials in logs, insecure TLS.
- Input handling: unvalidated user input, missing bounds checks, integer
  overflow, unsafe type conversions.
- Privilege issues: over-broad permissions, privilege escalation,
  secrets leaked via error messages or panics.

Do NOT comment on pure style or formatting; another reviewer handles
those.

For each finding, output a bullet starting with the file and line range in
the form path:start-end, followed by a short severity tag
[blocker], [major], [minor], or [nit], then a one-sentence description.
If you find nothing worth flagging, say so in one line.

Keep your review under 300 words unless the diff is large.`

// NewSecurityAgent builds the security LLM agent.
func NewSecurityAgent(m model.LLM, instruction string) (agent.Agent, error) {
	if instruction == "" {
		instruction = DefaultSecurityInstruction
	}
	return llmagent.New(llmagent.Config{
		Name:        SecurityAgentName,
		Model:       m,
		Description: "Looks for vulnerabilities, unsafe patterns, and secret handling issues in a diff.",
		Mode:        llmagent.ModeSingleTurn,
		Instruction: instruction,
		OutputKey:   "security_findings",
	})
}