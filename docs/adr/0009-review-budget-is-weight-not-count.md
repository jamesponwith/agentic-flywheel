# 0009. The review budget is weight, not a PR count

Date: 2026-08-16
Status: accepted
Amends: 0006

## Context

ADR 0006 capped the fleet at 8 PRs/day, derived from a two-hour review morning
at 15 minutes each. The v2 build then produced 9 PRs in a session and exposed
the flaw: a 9-line CI fix and a 600-line coordinator are both "1 PR" and are
nothing like the same review. Counting PRs measures the wrong thing, so the cap
was simultaneously too tight for trivia and far too loose for hard changes.

## Decision

Budget **review weight**, not PRs. Each bead declares one:

| weight | means | example |
|--------|-------|---------|
| 1 | mechanical, obvious, low blast radius | the bd install fix, a roster path |
| 2 | ordinary feature or fix; some judgement | hydrate, the bypass detector |
| 3 | new subsystem, or hard to reverse | the coordinator, the lease protocol |

A night's budget is **8 weight**, not 8 PRs — so eight trivia or two hard
changes, whichever the queue holds. Unweighted beads count as 2.

Two shaping rules the count never expressed:

- **One PR is one idea that can stand alone and be reverted alone.** Group
  changes that only make sense together; never bundle unrelated ones to save a
  PR, and never split one idea to look smaller.
- **Stack dependent work rather than merging or splitting it.** A stacked PR is
  reviewed against its parent, so each stays small; the stack merges bottom-up.

Open PRs count against the budget. Allocation is *pressure-sensitive*: a full
review queue reduces what tonight may start, which is the constraint enforcing
itself rather than being remembered.

## Consequences

The budget now tracks the thing that actually costs — judgement — and small
safe work stops being rationed. Harder: weight is a guess made before the work
exists, so it will sometimes be wrong; the retro compares declared weight to
actual review time and recalibrates. Deliberately no automatic weighting from
diff size: lines changed is not significance, and a mechanical 500-line rename
is easier to review than a 40-line change to the lease protocol.
