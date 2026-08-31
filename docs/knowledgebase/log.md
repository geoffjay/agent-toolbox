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
* **Decision + fix**: Empty reviews with Ollama/glm traced to an ADK bug
  (parallel single-turn agents lose their seeded user content when a
  sibling's events steal the current-turn pivot). Fixed in
  `internal/agents/user_content.go` with an `EnsureUserContent()`
  `BeforeModelCallback` on all four agents; recorded in
  `decisions/adk-parallel-turn-input.md`.

## 2026-08-30
* **Review response**: Worked through the PR 9 review findings. Fixed the
  TUI log sink (per-run 0600 file under `<UserCacheDir>/graph-review/log`,
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
