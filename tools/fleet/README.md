# tools/fleet

Cross-repo agent coordination (ADR 0005 — fleet tooling lives here and is never
cloned into a project).

```
fleet claim <bead> -agent NAME [-ttl 45m]   take a bead under lease
fleet heartbeat <bead> -agent NAME          extend your own lease
fleet release <bead> -agent NAME            give it back
fleet reclaim                               sweep expired leases back to ready
fleet status                                who holds what, across the fleet
fleet allocate                              tonight's plan (prints, never spawns)
fleet hydrate                               make every repo's beads readable
```

## Why hydrate exists

The first real `fleet allocate` across all five repos declined
llm-resiliency-router with "no beads database found". Its `.beads/issues.jsonl`
is committed but the Dolt database is gitignored, so a freshly cloned instance
has beads on disk that `bd` cannot read — and the coordinator sees an empty
queue where there is real work.

Upstream guidance is to sync with `bd dolt pull` and avoid `bd import` in normal
operation. That is right, and it does not apply here: the remotes carry no
`refs/dolt/*` at all, so the committed JSONL is the only durable copy. This is
the bootstrap-a-missing-database case, and it runs once per clone.

`hydrate` is idempotent, skips paused repos, never touches an existing database,
and verifies the repo is *actually* readable afterwards rather than trusting
that the commands ran.

It also checks whether it **changed the repository**, which is not the same
question as whether it left the tree dirty. `bd init` rewrites `CLAUDE.md`,
`AGENTS.md` and `.claude/settings.json`, and beads installs git hooks that
*commit* those changes — so a dirty-tree check finds nothing and reports
success. That happened twice for real (`fw-oef.12`): once the uninvited commit
reverted a merged PR, and once it contaminated an open PR that then passed CI,
because nothing was broken — it simply was not what the PR claimed to be.

hydrate now records HEAD before and after. A commit it did not ask for is
reported as `mutated`, with the exact range to inspect, and exits non-zero.

## Why leases

`bd update --claim` is atomic but has no liveness: an agent that dies mid-bead
leaves it claimed forever and the queue starves. `bd stale` is day-granularity —
far too coarse for a nightly run. A lease is a claim with an expiry the holder
must keep renewing, stored in the bead's own metadata so it survives the machine
that took it.

The rules that matter are all refusals:

- claiming a bead under someone else's live lease is refused
- heartbeating someone else's lease is refused, so a reclaimed agent that wakes
  up cannot resurrect its claim and edit alongside its replacement
- an in-progress bead with **no** lease is never reclaimed — that is a human's
  work, and taking it would be worse than the starvation being prevented

## Why the coordinator only plans

`allocate` returns a plan and spawns nothing. Dry-run is the default rather than
a special mode, so a plan can be printed, diffed, and reviewed before anything
runs. Ties break by bead id, so the same queue always produces the same plan.

Caps come from ADR 0006 and derive from review capacity, not compute. Every
declined bead carries a reason: silent truncation would read as "there was
nothing to do", which is the one lie that would make the fleet untrustworthy.

The kill switch (`tools/flywheel/guard.sh`) outranks everything and halts the
whole cycle — half a fleet is a worse state than a stopped one.

## Conformance

```
go test -tags=conformance ./tools/fleet/
```

Runs against a real `bd` database in scratch repos, asserting the failure
behaviour: two agents cannot hold one bead, a killed agent's work returns to the
queue with a note, a stale agent cannot resurrect its lease, human work is never
reclaimed, the kill switch halts mid-cycle, and caps are never exceeded.

Territory conformance (blackbird file reservations) is **not** here — the
reservation API is MCP-only, so it is agent-driven. See `fw-wb2.7`.
