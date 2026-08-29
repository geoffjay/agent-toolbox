---
type: Plan
title: Human-in-the-loop gates in the review graph
description: Add explicit human-approval nodes to the ADK workflow graph, starting with a findings gate that supports decline-with-feedback loop-back to the reviewer nodes.
tags: [plan, hitl, workflow, graph, adk]
---

# Human-in-the-loop gates in the review graph
Status: **findings gate with loop-back built** (2026-08-28) —
`internal/agents/gate.go`, `graph.Config.FindingsGate`, the
`--findings-gate` CLI flag, and pause/resume in `cmd/review.go`
`runPipeline`. See the pattern doc
[adk-hitl-gates](../patterns/adk-hitl-gates.md).
Earlier phases also done: the `--post-comments` submission gate
(`cmd/pr.go` `confirmPost`) and `--assume-yes`.


## Background

The runtime (Google ADK v2.1.0) has native human-in-the-loop support:

- `workflow.ResumeOrRequestInput(ctx, emit, session.RequestInput)` —
  first pass emits a `RequestInput` and returns `ErrNodeInterrupted`
  (run pauses, `RunState` persisted); on resume the call returns the
  human's reply.
- `NodeConfig{RerunOnResume: &true}` — re-entry mode: the interrupted
  node re-runs from scratch and sees the reply.
- Resume is a second `runner.Run` turn on the same session whose message
  is a `FunctionResponse` part: `ID: <interruptID>`, `Name:
  workflow.WorkflowInputFunctionCallName` ("adk_request_input"),
  `Response: {"payload": <answer>}`.
- `RequestInput.InterruptID` must be unique per run (embed
  `ctx.InvocationID()`); a fixed literal breaks later pauses in the
  same session.

## Gate points

Three candidate gates, ordered by value:

1. **Scope gate** (after `route`): confirm triage category before
   reviewer tokens are spent. Cheapest; not built yet.
2. **Findings gate** (after `format_findings`): approve / revise /
   abort the findings before the summary/verdict consumes them.
   Highest value — the verdict maps to GitHub `APPROVED` /
   `REQUEST_CHANGES` (`internal/review.ExtractVerdict`). **Building
   this first.**
3. **Verdict gate** (after `summary`): in-graph submission approval,
   surface-agnostic (CLI, console launcher, dev UI). The CLI-level
   `confirmPost` gate covers only the `--post-comments` path.

## Findings gate design (building)

- New node in `internal/agents`: an `EmittingFunctionNode` with
  `RerunOnResume: &true` that calls `ResumeOrRequestInput`.
- Decisions: `approve` (forward to summary), `revise` (loop back to the
  reviewer nodes with an enriched prompt payload), `abort` (fail the
  run).
- **Loop-back is a conditional cycle**: `eb.AddRoute(gate, staticNode,
  StringRoute("revise"))` (+ security). `workflow.validateCycles`
  rejects only unconditional cycles; routed edges make the cycle legal.
  Scheduler re-fires looped nodes with a fresh lifecycle
  (`scheduler.go` `handleCompletion`).
- The gate's return value is the successor's input: reviewers feed on
  predecessor output (why `RouteByTriage` returns the original user
  message), so the revise payload is diff + prior findings + human
  feedback.
- Revision cap enforced by the gate via session state counter (the
  engine bounds only unconditional cycles); force-fail past the cap.
- `JoinNode` (`gather`) re-evaluates its barrier on every predecessor
  completion; revising both reviewers re-fires the join with fresh
  outputs. Routing a revision to a single reviewer re-uses the other's
  stale output — a deliberate, documented semantic.
- CLI (`cmd/review.go` `runPipeline`): the event loop must detect
  `ev.RequestedInput`, print message + payload, prompt for decision +
  feedback, then issue the resume turn. Non-interactive stdin fails
  closed (consistent with `confirmPost`).
- Flag: `--findings-gate` on both review subcommands (shared via
  `pipelineFlags`), wired through `graph.Config`. Default off.

## Verification plan

- Unit tests: decision parsing, revise payload composition, revision
  cap.
- Graph test: `graph.New` builds with the gate (cycle validation
  passes).
- E2E: fake `model.LLM` driving the workflow — pause event observed,
  resume turn with revise feedback loops back to reviewers, second
  round approves, summary consumes the refined findings.