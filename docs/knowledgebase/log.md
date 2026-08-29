# Knowledge Base Update Log

## 2026-08-28
* **Scaffold**: Created the graph-review knowledge base at `docs/knowledgebase/` using the `okf-ify` skill. Initial structure: `index.md`, `log.md`, and concept directories (concepts, decisions, patterns, references, plans). OKF v0.2 conformant.
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
