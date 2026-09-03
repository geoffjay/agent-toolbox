---
type: Plan
title: Bubbletea terminal interface for review runs
description: Replace raw fmt/slog output with a charm-stack TUI — scrolling gray agent stream, spinner, huh HITL forms, glamour-rendered markdown report, and file logging — with a plain stdout/stderr fallback surface for pipes and CI.
tags: [plan, ui, tui, bubbletea, charm, hitl, cli]
---

# Bubbletea terminal interface for review runs

Status: **built** (2026-08-28/29) — commits `5fd2e40` (implementation)
and `82d6508` (e2e test fixes): `internal/ui/` package, `cmd/presenter.go`
Presenter interface, presenter wiring through `cmd/review.go` and
`cmd/pr.go`. This plan is written **retroactively**; deviations from the
original request and open improvement opportunities are explicitly
marked below for review rather than hidden inside "done".

## Background

The CLI printed everything with `fmt` (stdout/stderr) and `slog` (stderr).
Review executions are multi-stage agent processes (triage → reviewers
(dynamic fan-out) → format → optional findings gate → summary) that may
pause for human-in-the-loop decisions. Output deserved a real terminal
interface: a view for content, spinners for long-running stages,
streaming agent output, forms for HITL, and rendered markdown for the
final report instead of a raw dump.

## Requested requirements → where they landed

| Requirement (original request) | Implementation | State |
|---|---|---|
| bubbletea v2 displays all output | `internal/ui` on `charm.land/bubbletea/v2@v2.0.9`; alt-screen view: header + status + viewport + form + footer (`internal/ui/model.go`) | done |
| All content in a view | single `model.View()` renders every surface; no fmt prints while the TUI is up | done |
| Spinner for long-running processes | `bubbles/v2` spinner beside an activity line: fetch PR, clone, per-agent activity, tool calls (`agent → tool`), posting review | done |
| Streaming agent output scrolls in the view | `bubbles/v2` viewport, soft wrap, sticky-to-bottom, per-agent separators | done |
| Streaming rendered light gray | lipgloss foreground color 247 (`internal/ui/styles.go` `Theme.Stream`) | done |
| Logging to a file (stdout occupied by bubbletea) | `tea.LogToFile("debug.log", "debug")` + slog handler installed in `cmd/presenter.go` `setupLogging` **before** the program starts | done |
| bubbles primitives | spinner (see above) | done |
| huh forms for HITL pipeline stages | embedded `huh/v2` forms: findings gate (approve / revise-with-feedback / abort) and post-comments confirmation (`internal/ui/model.go` `openForm`/`closeForm`) | done |
| lipgloss styling from the beginning | `internal/ui/styles.go` central `Theme` palette (brand, gray, warn, error, ok, help) | done |
| glamour for the final markdown report | async `tea.Cmd` render, dark/light adapted from the terminal background query, re-rendered on resize (`internal/ui/markdown.go`) | done |

## Design decisions

- **Presenter seam** — `cmd.Presenter` interface (`cmd/presenter.go`) with
  two implementations: `tuiPresenter` (wraps `*ui.Program`) and
  `plainPresenter` (classic stdout/stderr + stdin prompts). The pipeline
  (`runPipeline`, `reviewPR`, `postReview`) presents through it and
  never prints directly. Plain mode keeps the pre-TUI behavior byte-for-byte,
  which is what CI and piped runs keep getting.
- **Surface selection** — TUI when stdout is a terminal (bubbletea opens
  the controlling TTY for input when stdin is a pipe carrying the diff);
  `--plain` flag forces the classic surface. Non-interactive gates fail
  closed in plain mode, unchanged from before.
- **Threading** — the pipeline runs on a background goroutine and talks
  to the program via thread-safe `Program.Send`; `Gate`/`Confirm` block
  on buffered reply channels so the pipeline resumes exactly where it
  paused. Quitting (q / ctrl+c) cancels the work context; a resulting
  `context.Canceled` is swallowed so a deliberate interrupt is not
  reported as a failure.
- **huh embedding hazard** — bubbletea models are copied by value in
  `Update`; field bindings must point at heap state (`formState`), or
  `strings.Builder` copy-check panics appear (`5fd2e40` initially shipped
  a value `strings.Builder`, fixed in `82d6508`). Recorded here so the
  pattern is not re-learned.
- **Report persistence** — after the alt screen closes, the
  glamour-rendered report is printed once to stdout so it survives in
  the terminal scrollback.
- **Form abort semantics** — ctrl+c while a form is open maps to the
  *abort* gate decision / *declined* confirmation, because the form
  owns the keyboard while it is up.

## Deviations from the original request (review these)

1. **Import paths are `charm.land/...`, not `github.com/charmbracelet/...`.**
   The v2 stack moved to a vanity domain; the github module paths fail
   with "module declares its path as charm.land/...". The request's
   `github.com/charmbracelet/bubbletea` path only serves v1.
2. **`debug.log` is always written in TUI mode**; the `DEBUG` env var
   only forces debug level. The request's example gated file *creation*
   on `DEBUG`, but the TUI owns the whole terminal — warnings cannot go
   to stderr — so logging must always land in the file while the TUI
   runs. Plain mode still logs to stderr.
3. **`--plain` escape hatch added** (not requested) — a kill switch for
   a brand-new full-screen interface; also covers non-TTY stdout.
4. **Plain fallback surface retained** rather than making the TUI the
   only interface (the request said "launching the application should
   display all content in a view"; pipes/CI still need output).

## Gaps and improvement opportunities (open for review)

- **Gate payload display** — the findings-gate payload (formatted findings
  markdown) is not glamour-rendered in the gate view; the human reads the
  gray streamed reviewer output above the form instead. Rendering the
  payload would duplicate content unless the stream is collapsed.
- **Text selection restored + copy key** (2026-09-02, closing this gap) —
  mouse capture (`MouseModeCellMotion`) was removed
  (`View().MouseMode = tea.MouseModeNone`) so the terminal's native
  selection works over the whole interface; mouse-wheel scrolling inside
  the viewport is gone with it, but the full default keymap covers
  scrolling (`↑/↓`, `j/k`, `u/d`, `f/b`, `pgup/pgdn`, space — verified in
  `bubbles/v2 viewport/keymap.go`; the earlier note that `j`/`k` were
  unbound was wrong). A `c` copy key (`tea.SetClipboard`, OSC 52) copies
  the raw report markdown once finished, otherwise the plain-text
  stream (kept in a parallel unstyled builder); inert while a form owns
  the keyboard. Footer: `↑/↓ or j/k scroll · c copy · q quit`.
- **Activity granularity** — the status line shows the latest event
  (agent name, tool call); it does not show elapsed time, tokens, or a
  stage checklist (triage → reviewers → gate → summary).
- **`--post-comments` E2E not run against a real PR** (no GITHUB_TOKEN in
  the verification environment); the confirm form is covered by unit
  tests of the shared huh machinery.
- **Empty-report runs** keep the streaming view and show warnings below
  it (deliberate; alternative: a dedicated error view).
- **Interrupted exit code** — user-quit mid-run exits 0 silently; a
  non-zero "interrupted" exit could be argued for CI parity.
- **Agent separators** are sized to the width at append time; a terminal
  resize leaves earlier separators at the old width (cosmetic).
- **Test-driver caveat** — `internal/ui/model_test.go` executes returned
  commands with a 50 ms timeout, dropping animation timers (cursor
  blink, spinner ticks); a slow-but-legit command would be silently
  dropped too.

## Discovered pre-existing issues (candidates for their own plans)

Both reproduce identically on the pre-TUI baseline, so they are *not*
regressions, but the TUI work surfaced them:

1. **`--no-tools` stalls on current gateway models** — reviewers emit
   pseudo-tool-call XML as plain text; the workflow ends after
   `static_analysis` and never reaches `summary` ("no report produced").
2. **Large diffs (70 KB sample) never reach the reviewers** — both
   reviewers report "no diff was provided"; the verdict correctly flags
   it. Suspect a workflow-agent message-size or handoff limit.

## Verification

- Unit: `internal/ui/model_test.go` — gate approve/revise-with-feedback/
  abort, confirm accept/decline, quit-key ownership while a form is open,
  stream → finish → glamour swap, truncation.
- Full suite: `go build`, `go vet`, `gofmt`, `go test ./...` clean.
- E2E (tmux, live model): gray streaming with agent separators, spinner
  activity, findings-gate huh form → approve → pipeline resumed →
  glamour report rendered in the viewport → `q` exits 0 with the report
  printed after the alt screen closes; `--plain` output identical to the
  pre-TUI baseline binary run on the same input.
