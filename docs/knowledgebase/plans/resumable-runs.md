---
type: Plan
title: Resumable runs via a persisted run file
description: Persist each review run as JSON so a failed or paused run can be picked up midway by a subsequent invocation, like gh CLI's failed-PR-create recovery.
tags: [plan, resume, durability, session, adk, cli]
---

# Resumable runs via a persisted run file

Status: **planned** (2026-08-28). Nothing built yet.

## Motivation

A review run is expensive (multiple LLM reviewer passes) and fragile
(model failures, terminal closed mid-gate, crash after gate approval
but before posting). Today `cmd/review.go` `runPipeline` uses
`runner.NewInMemory`: everything lives in the process and dies with it.
Picking a failed run up midway — the way `gh pr create` writes state so
a failed creation can be recovered — would stop repeated token spend
and stop losing human gate decisions.

## The key enabler (verified in ADK v2.1.0)

- `runner.Config.SessionService` is pluggable; the SDK ships only
  `session.InMemoryService()`, so a file-backed implementation is ours
  to write.
- **No custom checkpoint format is needed.** The runner rehydrates a
  paused run from session *history*: `ReconstructRunState`
  (`workflow/persistence.go`) rebuilds node states by scanning events,
  and `buildResumeResponses` (`runner/run_node.go`) finds open
  interrupts by matching `LongRunningToolIDs` in events against
  unanswered `FunctionResponse`s. Persist the events (plus the session
  state map) and a **new process** can continue a paused run with the
  same resume turn we already send.
- Cross-invocation resume is the designed path: every `r.Run` call is a
  new invocation, and the findings-gate e2e test already resumes across
  turns. Interrupt IDs are per-invocation unique
  (`findings_gate-<invocationID>`), so no collision on reload.
- Completed nodes are not re-run after rehydration (`state.go`
  `completed` set, reconstructed from history) — so a crash mid-run
  also recovers without re-paying for finished reviewer passes.
- The gate revision counter lives in session state → the cap survives
  resume.

## Design

### 1. File-backed session service

New package (e.g. `internal/runs`): a `session.Service` storing one JSON
file per session at
`$XDG_STATE_HOME/agent-toolbox/runs/<session-id>.json`
(fallback `~/.local/state/...`; `--runs-dir` to override; not a temp
file — it must survive reboots, which rules out os.TempDir).

- `AppendEvent` rewrites the file atomically (temp file + rename) with
  0600 perms — diffs can contain secrets.
- File layout (versioned): metadata (app/user/session IDs, timestamps),
  the session state map, and the ordered event array. Events are
  already JSON-shaped (`session.RequestInput` documents this).
- `Get` rebuilds an in-memory session object from the file;
  `AutoCreateSession` in `runner.Config` covers first runs.
- `List`/`Delete` enable `runs list` / cleanup.

### 2. Run manifest

The session file answers "where was the workflow"; a small manifest
section in the same file answers "what CLI invocation was this":

- model config (provider, model, base URL — **never** keys/tokens;
  credentials come from the environment on resume)
- pipeline flags (instruction overrides, `--no-tools`,
  `--findings-gate`, `--rules-dir`)
- source: PR ref + number (diff re-fetched on resume) or diff-origin
  path (the diff text is already in the session events as the user
  message — no copy needed)
- posting state: `--post-comments` flag, posted review URL if any —
  so a crash between gate approval and posting can be retried
- status: `running` → `paused` | `failed` | `completed` (+ `posted`)
- the pending gate request is **derived** from events (open
  `RequestInput`), never duplicated in the manifest

### 3. CLI UX

- Every run writes its run file; the path is printed to stderr on
  start, on pause, and on failure ("resume with: agent-toolbox resume
  <path>").
- `agent-toolbox resume <session-id-or-path> [--assume-yes] [flag
  overrides]`: rebuilds the pipeline from the manifest, opens the same
  session, and continues — prompts at a paused gate, re-runs only
  unfinished nodes after a crash, or re-confirms a pending post.
- `agent-toolbox runs list` / `runs prune --older-than 30d`
  (follow-ups).

### 4. Wiring

`runPipeline` switches from `runner.NewInMemory` to
`runner.New(Config{AppName, Agent, SessionService: fileSvc})` and
updates the manifest at the lifecycle points it already knows: turn
end with `pending != nil` → `paused`; run error → `failed`; report
complete → `completed`; posted → `posted`.

## Phases

1. File-backed session service + round-trip tests (create/append/get,
   atomicity under kill, perms, version field).
2. Manifest read/write + `runPipeline` wiring.
3. `resume` command (paused-gate and crash-recovery paths, pending-post
   retry).
4. `runs list` / prune, README, KB pattern entry.

## Verification plan

- Unit: service round-trip; corrupt/truncated file → clear error, not
  silent partial history; state map survives reload.
- E2E: gate run paused → **construct a new runner in a fresh "process"
  from the file** → resume with approve → summary completes and the
  reviewers are NOT re-invoked (assert via call recording, as in
  `graph_gate_test.go`).
- Crash recovery E2E: scripted model fails after one reviewer completes
  → reload → new run → only the unfinished work executes.
- Security: no credential material in the file (grep the JSON for the
  env keys in a live run).

## Risks / open questions

- Event volume: large diffs make large files; acceptable for a CLI, but
  consider truncating thought/debug fields if size becomes a problem.
- Model drift between run and resume: manifest pins the model name;
  allow overrides but warn that reviewers' outputs came from a
  different model.
- Resume of a run whose PR moved (new commits pushed): re-fetch the
  diff and refuse resume with a clear message if the head SHA differs —
  reviewing a stale diff is worse than re-running.
- Failure recovery for a *failed node* (as opposed to paused) needs a
  deliberate test: rehydration treats "no output event" as incomplete,
  but the first implementation should verify the failure-to-run
  transition rather than assume it.
