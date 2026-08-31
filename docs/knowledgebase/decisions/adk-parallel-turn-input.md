---
type: decision
status: accepted
date: 2026-08-29
---

# Keep user content in every agent request (ADK parallel pivot workaround)

## Context

`graph-review review pr` against an Ollama-served model
(`--provider openai --base-url http://localhost:11434/v1`,
e.g. `glm-5.3:cloud`) produced empty reports: every reviewer agent
exhausted its retries with
`openai: response output did not contain text or tool content` and
continued with an empty response.

## Root cause (evidence)

Reviewer agents are `llmagent.ModeSingleTurn` agents run as parallel
workflow nodes. Each LLM request is built from the "current turn": the
events after the latest user (or foreign-agent) event in the session —
the backward pivot in ADK's
`internal/llminternal/contents_processor.go buildContentsCurrentTurnContextOnly`.
That pivot scan checks the isolation scope but **not the event branch**.
Parallel reviewers share one session on sibling branches, so a sibling's
events land after this agent's seeded user event and steal the pivot:

1. The request loses the seeded user content (the diff) and starts with
   a bare assistant message.
2. Ollama's Responses API (glm models) answers that shape with an empty
   message → the ADK returns `ErrNoTextOrToolContent`.
3. `internal/agents/retry.go` re-sends the identical request 3 more
   times, gets the identical empty response, and the agent ends with no
   output.

Verified by capturing the wire through a logging proxy
(`/tmp/gr-proxy.py` in the debugging session): the failing requests
contain only `[assistant reasoning, function_calls, function_call_outputs]`
with no user message, replaying the request verbatim reproduces the empty
answer, and replaying it with the user message prepended makes the model
respond normally. Affected ADK versions: v2.0.0–v2.2.0 (v2.2.0 unchanged).

## Decision

Do not fork or vendor the ADK. Ship a public-API workaround:
`internal/agents/user_content.go EnsureUserContent()` — a
`llmagent.BeforeModelCallback` wired into all four single-turn agents
(triage, static, security, summary). When an LLM request carries no
user-role content matching the invocation's `ctx.UserContent()` text, a
copy is prepended, restoring the intended request shape
`[user diff, ...current-turn events...]`.

Tradeoffs:

- The hijacked turn still loses the agent's *earlier* tool history for
  that one request (the pivot slices it off); the diff and the latest
  round survive, which is enough for the reviewers to continue.
- If the ADK fixes the pivot (branch check) upstream, the callback
  becomes a no-op (healthy requests already contain the user content) —
  safe to keep.

## Consequences

- OpenAI-compatible providers receive a well-formed user-first request
  on every turn; the empty-response failure mode disappears.
- Any future graph shape with parallel single-turn agents is covered by
  the same callback.
- Watch ADK releases; if `buildContentsCurrentTurnContextOnly` gains the
  branch filter, this doc should be updated and the callback may be
  retired.