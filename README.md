# agentic-flywheel

The flywheel's own home: **Intent → Build → Validate → Release → Operate → Learn**,
rebuilt lean for a team of one plus agents.

This repo is the meta layer. It holds the spec, the writeup, the flywheel-level
ADRs, and the tooling that runs *across* repos — the coordinator, the guard, the
roster. Per-project tooling lives in the templates instead, and is never cloned
here (ADR 0005). If a thing runs inside one repo, it belongs in
`flywheel-template`; if it runs over five, it belongs here.

**Start with [SPEC.md](SPEC.md)** for what is being built and why, and
**[WRITEUP.md](WRITEUP.md)** for the narrative version — the Capital One pattern,
what transferred to a solo scale, and what didn't.

## The six stages, and where each one lives

| Stage | Owned by | In this repo |
|-------|----------|--------------|
| Intent | SPEC.md, ADRs, beads (`bd`) | `SPEC.md`, `docs/adr/`, `.beads/`, `/flywheel-plan`, `tools/flywheel/bead-lint.sh` |
| Build | Claude Code under ponytail discipline, lefthook | `CLAUDE.md`, `lefthook.yml`, `tools/flywheel/install-hooks.sh` |
| Validate | PR gate + a three-lens review panel | `.github/workflows/pr.yml`, `/flywheel-review`, the findings ledger |
| Release | goreleaser, semver tags, SBOM + signing | in the templates — this repo ships no binary |
| Operate | SLOs, probes, machine-filed incidents | in the templates (`tools/watch`); the contract is ADR 0002 |
| Learn | DORA + agent-effectiveness metrics | `.github/workflows/learn.yml`, `docs/fleet.html`, `/flywheel-retro` |

Operate is the honest name for the gap between "released" and "learned". V1
ended at "binary on a host", which made change-failure rate and MTTR a measure
of how reliably a human remembered to label a GitHub issue.

## Layout

```
SPEC.md                     what v2 is, the epics, the success criteria
WRITEUP.md                  the essay — the DeepMind-visible artifact
CLAUDE.md / AGENTS.md       conventions agents load before writing code
docs/adr/                   0001–0013, ~20 lines each
docs/fleet.html             every flywheel repo's dora.json on one page
fleet/roster.json           the fleet's constitution: repos, agents, caps
tools/fleet/                the cross-repo coordinator (Go, stdlib only)
tools/flywheel/             guard.sh (kill switch + audit log), bead-lint, install-hooks
.claude/skills/             flywheel-next, -plan, -review, -retro
.flywheel/                  local agent state: STOP file, agent-log.jsonl
.github/workflows/          pr.yml (gate), learn.yml (weekly snapshot)
```

## The agent stack

Three layers, each owning its facts exclusively (ADR 0004). All three already
ran on this machine, which is the only reason the protocol epic fit the budget —
it is wiring, not new services.

| Layer | Owns | Provided by |
|-------|------|-------------|
| Work graph | issues, dependencies, claims, gates, merge-slot | `bd` (beads) |
| Coordination | identity, file reservations with fencing tokens, acked messages, event journal | `blackbird` (systemd daemon, MCP) |
| Execution | worktrees, subagents, skills, scheduled runs | an agent CLI — a role, not a vendor (ADR 0010) |

`bd` claims the **work**; blackbird reserves the **files**. Both are required
before an agent edits anything. Agents fail closed: no reservation, no edit.

## `fleet` — the cross-repo coordinator

```
fleet claim <bead> -agent NAME [-ttl 45m]   take a bead under lease
fleet heartbeat <bead> -agent NAME          extend your own lease
fleet release <bead> -agent NAME            give it back
fleet reclaim                               sweep expired leases back to ready
fleet status                                who holds what, across the fleet
fleet allocate                              tonight's plan (prints, never spawns)
fleet run [-execute]                        allocate, then spawn builders
fleet hydrate                               make every repo's beads readable
fleet doctor [-fix -source PATH]            which stages each repo is missing
fleet bypasses [-since 30.days]             gates that got skipped, and by how much
```

```bash
go build -o /tmp/fleet ./tools/fleet   # -o is mandatory: the binary name
                                       # collides with the fleet/ config dir
```

Dry-run is the default rather than a special mode, so a plan can be printed,
diffed, and reviewed before anything runs. Caps come from **review capacity, not
compute** (ADR 0006): the roster budgets 8 units of review *weight* per night —
w:1 mechanical, w:2 ordinary, w:3 a new subsystem — because a 9-line CI fix and
a 600-line coordinator are both "1 PR" and are nothing like the same review
(ADR 0009). Every declined bead carries a reason; silent truncation would read
as "there was nothing to do", which is the one lie that would make the fleet
untrustworthy. See [tools/fleet/README.md](tools/fleet/README.md) for why
`hydrate` and leases exist.

## Skills

| Skill | Does |
|-------|------|
| `/flywheel-next` | one ready bead → worktree → gate → PR → report, then stop |
| `/flywheel-plan` | a SPEC section → an epic, children, and dependency edges (dry-runs first) |
| `/flywheel-review` | three independent lenses — correctness, security, simplicity — every finding recorded |
| `/flywheel-retro` | reads DORA, closed beads, the ledger, bypasses; proposes beads |

All four propose or build; none of them merge.

## Safety rails

Unattended agents MAY branch, commit, run gates, open PRs, comment on beads,
file beads, and reserve territory. They MAY NOT merge, tag, release, deploy,
touch production hosts, read or rotate secrets, force-push, or rewrite the audit
log (ADR 0003).

```bash
tools/flywheel/guard.sh check              # exit 0 runnable, 1 stopped
tools/flywheel/guard.sh stop "reason"      # this repo
tools/flywheel/guard.sh stop --fleet       # everything, everywhere
tools/flywheel/guard.sh resume [--fleet]
```

The kill switch is a file — `.flywheel/STOP`, or `~/.flywheel/STOP` fleet-wide,
which takes precedence. Deliberately dumb enough to be trustworthy, and not
committed: stopping the fleet must never require a push.

## Gates

```bash
tools/flywheel/install-hooks.sh   # once per clone
```

lefthook owns `.git/hooks` and beads runs as a declared step inside it. One
chain owns `core.hooksPath` (ADR 0012) — two hook systems both assuming
ownership of one path is how this repo's Build gate came to exist in config and
never once run. `install-hooks.sh` therefore ends by committing deliberately
unformatted code and asserting the gate rejects it: a gate you have not watched
fail is a gate you have not got.

Pre-commit is gofmt, vet, `go test -short`, and `bash -n` — about 1.5 seconds.
CI adds `-race`, the bead lint, and the conformance suite against a pinned real
`bd` binary:

```bash
go test -tags=conformance ./tools/fleet/    # ~75s, real bd databases
```

Conformance asserts the **refusals**, not the happy path: two agents cannot hold
one bead, a killed agent's work returns to the queue, a stale agent cannot
resurrect its lease, human work is never reclaimed, the kill switch halts
mid-cycle, caps are never exceeded.

AI review is deliberately *not* on pre-push. It ran for 482 seconds beside a
1.5-second commit gate and was bypassed six times in one afternoon by the person
who wrote the rule, so it moved into the agent's own loop (ADR 0013). Standing
rule underneath all of it: **if a gate gets bypassed twice, delete it or
automate it.**

## Where v2 stands (2026-08-16)

74 beads filed, 40 closed, across eight epics. A bead closes when its acceptance
criterion is *demonstrated*, not when the code exists — several open beads have
shipped artifacts sitting in this tree awaiting a live proof.

| Epic | Beads | Status |
|------|---|--------|
| `fw-wma` Operate | 5/6 | SLO contract, prober, auto-close, smoke+rollback all landed. Router adoption on the linux box is deferred on the box. |
| `fw-l8k` Validate v2 | 8/9 | Review panel, findings ledger, supply chain, security gate, deeper test rungs. Flaky-test quarantine open. |
| `fw-wb2` Protocol | 6/8 | Identity, territory, leases, message contract, conformance harness. Cross-repo gates and MCP territory conformance open. |
| `fw-oef` Fleet | 7/12 | Roster, hydrate, doctor, coordinator (`allocate`/`run`). Budget enforcement, fleet observability, chaos drill open. |
| `fw-cpd` Intent v2 | 4/7 | `/flywheel-plan`, bead lint in CI, interface-first, untracked JSONL export. Signal intake open. |
| `fw-e7e` Learn v2 | 4/6 | Agent-effectiveness metrics, bypass detector, fleet dashboard, `/flywheel-retro`. **Cost accounting is the open one that matters.** |
| `fw-lb8` Autonomous loop | 1/6 | `/flywheel-next` ran end-to-end against a real bead once. Nightly schedule, parallel builders, digest still open. |
| `fw-fsa` Distribution | 2/8 | `fleet doctor` and the v2 writeup. Both templates still need syncing to v2, and the router blog post is unpublished. |

The two claims v2 exists to make, and where each one is:

- **Seven consecutive unattended nights of green reviewable PRs**, with a budget
  cap and a kill switch verified by tripping it mid-run. One live night so far;
  the switch has been tripped mid-*allocation* but not mid-*edit*.
- **Public numbers on what agentic development costs.** The dashboard grew an
  agent panel, but tokens-and-dollars-per-merged-PR (`fw-e7e.2`) is still open —
  which is the number nobody publishes and the reason for wanting it.

```bash
bd ready          # what is workable now
bd show <id>      # why it exists
```

## The rest of the fleet

| Repo | Role |
|------|------|
| [flywheel-template](https://github.com/jamesponwith/flywheel-template) | the Go template — all six stages, plus `tools/dora` and `tools/watch` |
| [flywheel-template-py](https://github.com/jamesponwith/flywheel-template-py) | the Python template — uv/ruff/pytest, reuses the Go collector |
| [llm-resiliency-router](https://github.com/jamesponwith/llm-resiliency-router) | pilot instance; released v0.1.0 through the pipeline |
| [brax-tennis-rl](https://github.com/jamesponwith/brax-tennis-rl) | pilot instance; bootstrapped from the Python template in 2m55s |

Dashboards: [DORA](https://jamesponwith.github.io/dora.html) ·
[fleet](docs/fleet.html)

## Conventions

Read [CLAUDE.md](CLAUDE.md) before writing code here. The short version: no bead
no build; ponytail active (the laziest solution that works, stdlib first); no
new dependency without an ADR; table-driven tests that cover the refusals; one
PR is one idea that can stand alone and be reverted alone; never bypass a
failing hook.
