---
title: "Place generic tools in internal/tools and specific tools with the pipeline"
agents: ["static_analysis", "security"]
severity: minor
priority: 65
tags: ["architecture", "tools", "layering"]
---

`internal/tools` holds repository-inspection tools that every pipeline can
use: `read_file`, `list_files`, `git_blame`, and `git_log`. These tools
stay rooted at the repo and must reject paths that escape the root.

- Keep provider-specific or pipeline-specific tools out of
  `internal/tools`. GitHub pull-request tools belong with the review
  pipeline. Report a GitHub or PR tool added to the generic tools package.
- A new generic tool must resolve its repo root from the agent context
  state and validate every path with the safe-join check. Report a tool
  that reads or runs commands on an unchecked path.
- Report a tool that runs a shell string instead of an argument list, or
  that runs git through a shell.
