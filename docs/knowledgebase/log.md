# Knowledge Base Update Log

## 2026-08-28
* **Scaffold**: Created the agent-toolbox knowledge base at `docs/knowledgebase/` using the `okf-ify` skill. Initial structure: `index.md`, `log.md`, and concept directories (concepts, decisions, patterns, references, plans). OKF v0.2 conformant.
* **Plan**: Added `plans/hitl-gates.md` — human-in-the-loop gate nodes
  (scope / findings / verdict) for the ADK review graph, findings gate
  with revise loop-back as the first build.
* **Pattern + implementation**: Built the findings gate with revise
  loop-back — `internal/agents/gate.go`, `graph.Config.FindingsGate`,
  `--findings-gate` flag, pause/resume in `cmd/review.go` `runPipeline`;
  recorded the HITL gate/loop-back pattern in
  `patterns/adk-hitl-gates.md`.
* **Plan**: Added `plans/resumable-runs.md` — persist review runs as JSON
  (file-backed session service + run manifest) so failed or gate-paused
  runs resume midway without re-paying for completed agents; verified the
  ADK rehydration path (`ReconstructRunState` scans session history, no
  checkpoint format needed) before designing.

## 2026-08-29
* **Plan (retroactive)**: Added `plans/tui-presentation.md` — the bubbletea
  terminal interface plan, written after the fact from the original UI
  request. Records requirement→implementation mapping, design decisions,
  deviations (charm.land import paths, always-on `debug.log` in TUI mode,
  `--plain` flag), open improvement opportunities, and the two pre-existing
  pipeline issues the E2E runs surfaced (`--no-tools` stall; large diffs
  not reaching reviewers). Implementation lives in commits `5fd2e40` +
  `82d6508`.
* **Decision + fix**: Empty reviews with Ollama/glm traced to an ADK bug
  (parallel single-turn agents lose their seeded user content when a
  sibling's events steal the current-turn pivot). Fixed in
  `internal/agents/user_content.go` with an `EnsureUserContent()`
  `BeforeModelCallback` on all four agents; recorded in
  `decisions/adk-parallel-turn-input.md`.

## 2026-08-30
* **Review response**: Worked through the PR 9 review findings. Fixed the
  TUI log sink (per-run 0600 file under `<UserCacheDir>/agent-toolbox/log`,
  pruned to the last 5 runs, `DEBUG`-env escalation removed, path shown at
  startup, verified `tea.LogToFile` is O_APPEND — the security reviewer was
  right); the spinner tick-chain death after gate pauses/finish (always
  answer `spinner.TickMsg`, freeze is visual); the fail-open gate default
  (abort preselected, findings payload surfaced in the viewport); bare
  `return err` boundaries wrapped; plain surface regressions (milestone
  lines restored via `Presenter.Milestone`, post confirm prints the review
  body again); `EnsureUserContent` seed aliasing (parts deep-copied; the
  old alias assertion compared slice-slot addresses and could not fail);
  dead `userQuit` state; the unreachable empty-report test; `reviewPR`
  grouped into `prOptions`; missing trailing newlines in the new docs.
  Not changed: the Taskfile binary name (intentional per author) and
  `Program.Run` swallowing deliberate interruptions (documented behavior).

## 2026-08-31
* **Review response (round 2)**: Restored the plain gate's decision
  prompt (`decision [approve/revise/abort]:`) dropped in the TUI rework;
  wrapped the two remaining bare `return err` boundaries in `runPipeline`;
  added a control-byte sanitizer in `cmd/presenter.go`, applied inside
  both surfaces (a wrapping decorator delegating interface errors tripped
  `wrapcheck`), so a crafted PR title cannot inject ANSI/OSC sequences —
  e.g. an OSC 52 clipboard rewrite — into the reviewer's terminal;
  `printReport` falls back to the raw markdown when the glamour render
  has not landed before the user quits; run-log names zero-pad the pid so
  pruning order matches creation order; `TestPlainConfirmPrintsBody` pipes
  stdin so it cannot block under a PTY; reworded the duplicated
  "post review" wrap; removed a dead `var _ io.Writer`.
* **go-sec step**: Enabled `Builtins.go_sec` in `hk.pkl`. The globally
  installed gosec (v2.22.4, built with go1.24) fails on go 1.27 with
  `internal error: package "..." without types`; pinned
  `aqua:securego/gosec = 2.29.0` in `mise.toml` so the step runs a current
  binary. The five findings it raises on this codebase are deliberate
  patterns (shell-free git subprocesses, repo-scoped file reads, the
  cache-dir log file) and carry `#nosec` annotations with rationale.
* **Plan**: Added `plans/reusable-pipelines.md` — refactor the single
  hardwired code-review graph into a toolbox of reusable agent-graph
  pipelines. Introduces a `Pipeline` abstraction + pipeline-agnostic
  `engine` (lifted from `cmd.runPipeline`), extracts model runtime
  (`internal/model`) and HITL vocab (`internal/hitl`) out of
  `internal/agents`, relocates review-specific code under
  `internal/pipelines/review`, and shrinks `internal/graph` to a toolkit.
  Sketches a repo-audit second pipeline to prove the seams. Records the
  full coupling inventory, ordered migration steps, and configuration
  concepts.
* **Presenter move**: Moved the `Presenter` interface, plain/TUI surfaces,
  `sanitize`, `dispatch`→`ui.Dispatch`, and the run-log sink out of
  `cmd/presenter.go` into `internal/ui/presenter.go` (step 1 of the plan).
  Decoupled from `cmd.loggingFlags` (now takes `slog.Level`) and from
  `agents.SummaryAgentName` (report-agent name passed into `ui.Dispatch`).
  Relocated the presenter/gate-prompt/approval tests into `internal/ui`.
* **Review rules**: Added an exhaustive `.review/rules/` set encoding the
  target architecture (pipeline isolation, ui-no-domain-imports,
  engine agent-agnosticism, neutral HITL vocab, generic-vs-pipeline tools,
  parameterized rules scoping, package layout, neutral shared-layer names)
  so the review pipeline flags structural regressions on this repo.

## 2026-09-02
* **Decision + fix (join-barrier deadlock)**: A live `review pr` run
  ended "no report produced" after the security reviewer streamed 18
  tool calls and real findings — the run triaged to a single reviewer
  (`security`), and ADK's JoinNode barrier (`gather`, declared
  predecessors: both reviewers) waited forever on the route-skipped
  `static_analysis`, so the scheduler drained silently and the summary
  never ran; the empty report then blocked posting. Replaced the route
  edges + join with a dynamic orchestrator (`internal/agents/reviewers.go`
  `ReviewersNode`: reads `TriageCategoryStateKey` from session state,
  runs the selected reviewers in parallel via `workflow.RunNode` with
  `WithUseSubBranch` + revision-scoped `WithRunID`); the route node became
  a plain function node that persists the category. Gate revise edge now
  loops to the reviewers node (single conditional edge). Recorded in
  `decisions/adk-join-barrier-deadlock.md`; tests in
  `internal/graph/graph_route_test.go` (route table + single-reviewer
  gate revise loop).
* **TUI fix (text selection + copy key)**: Disabled mouse capture
  (`MouseModeNone` in `View()`) so native terminal selection works over
  the whole interface; added a `c` copy key (OSC 52 via
  `tea.SetClipboard`) copying the raw report markdown when done, else
  the plain-text stream (parallel unstyled builder); footer updated.
  Mouse-wheel viewport scrolling is gone; the viewport's default keymap
  (j/k/u/d/f/b/pgup/pgdn/space) covers scrolling. Updated
  `plans/tui-presentation.md` (gap closed; also corrected its claim that
  `j`/`k` were unbound), `patterns/adk-hitl-gates.md`,
  `plans/hitl-gates.md`, `plans/reusable-pipelines.md`, and the README
  flowchart.

* **Decision + fix (line-anchor drift)**: Audited the anchors of the last
  posted review (graph-review PR 10): 4 of 11 inline comments cited
  wrong lines (off by −1..+2, both directions). The diff itself is never
  altered (`GetDiff` is a passthrough; the seed fences it verbatim) —
  the drift is the models counting hunk headers by hand, and
  `FilterByDiffLines` can't catch a wrong number that happens to exist
  in a hunk. Fix: `review.NumberedDiff` renders the seed diff with a
  new-file line-number gutter (hunk headers replaced by a rule — their
  `+START,COUNT` is the top confusion source), `read_file` numbers its
  output with the same gutter, reviewer instructions gained a "Line
  numbers" section (cite the gutter verbatim, never count), and the
  summary must copy reviewer numbers verbatim. Cross-invariant test:
  every shown number is anchorable per `FilterByDiffLines`. E2E smoke:
  mock Responses-API model driven through the real CLI cites exactly the
  gutter number. Recorded in `decisions/review-line-anchor-drift.md`;
  README routing section documents the numbering.

## 2026-09-03
* **Fix (parallel reviewer non-completion, 53-minute loop)**: A live
  claude-opus-5 run never completed: the log showed reviewer LLM calls
  for 53 minutes with a `contents=2→4→…→12→2` sawtooth, then two
  `context canceled` errors when the user quit. Root cause is the
  documented ADK v2.2.0 pivot bug escalated: `EnsureUserContent` restored
  the diff, but the pivot steal also slices the reviewer's own tool
  history, so a tool-heavy model re-explored from scratch after every
  sibling event, forever (yesterday's glm run converged only because it
  used few tools). Fix: each reviewer `RunNode` call now passes
  `WithIsolationScope(name + "@" + runID)` — out-of-scope events can't
  be pivots, so sibling events can't reset the turn boundary and each
  reviewer's history stays its own. Repro:
  `TestParallelReviewersKeepOwnHistory` (interleaved tool calls via
  stage barriers; asserts own tool history survives) fails pre-fix,
  passes post-fix; upstream v2.3.0 fixed the pivot scan itself
  (branch check) — scopes become redundant but harmless on upgrade.
  Recorded as an escalation in
  `decisions/adk-parallel-turn-input.md`. E2E: tool-using mock model
  driven through the real CLI converges in 2.5s with correct
  gutter-cited findings.
* **Fix (cancellation masked as model failure)**: `retry.go` treated
  `context canceled` as retryable-transient and, when "exhausted",
  fabricated an empty model response (`transient_error_exhausted`) —
  a deliberate quit (q / ctrl+c) surfaced as two "transient errors
  exhausted" warnings and an empty report. Now cancellation and
  deadline errors are non-transient and propagate: the TUI's existing
  `errors.Is(workErr, context.Canceled)` swallow in `ui.Run` turns a
  deliberate interrupt into a clean exit, and a genuinely canceled run
  reports the interrupt instead of a fake empty review. Test:
  `TestRetryModelPropagatesCancellation`.
