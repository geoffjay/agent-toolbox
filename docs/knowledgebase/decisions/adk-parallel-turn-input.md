---
type: decision
status: accepted
date: 2026-08-29
---

# Keep user content in every agent request (ADK parallel pivot workaround)

## Context

`agent-toolbox review pr` against an Ollama-served model
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

## Escalation (2026-09-03): the workaround is insufficient for
tool-heavy reviewers — per-reviewer isolation scopes

The callback restores the diff, but it cannot restore what the pivot
steal also slices off: the reviewer's **own earlier tool calls**. A live
`claude-opus-5` run (log `run-20260903T220109`) showed the consequence:
each reviewer made a tool call, the sibling's event stole the pivot, the
next request lost the reviewer's accumulated tool history, the model
re-explored from scratch — for 53 minutes, until the user quit. The log
signature is a sawtooth: `contents=2→4→6→8→10→12→2` resetting over and
over while `EnsureUserContent` faithfully restores the diff each time.

Fix (still public API, `internal/agents/reviewers.go`): each reviewer's
`workflow.RunNode` call now also passes
`workflow.WithIsolationScope(name + "@" + runID)`. The pivot scan skips
out-of-scope events as turn starts (v2.2.0 `contents_processor.go` line
537), so sibling events can no longer steal the pivot; scoped single-turn
agents get their task input rebuilt by `buildTaskInputUserContent`, and
each reviewer's history is strictly its own. Verified by
`TestParallelReviewersKeepOwnHistory` (interleaved reviewer tool calls;
asserts each reviewer's own FunctionCalls/Responses survive in its next
request) — it fails on the pre-fix wiring and passes with scopes.

Upstream fixed the pivot scan itself in ADK v2.3.0 (`eventBelongsToBranch`
check in `buildContentsCurrentTurnContextOnly`). When this repo moves to
v2.3.0+, the scopes are redundant but harmless; `EnsureUserContent` stays
as a safety net.

Tradeoffs (updated):

- The scopes make each reviewer's events invisible to the other within a
  round — already true at the branch level; the scope adds history-level
  isolation, which is what a parallel review wants.
- If the ADK fixes the pivot (branch check) upstream, the callback
  becomes a no-op (healthy requests already contain the user content) —
  safe to keep.

## Consequences

- OpenAI-compatible providers receive a well-formed user-first request
  on every turn; the empty-response failure mode disappears.
- Parallel tool-using reviewers keep their own histories; runs converge
  regardless of how tool-heavy the model is.
- Any future graph shape with parallel single-turn agents is covered by
  the same callback and should pass its own isolation scope per child.
- Watch ADK releases; v2.3.0 has the branch filter. On upgrade, this doc
  should be updated and the scopes may be retired.
