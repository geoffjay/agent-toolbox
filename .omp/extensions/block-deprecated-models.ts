/**
 * agent-toolbox deprecated-model guard for oh-my-pi.
 *
 * The organization's LLM gateway rejects the retired Anthropic Claude 3
 * generation (Sonnet 3.5, Haiku 3.5, and the rest of `claude-3-*`). Every
 * attempt is logged and raises an IT event, so an agent that runs the review
 * CLI (or any command) with `--model claude-3-5-sonnet`, `-m claude-sonnet-3-5`,
 * `ANTHROPIC_MODEL=claude-3-haiku`, etc. creates noise and false alarms.
 *
 * This extension intercepts `bash` tool calls before execution and blocks any
 * command whose text names a deprecated model, returning a reason that steers
 * the agent to a current Claude 4+ model. It mirrors the Claude Code hook at
 * `.claude/hooks/block-deprecated-models.py` so both harnesses enforce the same
 * policy.
 */

import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";

// Deprecated == the whole Claude 3 generation, in either id ordering:
//   claude-3[.-]...        → claude-3-5-sonnet, claude-3-7-sonnet, claude-3-opus, claude-3.5-haiku
//   claude-(sonnet|…)-3…   → claude-sonnet-3-5, claude-haiku-3.5, claude-opus-3
// Claude 4+ ids (claude-sonnet-4-…, claude-opus-4-…) never match.
const DEPRECATED_MODEL = /claude-(?:3[.\-]|(?:sonnet|haiku|opus)-3)/i;

const SUGGESTION =
  "Use a current, non-deprecated Claude model instead " +
  "(e.g. claude-sonnet-4-20250514).";

export default function blockDeprecatedModels(pi: ExtensionAPI): void {
  pi.setLabel("agent-toolbox deprecated-model guard");

  pi.on("tool_call", async (event) => {
    if (event.toolName !== "bash") return;

    const command = String(event.input.command ?? "");
    const match = DEPRECATED_MODEL.exec(command);
    if (!match) return;

    return {
      block: true,
      reason:
        `Blocked: this command references the deprecated Anthropic model ` +
        `'${match[0]}...'. The organization's LLM gateway rejects the retired ` +
        `Claude 3 generation (Sonnet 3.5, Haiku 3.5, and other claude-3-* ` +
        `models); each attempt is logged and triggers an IT event. ${SUGGESTION}`,
    };
  });
}
