# Decisions

decisions for the agent-toolbox project.

* [ADK parallel-turn input workaround](adk-parallel-turn-input.md) — keep
  the diff in every agent LLM request; ADK's single-turn pivot scan ignores
  branches and parallel reviewers steal each other's turn boundary.

* [ADK join-barrier deadlock](adk-join-barrier-deadlock.md) — conditional
  routing into a JoinNode deadlocks the fan-in; run only the reviewers
  triage selected via a dynamic orchestrator node instead of route edges +
  join.
