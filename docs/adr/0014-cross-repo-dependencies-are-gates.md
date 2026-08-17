# 0014. Cross-repo dependencies are gates, not comments

Date: 2026-08-16
Status: accepted

## Context

The flywheel's five repos depend on each other constantly: an instance cannot
adopt a workflow until the template ships it, and a template cannot claim
parity until the shared tool it invokes is released. Until now that ordering
lived in prose — a note in a bead saying "after the template lands" — which the
coordinator cannot read and an unattended builder will happily ignore.

`bd` already models this. Gates block an issue out of `bd ready` until a
condition resolves, and `gh:pr` gates resolve themselves when the PR merges.

## Decision

A dependency on work in another repo is expressed as a gate:

```
bd gate create --type=gh:pr --blocks <bead> --await-id=<pr> --reason="…"
bd gate check      # resolves any whose PR has merged
```

Gated beads do not appear in `bd ready`, so the coordinator never allocates
work whose prerequisite is unmerged. `bd gate check` runs in the fleet's cycle.

Prefer `gh:pr` over `human` gates: a human gate needs someone to remember, and
the whole point is to stop ordering living in memory.

## Consequences

Ordering is enforced rather than described, and an unattended builder cannot
start work whose prerequisite has not landed. Proven end to end: a bead gated
on PR #24 stayed out of `ready`, and `bd gate check` resolved it the moment the
PR merged.

Harder: a gate whose PR is closed rather than merged blocks forever until
someone resolves it by hand — the failure is loud (the bead never becomes
ready) rather than silent, which is the right direction, but it still wants a
staleness check.
