---
okf_version: "0.2"
---

# graph-review knowledge base

This is the working knowledge base for the graph-review project, conforming to the
[Open Knowledge Format (OKF) v0.2](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md).

It consolidates working knowledge about the project: what graph-review is, how it is
structured, decisions and their rationale, recurring patterns, and plans.
It is authored by people and agents and meant to be read by both.

## For agents (policy)

This section is the single source of truth for how agents should use this knowledge
base. Tooling injects it into context automatically, so it does not depend on
`CLAUDE.md`/`AGENTS.md` being picked up.

The following agents are wired to consult this knowledge base automatically:

* **Claude Code** — via a `SessionStart` hook (`.claude/hooks/kb-inject.py`) that
  injects this index, plus `PostToolUse`/`Stop` reminder hooks
  (`.claude/hooks/kb-reminder.py`).
* **opencode** — via the `instructions` array in `.opencode/opencode.jsonc`.
* **oh-my-pi** — via the `.omp/extensions/kb-hooks.ts` extension.

**Consult before acting.** Before working on a task, scan the entries below and
read any concept/decision/pattern doc relevant to what you are about to change.
Prefer the recorded decision or pattern over re-deriving one. This index is the
map; read the specific doc on demand rather than guessing.

**Update after acting.** Update the knowledge base when a change would make an
existing entry wrong or leave a new fact unrecorded. In particular:

* A new architectural decision or a change to startup/threading →
  add or update a [decision](decisions/index.md) and relevant concept docs.
* A new recurring convention → add a [pattern](patterns/index.md).
* A new concept or architectural understanding → add a [concept](concepts/index.md).
* A forward-looking plan or roadmap item → add a [plan](plans/index.md).
* An external source or spec referenced by the KB → add a [reference](references/index.md).

Concept docs require YAML frontmatter with a `type` field; `index.md` and
`log.md` are reserved. When you add a doc, add a one-line pointer to the matching
category index below and a line to [`log.md`](log.md). If you deliberately decide
*not* to record a change, that is fine — the policy is judgement, not a mandate
to touch the KB on every edit.

## Concepts

* _(empty — add concept docs here)_

## Decisions

* [ADK parallel-turn input workaround](decisions/adk-parallel-turn-input.md) —
  keep the diff in every agent LLM request; ADK's single-turn pivot scan
  ignores branches, so parallel reviewers steal each other's turn boundary.

## Patterns

* [ADK HITL gates](patterns/adk-hitl-gates.md) — request-input interrupt,
  re-run-on-resume, conditional-cycle loop-back pattern used by the
  findings gate.

## Plans

* [Human-in-the-loop gates in the review graph](plans/hitl-gates.md) —
  approval nodes in the ADK workflow graph; findings gate with
  decline-with-feedback loop-back built first.
* [Resumable runs via a persisted run file](plans/resumable-runs.md) —
  file-backed ADK session service plus a run manifest so failed or
  gate-paused runs resume midway.
* [Bubbletea terminal interface for review runs](plans/tui-presentation.md) —
  charm-stack TUI (bubbletea, bubbles, huh, lipgloss, glamour) with a
  plain fallback surface; built, with deviations and open improvements
  marked for review.

## References

* [OKF spec](references/okf-spec.md) - pointer to the Open Knowledge Format v0.2 specification.
