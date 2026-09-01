---
title: "Use neutral names in shared layers"
agents: ["static_analysis"]
severity: minor
priority: 55
tags: ["architecture", "naming"]
---

Shared layers serve every pipeline, so their names must not favor one
process.

- Report a review-branded exported identifier in a shared layer, such as a
  type or function named for reviews, findings, or diffs when it holds a
  general concept.
- Directory and file names in shared layers stay process-neutral. The
  application name (`agent-toolbox`) is acceptable for the binary and the
  log directory. A `review` or `findings` directory name inside a shared
  layer is not.
- Prefer general words: pipeline, report, finding location, gate. Reserve
  process words (review, audit, scan) for that process's package.
