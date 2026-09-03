---
type: Decision
title: Numbered diff and numbered read_file to fix line-anchor drift
description: Reviewer models cited wrong file:line numbers because they counted hunk headers by hand. Render the diff with new-file line numbers in a gutter, number read_file output the same way, and instruct reviewers to cite shown numbers verbatim.
tags: [decision, review-quality, line-anchors, diff, citations, github]
---

# Numbered diff and numbered read_file to fix line-anchor drift

Decided 2026-09-02, after auditing the anchors in the last posted review
(graph-review PR 10, submitted 2026-09-01T16:41): 4 of 11 inline comments
anchored to the wrong lines (`presenter.go:16` → the import is at 15;
`cmd/review.go:176` → the hardcode is at 174; `cmd/pr.go:101` →
`Dispatch` is at 100; `cmd/review.go:392` → `resumeMessage` is at 393).
The offsets went in both directions (+1, +2, −1), ruling out any
mechanical shift; the other 7 anchors were exact.

## Root cause

Nothing alters the diff: `GetDiff` is a byte passthrough, the seed
message wraps the diff verbatim, and `EnsureUserContent` restores the same
text. The drift happens in the model's head: the reviewers were told to
cite `path:line` but were given a raw unified diff with `@@ -12,13
+13,16 @@` hunk headers and an unnumbered `read_file`, so they derived
line numbers by counting — and models are bad at counting across
multi-hunk diffs. `FilterByDiffLines` cannot catch a wrong number that
happens to exist in a hunk (a `)`, a `}`, a blank line), which is exactly
what slipped through.

## Decision

Make the line number authoritative everywhere the model looks, and
forbid hand counting:

1. **`review.NumberedDiff`** (`internal/review/numbereddiff.go`) renders
   the seed-message diff with each added/context line prefixed by its
   RIGHT-side (new-file) number in a `%4d|` gutter — the exact quantity
   GitHub anchors inline comments to. Deletions and file headers carry a
   blank gutter; **hunk headers are replaced by a dash rule** because
   their `+START,COUNT` numbers are the single biggest citation
   confusion (observed: `+13,16` cited as line 16).
2. **`read_file` numbers its output** with the same gutter
   (`internal/tools/tools.go` `numberLines`), so full-file context cites
   the same coordinate system; the tool description says so.
3. **Reviewer instructions** (static + security) gained a "Line numbers"
   section: cite the gutter number exactly as shown; never count lines;
   never derive a number from a hunk header; unnumbered (deleted) lines →
   cite the nearest numbered line above.
4. **Summary instruction** now demands copying the reviewer's
   file:line verbatim — no recomputation during aggregation.

The transformation is presentation-only: anchor validation
(`FilterByDiffLines`), the shallow-review heuristic, and posting all
consume the original diff.

## Invariants (tested)

- Every number `NumberedDiff` displays is accepted by
  `FilterByDiffLines` on the same patch
  (`TestNumberedDiffGutterMatchesAnchors`) — a shown number can never
  produce an unanchorable finding.
- Line classification matches `patchHunks` exactly (`+`/` ` numbered,
  everything else not), so gutter numbers and anchor numbers share one
  definition.
- E2E: a mock Responses-API server playing an obedient model, driven
  through the real CLI, produced a finding citing exactly the gutter
  number of the target line (`x.go:21`).

## Residual risk

An instruction-following failure can still cite a wrong number — but now
the number is printed on the line itself, which is the strongest
correction signal a prompt can give. If a live model still drifts, the
next escalation is post-hoc: search the numbered diff for the quoted
finding text and re-anchor automatically.
