---
title: "The run engine must stay agent-agnostic"
agents: ["static_analysis"]
severity: major
priority: 80
tags: ["architecture", "engine", "pipeline"]
---

The run engine drives any pipeline: it builds the model, tools, graph, and
runner, then streams events and handles human gates. It must not know one
pipeline's agents.

- The engine selects the report stream by the pipeline's report-agent name,
  supplied at run time. Report a hardcoded agent name used to pick the
  report output.
- The engine must not build a specific graph directly. It calls the
  pipeline's build function. Report the engine importing a pipeline's graph
  constructor.
- Keep input framing out of the engine. The seed user message (a diff, a
  repo path, a PR reference) comes from the pipeline. Report an engine that
  hardcodes an input string such as "Please review the following diff".
