#!/usr/bin/env python3
"""
Claude Code PreToolUse hook that blocks Bash commands targeting deprecated
Anthropic Claude 3.x models.

The organization's LLM gateway rejects the retired Claude 3 generation
(Sonnet 3.5, Haiku 3.5, and the rest of `claude-3-*`). Every attempt is
logged and raises an IT event, so an agent that runs the review CLI (or any
command) with `--model claude-3-5-sonnet`, `-m claude-sonnet-3-5`,
`ANTHROPIC_MODEL=claude-3-haiku`, etc. creates noise and false alarms.

This hook inspects the Bash command about to run and denies it when it names
a deprecated model, telling the agent to pick a current Claude 4+ model
instead. It matches both Anthropic's canonical id ordering
(`claude-3-5-sonnet-...`, `claude-3-7-sonnet-...`, `claude-3-opus-...`) and the
shorthand ordering (`claude-sonnet-3-5`, `claude-haiku-3.5`).

Configured under "PreToolUse" (matcher "Bash") in .claude/settings.json.
"""

import json
import re
import sys

# Deprecated == the whole Claude 3 generation, in either id ordering:
#   claude-3[.-]...        → claude-3-5-sonnet, claude-3-7-sonnet, claude-3-opus, claude-3.5-haiku
#   claude-(sonnet|…)-3…   → claude-sonnet-3-5, claude-haiku-3.5, claude-opus-3
# Claude 4+ ids (claude-sonnet-4-…, claude-opus-4-…) never match.
DEPRECATED_MODEL = re.compile(
    r"claude-(?:3[.\-]|(?:sonnet|haiku|opus)-3)",
    re.IGNORECASE,
)

SUGGESTION = (
    "Use a current, non-deprecated Claude model instead "
    "(e.g. claude-sonnet-4-20250514)."
)


def main() -> int:
    try:
        hook_input = json.load(sys.stdin)
    except (json.JSONDecodeError, EOFError):
        # Can't parse the payload: don't get in the way of real work.
        return 0

    if hook_input.get("tool_name") != "Bash":
        return 0

    command = str(hook_input.get("tool_input", {}).get("command", ""))
    match = DEPRECATED_MODEL.search(command)
    if not match:
        return 0

    reason = (
        f"Blocked: this command references the deprecated Anthropic model "
        f"'{match.group(0)}...'. The organization's LLM gateway rejects the "
        f"retired Claude 3 generation (Sonnet 3.5, Haiku 3.5, and other "
        f"claude-3-* models); each attempt is logged and triggers an IT "
        f"event. {SUGGESTION}"
    )

    print(json.dumps({
        "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "deny",
            "permissionDecisionReason": reason,
        }
    }))
    return 0


if __name__ == "__main__":
    sys.exit(main())
