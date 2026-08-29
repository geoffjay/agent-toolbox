# Plans

plans for the graph-review project.

- [Human-in-the-loop gates in the review graph](hitl-gates.md) — approval
  nodes in the ADK workflow graph; findings gate with decline-with-feedback
  loop-back is the first build.
- [Resumable runs via a persisted run file](resumable-runs.md) — file-backed
  ADK session service plus a run manifest so failed or gate-paused runs can
  be picked up midway by a later invocation (gh-CLI-style recovery).
- [Bubbletea terminal interface for review runs](tui-presentation.md) —
  charm-stack TUI (bubbletea, bubbles, huh, lipgloss, glamour) with a
  plain fallback surface; built, with deviations and open improvements
  marked for review.
