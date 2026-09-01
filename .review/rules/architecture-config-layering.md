---
title: "Layer configuration: shared flags on the root, pipeline flags on the pipeline"
agents: ["static_analysis"]
severity: minor
priority: 50
tags: ["architecture", "config", "cli"]
---

Configuration follows the same split as the code.

- Shared flags (model, logging, rules directory, gate) belong on the root
  or persistent flag set. Report a shared flag redefined inside one
  pipeline's command.
- Pipeline-specific flags belong on that pipeline's command. Report a
  pipeline flag (for example a post-comments toggle, or a scan scope) added
  to the shared flag set.
- Do not add a fixed field per agent for instruction overrides. Use a map
  keyed by the active pipeline's agent names, or let each pipeline register
  its own override flags. Report a new hardcoded per-agent instruction
  field in a shared config struct.
- Resolved instructions (base plus repository rules) reach each agent
  through the build config, not through a global.
