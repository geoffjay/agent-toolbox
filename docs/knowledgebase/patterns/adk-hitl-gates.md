---
type: Pattern
title: ADK human-in-the-loop gates and revise loop-backs
description: How approval gates pause/resume the review workflow and how declined findings loop back to the reviewers, including the engine gotchas.
tags: [pattern, hitl, adk, workflow, gate, testing]
---

# ADK human-in-the-loop gates and revise loop-backs

Implemented by the findings gate (`internal/agents/gate.go`,
`graph.Config.FindingsGate`, `--findings-gate`); planned extensions are in
[the plan](../plans/hitl-gates.md).

## Shape

An `EmittingFunctionNode` with `NodeConfig{RerunOnResume: &true}` calls
`workflow.ResumeOrRequestInput` first. First pass: emits a
`session.RequestInput` and returns `ErrNodeInterrupted`; the run pauses
with `RunState` persisted. The caller resumes with a second
`runner.Run` turn on the same session carrying one `FunctionResponse`
part: `ID = interruptID`, `Name = workflow.WorkflowInputCallName`,
`Response = {"payload": answer}`. On re-entry the call returns the
answer.

## Revise loop-back

The gate's decline path emits `ev.Routes = []string{"revise"}` and
returns the reviewer prompt (original diff + prior findings + feedback —
reviewers consume predecessor output, see `RouteByTriage`). The graph
wires `eb.AddRoute(gate, reviewer, StringRoute("revise"))` back to the
reviewer nodes; the approve path is `workflow.Default`. Cycles are
legal when every cycle contains at least one conditional edge
(`workflow.ErrUnconditionalCycle` fires only on all-nil-route cycles).
Looped nodes start a fresh lifecycle and overwrite `NodeState.Output`;
`JoinNode` re-evaluates its barrier on every predecessor completion, so
the revising reviewers re-fire the join and the revised findings re-enter
the gate (a second approval round per revision, by construction).

## Gotchas (all hit for real)

- **Interrupt IDs must be unique per run** (`name + "-" +
  ctx.InvocationID()`): the runner correlates resume responses by ID and
  will not re-prompt for an already-answered one.
- **No engine loop bound**: the gate itself must count revisions in
  session state and fail loudly past a cap (`MaxFindingsRevisions`).
- **Scripted model fakes must set `Content.Role = "model"`** on their
  `model.LLMResponse` — the engine drops role-less model content and the
  agents appear to produce empty output.
- **One `bufio.Reader` per interactive prompt sequence**: a second
  reader over the same stream loses whatever the first buffered ahead.
- **Fail closed on non-terminals**: gates in a CLI must check
  `os.Stdin.Stat()` char-device before prompting (note `/dev/null` is a
  char device — it passes the check but reads EOF, which still errors
  rather than approving).
- Session state values may round-trip JSON (`float64`), so counters
  read via `State().Get` should normalize `int`/`int64`/`float64`.

## Posting gates' findings to GitHub

Inline review comments must anchor to RIGHT-side lines *inside the file's
diff hunks* (context or added lines; both endpoints of a range must fall
in the same hunk). A finding citing a file that is in the PR but a line
outside the hunks fails the whole POST with 422 "Line could not be
resolved" — validate anchors with `review.FilterByDiffLines` before
posting, and degrade to a body-only review if GitHub still rejects (the
report body carries all findings anyway).