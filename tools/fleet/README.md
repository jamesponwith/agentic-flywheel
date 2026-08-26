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
fleet bypasses                              gates that got skipped, and by how much
fleet adr-drift                             decisions this branch made without recording
fleet doctor                                which stages each repo is missing
fleet cost                                  what the fleet spent, per repo
fleet review-rate                           what the AI reviewer actually caught
fleet run                                   allocate, then spawn builders (needs -execute)
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

## Why adr-drift is advisory and generous

```
fleet adr-drift [-dir .] [-base origin/main] [-json]
```

Three signals, all from git alone: a `require` line naming a module the base did
not have, an exported top-level declaration that changed shape or disappeared,
and a changed file sitting under a path an existing ADR names. Any of them
*might* be a decision nobody wrote down.

One suppression rule covers all three: a branch that touches `docs/adr/`, or
says "ADR NNNN" in a commit message or an added line, is silent. That is
deliberately blunt — it cannot tell whether the ADR is about the thing flagged.
The check's own acceptance criterion is that it gets deleted if it is wrong more
than half the time, so every ambiguity resolves toward saying nothing. A missed
flag costs one un-recorded decision; a wrong one costs the whole check, because
a noisy advisory warning is ignored within a week.

Its path list maintains itself: a backticked token in an ADR counts only if it
resolves to a real file or directory at HEAD, so paths that move, get deleted, or
were never in this repo drop out on their own.

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

## Why review-rate refuses to divide

```
fleet review-rate [-roster PATH] [-repo NAME] [-json]
```

WRITEUP.md names two things the flywheel could not tell you about itself. One
was whether the AI reviewer catches more than it costs — and half that answer
was already on disk, unread. Every `guard.sh finding` appends a line to
`.flywheel/review.jsonl` carrying a disposition; nothing but `wc -l` had ever
looked at it.

Three refusals do the work, and all three are about declining to manufacture a
number that flatters the reviewer:

- **`ignored` is not `rejected`.** A finding nobody judged is not a finding
  judged wrong. Precision divides accepted by *accepted + rejected* only;
  ignored findings never enter the denominator. Collapsing the two would let
  you pick whichever denominator gave the prettier answer.
- **A handful of findings is not a rate.** Below `minSample` judged findings
  the command says so and prints no percentage at all, the same way `cost.go`
  distinguishes zero spend from unmeasured spend. An unmeasurable rate that
  renders as `0%` reads as "the reviewer is always wrong", which the ledger
  does not say.
- **A reviewer agreeing with itself is not precision.** ADR 0013 runs the
  panel inside the builder's own loop, so almost every finding is raised and
  dispositioned by one agent in one run. That figure is real and it is not what
  "precision" means to anyone reading a dashboard.

A disposition outside the three known values is counted in the total and in no
bucket, and the gap is printed rather than folded into `ignored` — that would
be the same error one layer down. A missing ledger is "no reviews recorded"; a
ledger that exists and cannot be read is an **error**, because that is a broken
thing pretending to be an empty one.

### Who judged it, not just who raised it

Findings are counted in three classes, and only one of them is precision:

| class | the ledger line says | rendered as |
| --- | --- | --- |
| independent | a `judged_by` naming someone other than `agent` | **precision** |
| self-judged | `judged_by` equal to `agent` | self-agreement |
| judge unrecorded | no `judged_by`, or no `agent` | agreement, unlabelled |

`guard.sh finding` stamps `agent` — who raised it — and refuses a caller-supplied
one, for the reason it refuses a caller-supplied `agent` on `log`. The judge is
passed:

```
tools/flywheel/guard.sh finding lens=correctness file=a.go line=1 \
  severity=high claim="..." disposition=accepted judged_by=self
```

`judged_by=self` expands to the running agent's name at write time. It exists
because an agent cannot read its own name — procfs is a hard sandbox refusal and
`env`/`printenv` are outside the allowlist (fw-eoi, fw-k8f) — so interpolating
`$FLYWHEEL_AGENT` into the call is not something a builder can write. Storing
the expanded name keeps the ledger self-describing and every reader comparing
two names rather than learning a sentinel.

Each class clears `minSample` on its own evidence: a reviewer that agreed with
itself two hundred times does not thereby unlock a precision computed from three
independent judgements. **An absent `judged_by` is counted as neither** — not as
self, not as independent. Every line written before this distinction existed has
no attribution at all, and counting those as independent by default is exactly
the flattering number this command exists to refuse (fw-bu2).

No JSON key is called `precision`. A dashboard that wants one reads
`.independent.agreement`, which is the discrimination made explicit. The
top-level key was removed rather than renamed so a consumer of the old shape
fails loudly instead of quietly rescaling a number whose meaning changed.

Today it reports the honest answer: 17 findings, 11 judged, and no precision —
because no finding in the fleet has yet been judged by anyone but the agent that
raised it. The number arrives when the ledger does.

## Conformance

```
go test -tags=conformance ./tools/fleet/
```

Runs against a real `bd` database in scratch repos, asserting the failure
behaviour: two agents cannot hold one bead, a killed agent's work returns to the
queue with a note, a stale agent cannot resurrect its lease, human work is never
reclaimed, the kill switch halts mid-cycle, and caps are never exceeded.

It also round-trips the review ledger through the real `guard.sh finding`
rather than a fixture. This repo's own ledger records why: PR 43's cost parser
was tested against a fixture authored beside it, so the test proved the parser
matched the fixture rather than the writer — and the parser turned out to
depend on a key order the runner never guaranteed.

Territory conformance (blackbird file reservations) is **not** here — the
reservation API is MCP-only, so it is agent-driven. See `fw-wb2.7`.
