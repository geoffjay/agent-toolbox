---
type: Plan
title: Reusable agent-graph pipelines
description: Refactor agent-toolbox from a single hardwired code-review graph into a toolbox of reusable agent-graph pipelines, with a first-class Pipeline abstraction, a pipeline-agnostic run engine, shared UI/config/rules/tool layers, and a second (repo-audit) pipeline to prove the seams.
tags: [plan, architecture, pipeline, reusability, graph, adk, refactor]
---

# Reusable agent-graph pipelines

Status: **planned** (2026-08-31). Step 1 (move the presenter/surfaces into
`internal/ui`) is built as part of this change; the remaining steps are
sequenced below. Architectural invariants are mirrored as review rules in
`.review/rules/` so the code-review pipeline flags regressions on this repo
itself.

## Goal

Turn `agent-toolbox` from an application that *is* one code-review graph into
a toolbox that *hosts many* agent-graph pipelines. Adding a new pipeline
(a new "useful process") should mean writing one self-contained package that
declares its graph, agents, input, and output — and reusing everything else
(model runtime, tools, rules, UI presenter, HITL gates, the run loop, the
CLI shell) unchanged.

Code review was the first process. The second, sketched below to pressure-
test the abstraction, is a **repo audit** that scans a whole repository for
pattern violations, security-requirement breaches, and other actionable
items.

## Current state: where the coupling lives

Every layer today assumes exactly one pipeline. The coupling points, from
most to least entangled:

1. **`internal/graph`** — this is *the review graph*, not a graph library.
   `New(ctx, Config)` hardcodes the shape
   `START → triage → route → reviewers (dynamic) → format → [gate] → summary`.
   `Config` carries review-agent-specific instruction fields
   (`TriageInstruction`, `StaticInstruction`, `SecurityInstruction`,
   `SummaryInstruction`) and `FindingsGate`.

2. **`internal/agents`** — mixes three unrelated concerns:
   - reusable model runtime: `NewModel` / `ModelConfig` / `Provider`,
     `anthropicmodel/`, retry (`retry_test.go`), `EnsureUserContent`;
   - review-specific agents: `triage.go`, `static.go`, `security.go`,
     `summary.go`, their `Default*Instruction` blocks, `StyleInstruction`;
   - review-specific routing + gate: `RouteStatic/Security/Both`,
     `TriageCategoryStateKey` + `NormalizeTriageCategory`
     (`triage.go`), `ReviewersNode` (`reviewers.go`), `FormatFindings`,
     and the HITL vocabulary in `gate.go`
     (`DecisionApprove/Revise/Abort`, `RouteRevise`, `FindingsGate`,
     `RevisePrompt`, `MaxFindingsRevisions`).

3. **`internal/rules`** — a generic idea (repo rules appended to agent
   instructions) hardcoded to review agents: `AgentTriage/Static/Security/
   Summary`, the `validAgents` map, and the `.review/rules` default dir.

4. **`internal/review`** — the review-report output adapter: parse findings,
   extract verdict, build a GitHub review request. Wholly review-specific.

5. **`internal/tools`** — `tools.go` (read_file, list_files, git_blame,
   git_log) is generic and reusable; `pr.go` (pr_files/pr_comments/
   pr_reviews) is GitHub-code-review-specific.

6. **`cmd`** — mixes generic CLI/surface glue (`presenter.go`: the
   `Presenter` interface, plain/TUI surfaces, `sanitize`, `dispatch`,
   logging sink) with review command specifics (`review.go`, `pr.go`), the
   diff-shaped seed message (`"Please review the following diff…"`), and
   shallow-review heuristics keyed to diffs (`warnShallowReview`,
   `countDiffLines`).

7. **`internal/ui`** — mostly reusable, but `model.go` imports
   `internal/agents` for the gate decision vocabulary and treats
   `SummaryAgentName` as "the agent whose stream is the report"; labels read
   "review" / "human gate".

8. **Naming** — the binary/module, the log dir (`<cache>/agent-toolbox/log`),
   and `.review/rules` are all review-branded. The toolbox name is fine;
   review-specific *dir names in shared layers* are not.

The core defect: **there is no `Pipeline` concept and no pipeline-agnostic
run engine.** `cmd.runPipeline` is the de-facto engine, but it hardcodes the
review graph builder, the review seed message, and `SummaryAgentName` as the
output author.

## Target architecture

Introduce a first-class **Pipeline** and a generic **engine**; push every
pipeline-specific fact behind the Pipeline surface. Everything else becomes
a shared, pipeline-agnostic layer.

### Packages

- **`internal/pipeline`** — the `Pipeline` interface and a `Registry`.
  A pipeline declares:
  - `Name() string`, `Description() string`;
  - `Build(ctx, BuildConfig) (agent.Agent, error)` — construct its graph
    (model + tools + rules-scoped instructions handed in via `BuildConfig`);
  - `Agents() []string` — agent names, for rules-scope validation;
  - `ReportAgent() string` — which agent's streamed text is the final report;
  - `NewInput(...)` / `InitialMessage(input) *genai.Content` — how the CLI
    input becomes the seed user message (diff, repo path, PR ref, …);
  - optional `Report` adapter — parse the report text into structured items
    and run post-actions (e.g. post a GitHub review, write a JSON export);
  - `Command() *cobra.Command` (or an input spec the shell turns into one) —
    how the pipeline is invoked and which pipeline-specific flags it adds.

- **`internal/engine`** — the generic driver extracted from
  `cmd.runPipeline`: build the model, tools, and graph (via `Pipeline.Build`),
  the runner, then drive the run loop and the HITL gate/resume loop, collect
  stats, and stream to the presenter keyed by `Pipeline.ReportAgent()`. No
  agent names, no "diff", no "review" — all of that arrives through the
  Pipeline and `BuildConfig`.

- **`internal/pipelines/review`** — the current review pipeline, relocated
  and self-contained: its agents, graph wiring, findings parsing
  (`internal/review` folds in here), the GitHub PR tools + post-action, and
  its cobra commands (`diff`, `pr`).

- **`internal/pipelines/audit`** — the new second pipeline (design below).

- **`internal/model`** — model runtime extracted from `internal/agents`:
  `NewModel`, `ModelConfig`, `Provider`, `anthropicmodel/`, retry,
  `EnsureUserContent`. Shared by every pipeline.

- **`internal/hitl`** (or `internal/gate`) — pipeline-neutral human-in-the-
  loop primitives: the `GateRequest` / `Confirmation` types, the decision
  vocabulary (`approve` / `revise` / `abort`), the resume/`RerunOnResume`
  emitting-node helper, and a generic revise-prompt composer. This removes
  the `ui → agents` and `rules → agents` cross-domain edges: both depend on
  `hitl`, not on a domain package.

- **`internal/graph`** — shrink to a *graph toolkit*: edge-builder
  conveniences, join/route helpers, and the conditional-cycle guard. The
  review-specific wiring moves into `internal/pipelines/review`.

- **`internal/rules`** — parameterize agent-name validation: `Load(dir)`
  stays, but the set of valid `agents:` frontmatter names is supplied by the
  active pipeline (`Pipeline.Agents()`), not a hardcoded map. `*` still
  matches all. The default dir becomes an app-level constant.

- **`internal/tools`** — generic repo tools stay. GitHub PR tools move to
  `internal/pipelines/review` (or `internal/tools/github` if a second
  pipeline needs them).

- **`internal/ui`** — the presenter, plain/TUI surfaces, sanitizer, dispatch,
  and logging sink. Pipeline-neutral: no `internal/agents` import (consume
  `hitl` vocab), the report-agent name passed in, generic labels.

- **`cmd`** — thin shell: the root command, shared persistent flags (model,
  logging, rules-dir, gate), and a loop over the `Registry` that mounts each
  pipeline's command and calls `engine.Run`.

### Dependency rule

```
cmd → pipeline (registry) → pipelines/* → { engine, model, tools, rules, hitl, graph, ui }
```

Shared layers (`engine`, `model`, `tools`, `rules`, `hitl`, `graph`, `ui`)
MUST NOT import any `internal/pipelines/*` package or contain any
pipeline-specific identifier. This is the invariant the review rules
enforce.

## The second pipeline: repo audit / scan

A concrete second process, chosen because it maximally exercises the seams
that a diff-review pipeline leaves untested (whole-repo input instead of a
diff; a structured export instead of a PR comment) while reusing everything
else.

- **Purpose** — scan a repository and report actionable items: code that
  breaks from established patterns, violations of security requirements,
  risky dependencies / licensing, dead code, and missing tests.

- **Input** — a repo path plus optional include/exclude globs and an
  optional `--since <ref>` to scope to recent changes. Not a diff; the seed
  message describes the scan scope, and the analyzers pull code through the
  repo tools on demand.

- **Graph** —
  `START → survey → route → analyzers (dynamic fan-out of the selected set) → format → [audit gate] → report → END`.
  `survey` inventories languages / frameworks / entrypoints (an LLM node, or
  a function node that walks the tree). `route` records the applicable
  analyzer set in session state. The dynamic fan-out / route / gate wiring
  reuses the `graph` toolkit, the `ReviewersNode` pattern, and the
  `hitl` gate exactly as review does (conditional fan-in through a
  dynamic node — never a JoinNode behind conditional routing; see the
  [join-barrier deadlock decision](../decisions/adk-join-barrier-deadlock.md)).

- **Reuses (proves the abstraction)** — model runtime, repo tools
  (`read_file` / `list_files` / `git_log` / `git_blame`), the rules loader
  (scoped to audit agents), the UI presenter and all surfaces, the HITL
  gate, the engine run loop, and the `path:line [severity]` finding shape.

- **New pieces (the whole delta)** — audit agents + instructions; a
  repo-survey/walk tool if the survey is function-driven; the audit agent
  name set for rules scoping; and an output adapter that emits actionable
  items (e.g. a JSON / SARIF-style export in addition to the human report).

If the only new code needed for the audit pipeline is its
`internal/pipelines/audit` package, the refactor succeeded.

## Migration steps (ordered; each leaves the build green)

1. **Move the presenter, surfaces, and logging into `internal/ui`**
   (this change). Decouple from `cmd.loggingFlags` (pass `slog.Level`) and
   from `agents.SummaryAgentName` (pass the report-agent name into
   `ui.Dispatch`). `cmd` keeps only cobra wiring.
2. **Extract HITL vocab + helpers into `internal/hitl`**; repoint `ui`,
   `agents`, and `rules`. Removes the cross-domain edges.
3. **Extract the model runtime into `internal/model`** from
   `internal/agents`.
4. **Define `internal/pipeline` + `internal/engine`.** Lift the run loop out
   of `cmd.runPipeline` verbatim; make review a registered pipeline with no
   behavior change.
5. **Relocate review agents / graph / findings / PR tools into
   `internal/pipelines/review`;** shrink `internal/graph` to a toolkit.
6. **Parameterize `internal/rules`** agent-name validation by the active
   pipeline.
7. **Thin `cmd`:** registry-driven command mounting; shared persistent flags;
   call `engine.Run`.
8. **Add `internal/pipelines/audit`** and register it.
9. **De-brand shared-layer names** (log dir, default rules dir) as app-level
   constants.

Steps 1–3 are pure refactors (no user-visible change). Steps 4–7 preserve
review behavior. Step 8 is the first new capability.

## Configuration concepts

- **Layered flags.** Model, logging, rules-dir, and gate flags are shared and
  live on the root/persistent flag set. Pipeline-specific flags (e.g.
  `--post-comments`, `--no-clone` for review; `--since`, `--exclude` for
  audit) are contributed by each pipeline's command.
- **Instruction overrides generalized.** Replace the four hardcoded
  `*Instruction` fields with a map keyed by the pipeline's agent names, or let
  each pipeline register its own override flags. `BuildConfig` carries the
  resolved (base + rules) instruction per agent.
- **Rules are pipeline-scoped.** `agents:` frontmatter is validated against
  the active pipeline's `Agents()`; `*` still matches all.
- **Future:** a config file (precedence: flags > env > file > default) — see
  the `cli-configuration` skill — is out of scope for the refactor but the
  layered-flag shape leaves room for it.

## Invariants (enforced by `.review/rules/`)

- No pipeline-specific identifier appears in `engine`, `ui`, `graph`,
  `rules`, `model`, or `hitl`.
- `internal/ui` MUST NOT import a domain/pipeline package.
- The engine selects the report stream via `Pipeline.ReportAgent()`, never a
  hardcoded agent name.
- Each pipeline is self-contained under `internal/pipelines/<name>/`.
- Generic tools live in `internal/tools`; provider- or pipeline-specific
  tools live with the pipeline.
- HITL / gate vocabulary lives in the neutral `hitl` package, not in a
  domain package.
- Rules agent-name validation is parameterized, not hardcoded.

See `.review/rules/` for the enforceable rule files.
