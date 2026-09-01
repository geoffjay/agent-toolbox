---
title: "Keep pipeline-specific code inside its pipeline package"
agents: ["static_analysis"]
severity: major
priority: 90
tags: ["architecture", "pipeline", "layering"]
---

This project is a toolbox of agent-graph pipelines. Each pipeline lives in
its own package under `internal/pipelines/<name>/` (for example
`internal/pipelines/review` or `internal/pipelines/audit`).

Flag any pipeline-specific identifier that appears in a shared layer
(`internal/engine`, `internal/ui`, `internal/graph`, `internal/rules`,
`internal/model`, `internal/hitl`, `internal/tools`, or `cmd`).

Pipeline-specific identifiers include agent names (`triage`,
`static_analysis`, `security`, `summary`), route keys, and domain words
tied to one process (`diff`, `PR`, `findings`, `verdict`, `audit`,
`scan`). A shared layer that names one pipeline cannot host a second.

- Put the graph shape, agents, instructions, routing, and output parser for
  a process in that process's package.
- Report a shared file that imports `internal/pipelines/...` as a major
  finding. Shared code must not depend on any pipeline.
