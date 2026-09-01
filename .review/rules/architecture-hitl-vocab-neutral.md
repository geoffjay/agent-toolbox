---
title: "Keep human-gate vocabulary in a neutral package"
agents: ["static_analysis"]
severity: minor
priority: 70
tags: ["architecture", "hitl", "gate"]
---

Human-in-the-loop gates are a shared capability, not a review feature. The
gate request and confirmation types, the decision words (`approve`,
`revise`, `abort`), the resume helper, and the revise-prompt composer
belong in a neutral package (for example `internal/hitl`).

- Report new gate vocabulary or gate helpers added to a domain package such
  as `internal/agents`.
- A shared consumer (the `ui` package, the engine) must depend on the
  neutral gate package, not on a pipeline's agents.
- The revise-prompt composer concatenates human feedback into an LLM
  prompt. That input must stay operator-trusted. Report any path that feeds
  untrusted text into it.
