---
title: "The ui package must not import domain or pipeline packages"
agents: ["static_analysis"]
severity: major
priority: 85
tags: ["architecture", "ui", "layering"]
---

`internal/ui` holds reusable presentation: the `Presenter` interface, the
plain and TUI surfaces, the sanitizer, `Dispatch`, and the log sink. Every
pipeline reuses it unchanged.

Keep `internal/ui` free of domain and pipeline packages.

- Report a new import of `internal/pipelines/...`, `internal/review`,
  `internal/graph`, or a pipeline's agent package as a major finding.
- The engine passes the active pipeline's report-agent name into
  `ui.Dispatch`. Report any hardcoded agent name in a `ui` file (for
  example a comparison against `agents.SummaryAgentName`).
- One legacy edge remains: `internal/ui` reads the gate decision words
  (`approve`, `revise`, `abort`) from `internal/agents`. Do not add more
  `agents` uses. Prefer a neutral gate package for shared vocabulary.
