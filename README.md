# graph-review

`graph-review` is a tool for performing automated code reviews using
AI agents. It is built with [Go](https://go.dev) and the
[ADK](https://github.com/agent-development-kit/agent-development-kit-go)
library, which provides the agent loop and graph primitives used to
orchestrate the review workflow.

## Overview

The project defines a set of cooperating agents, each responsible for a
distinct stage of the code review process:

- **Triage Agent** — classifies the diff and routes it to the appropriate
  reviewers.
- **Static Analysis Agent** — checks style, formatting, and common
  anti-patterns.
- **Security Agent** — looks for vulnerabilities and unsafe patterns.
- **Summary Agent** — aggregates findings into a single, human-readable
  review report.

These agents are wired together as a graph using the ADK library, which
manages the control flow, tool dispatch, and state transitions between
nodes.

## Status

This project is a work in progress. Agent definitions, graph wiring,
and tooling are under active development.