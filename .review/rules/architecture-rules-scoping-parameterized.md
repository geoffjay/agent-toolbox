---
title: "Parameterize rules agent-name validation by the active pipeline"
agents: ["static_analysis"]
severity: minor
priority: 60
tags: ["architecture", "rules", "pipeline"]
---

Repository rules are a shared mechanism. Each rule scopes itself to one or
more agents through its `agents:` frontmatter. The set of valid agent names
depends on the pipeline that runs the rules, not on a fixed list.

- Report a hardcoded map of valid agent names in `internal/rules`. The
  valid set must come from the active pipeline's agent list, passed in.
- Keep the wildcard `*` working: a rule scoped to `*` applies to every
  agent in the active pipeline.
- The rules loader stays pipeline-neutral. Report a review-specific agent
  constant (for example `static_analysis`) baked into the loader.
