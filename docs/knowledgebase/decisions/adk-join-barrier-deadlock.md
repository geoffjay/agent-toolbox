---
type: Decision
title: Dynamic reviewer fan-out replaces route-conditional edges + JoinNode
description: Conditional routing into a JoinNode deadlocks the barrier — a route-skipped predecessor never fires, the run drains with no summary and the findings are lost. Replace the route edges + gather join with a dynamic orchestrator node that runs exactly the reviewers triage selected.
tags: [decision, adk, workflow, join, deadlock, routing, dynamic-node]
---

# Dynamic reviewer fan-out replaces route-conditional edges + JoinNode

Decided 2026-09-02, after a live `review pr` run (log
`run-20260902T182407-00084814.log`) ended with "no report produced" even
though the security reviewer had streamed 18 tool calls and real findings.

## The failure

The graph wired conditional route edges from the triage router to the two
reviewer agents and fanned them back in with a `workflow.JoinNode`
(`gather`). ADK's JoinNode contract
(`workflow/join_node.go`, v2.2.0) states that conditional routing into a
join is a **configuration error**: the barrier waits for every *declared*
predecessor, and a predecessor that route-skip never scheduled never
completes. When triage answered `security` (a single-reviewer route):

- `static_analysis` never ran (correct — routing skipped it),
- `gather` waited forever for it (deadlock),
- the scheduler drained with **no error and no summary**,
- the report was empty, so the CLI's empty-report backstop warned and
  `postReview` refused to post — the security findings that *had* been
  produced were lost.

The user-visible symptom ("one agent found nothing → error") was a
misread of the log: the triage verdict was the trigger, not the cause.

## The fix

Replace the static join shape with a dynamic orchestrator
(`internal/agents/reviewers.go`, `ReviewersNode`):

```
START → triage → route → reviewers (dynamic) → format_findings → [gate] → summary
```

- The route node (`triage.go` `TriageCategory`) is now a plain function
  node: it normalizes the triage reply and persists it under
  `TriageCategoryStateKey` via `ctx.State().Set`, then returns the
  original user text so the orchestrator receives the diff.
- `ReviewersNode` is a `workflow.NewDynamicNode` that reads the category
  from session state and calls `workflow.RunNode` on each selected
  reviewer, in parallel goroutines. ADK's sanctioned alternative for
  "wait only for the children that ran" (pattern:
  `examples/workflow/dynamic/llm`).
- It returns `map[string]any` keyed by agent name — the exact shape the
  JoinNode emitted — so `FormatFindings` is unchanged.
- The findings-gate revise edge is a single conditional edge
  `gate → reviewers`; a revise round re-runs the same reviewer set triage
  chose (the category persists in state).

## Hard-won details

- **`workflow.WithRunID` is mandatory on the `RunNode` calls.** The
  dynamic sub-scheduler repopulates child outputs from session history
  filtered by the current invocation; without a revision-scoped run ID
  (`r1`, `r2`, … from the gate's revision counter) a revise round would
  serve the stale round-1 reviewer outputs from cache and never re-run
  them.
- **`workflow.WithUseSubBranch()`** on each `RunNode` isolates the
  parallel reviewers' LLM histories (the
  `adk-parallel-turn-input` failure mode); `EnsureUserContent` stays as
  a safety net.
- **State writes from function nodes work**: events are persisted before
  successors schedule, so `TriageCategory`'s `State().Set` is visible to
  the orchestrator on the same pass — same mechanism the gate's
  `bumpRevisions` relies on.
- Revise rounds re-running only the routed reviewers is a **behavior
  change** vs the old join (which re-ran both), but it only differs for
  single-reviewer routes and it is the more defensible semantic: the
  human saw that reviewer set, that reviewer set gets revised.

## Evidence

- Repro (fails on the old graph, passes on the new):
  `TestSingleReviewerRouteReachesSummary` and the route table
  `TestTriageRoutesExpectedReviewers` in
  `internal/graph/graph_route_test.go`; gate coverage extended with
  `TestFindingsGateSingleReviewerReviseLoop` (the live scenario shape:
  triage `security` + revise round).
- All existing gate tests pass unchanged (`TestFindingsGateReviseLoop`,
  `TestFindingsGateAbortFailsRun`).
