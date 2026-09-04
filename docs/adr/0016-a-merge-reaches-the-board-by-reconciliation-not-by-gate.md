# 0016. A merge reaches the board by reconciliation, not by a gate

Date: 2026-08-28
Status: accepted

## Context

ADR 0003 forbids the fleet merging, so every merge happens outside the loop and
nothing carried the result back. fw-d20, fw-6gc and fw-0rf were still open with
merged PRs, and the next plan dispatched fw-d20 — ~$7 to rebuild work already on
main and open a second PR for it. Two fixes were on the table: a `gh:pr` gate on
the bead at PR-open time (ADR 0014's machinery), or a reconciler that asks gh.

## Decision

Reconcile. `bd gate check` closes the *gate* when its PR merges and releases what
it blocked — back into `bd ready`. Proven on fw-62j: PR 24 merged, the gate
closed, fw-fsa.9 became ready, not closed. A gate says "do not start until X
lands"; this needs "X landed, so this is done". `fleet reconcile-board` closes
every open or in-progress bead that a merged PR names (branch `bead/<id>`, or
titled `<id>: …` / `… (<id>)` — never a mid-title mention, which a builder
could write to close a bead it never built), with the PR as the close reason,
and runs before every `allocate` and `run`. A bead with a merged PR and a
still-open one is kept — a stack mid-review (ADR 0009). A gh failure is an
error and the plan is refused: "gh is down" must not read as "nothing merged".

Amended 2026-09-04 (fw-ojk): a merge only counts onto the repo's **default
branch**, asked of gh rather than pinned anywhere. gh reports `MERGED` for a PR
that landed anywhere, and spot-2ig was closed on a PR that went into
`fleet/builder-permissions` — 1880 lines on a branch nobody will merge again,
with the board saying done. A PR merged onto a non-default branch is reported as
`merged-elsewhere`, naming the branch, and never closes; a bead already closed
that way is reported as `closed-elsewhere` rather than reopened, because
recovering that work is a human's call (ADR 0003). Falsely closed is worse than
stale: stale costs a dispatch, false loses the work silently. The cost is that a
stacked child merged into its parent's branch never closes itself — a wasted
dispatch and a human `bd close`, deliberately the cheaper failure.

## Consequences

The board cannot drift from main by more than one fleet cycle, with no change to
the builder's skill and no second "done" for humans to remember. Harder: gh is a
second source of truth about what closed, and a PR that mentions a bead it did
not finish closes it — the id-in-title rule is only as honest as PR titles are.
