---
title: "Follow the layered package layout"
agents: ["static_analysis"]
severity: major
priority: 75
tags: ["architecture", "layering", "package"]
---

The dependency direction is one-way:

    cmd → pipeline registry → internal/pipelines/* → shared layers

Shared layers are `internal/engine`, `internal/model`, `internal/tools`,
`internal/rules`, `internal/hitl`, `internal/graph`, and `internal/ui`.

- A shared layer must not import `internal/pipelines/...`. Report such an
  import as a major finding.
- `cmd` stays thin: the root command, shared flags, and code that mounts
  each pipeline's command and calls the engine. Report pipeline graph
  wiring, agent definitions, or output parsing added to `cmd`.
- New process code belongs in `internal/pipelines/<name>/`, not in a shared
  layer. Report a new shared file whose contents serve one process.
- `internal/graph` is a graph toolkit (edge builders, join and route
  helpers, cycle guards). Report a concrete pipeline graph placed there.
