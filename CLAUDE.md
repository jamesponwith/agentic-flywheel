# CLAUDE.md

The flywheel's own home: Intent → Build → Validate → Release → Operate → Learn.
This repo holds the spec, the writeup, the flywheel-level ADRs, and the fleet
tooling that runs *across* repos. Per-project tooling lives in the templates
(ADR 0005).

## Before writing code (Intent)

- Read SPEC.md and the open `bd` issue. No bead, no build — create one first.
- Decisions that would take >5 minutes to re-derive get an ADR in `docs/adr/`,
  ~20 lines, copy `template.md`. The v2 decisions are 0001–0006.

## Conventions (Build)

- Ponytail active: the laziest solution that works. stdlib first.
- No new dependency without an ADR. `tools/fleet` and `tools/watch` have none.
- Table-driven tests. Test the refusals, not just the happy path — every claim
  the fleet makes is a claim about behaviour under failure.
- Deliberate shortcuts get a `// ponytail:` comment naming the ceiling and the
  upgrade path.
- `go build ./...` works. The roster lives in `.flywheel/roster.json`; it used
  to sit in `fleet/`, where the binary `go build` produces collided with the
  directory (fw-oef.10). A papercut that needs a documented workaround is one
  that trips every newcomer, so it was removed rather than documented around.

## Agents

- Unattended agents are bound by ADR 0003: branch, commit, gate, PR, comment,
  reserve — never merge, tag, deploy, or read secrets.
- Check `tools/flywheel/guard.sh check` before acting; it is the kill switch.
- Claim work through `fleet claim`, not bare `bd update --claim` — a claim
  without a lease starves the queue if you die.
- Anything expected to take more than one session gets a design note in the
  bead's `--design` field first: signatures and boundaries, no prose (ADR 0008).
- Beads carry a review weight — `w:1` mechanical, `w:2` ordinary, `w:3` a new
  subsystem or hard to reverse. The coordinator budgets weight, not PR count
  (ADR 0009); unweighted beads cost 2.
- One PR is one idea that can stand alone and be reverted alone. Stack
  dependent work against its parent branch rather than bundling or splitting it.
- Agent-to-agent messages are `handoff`, `finding`, `escalate`, `status` and
  nothing else; `handoff` and `escalate` require an ack (ADR 0007). If you are
  stuck, `escalate` — do not guess confidently.
- Reserve territory in blackbird before editing. No reservation, no edit.
- Builders do not write under `.claude/` (ADR 0015). A deliverable there is
  committed as `.flywheel/outbox/<path>` (maps to `.claude/<path>`); the human
  runs `tools/flywheel/outbox.sh apply` on the PR branch before merging.

## Commit protocol

- Small commits, imperative subject, reference the bead: `fw-oef.3: cap builders`.
- Never bypass a failing hook. If a gate gets skipped twice, delete it or
  automate it.

## Two process decisions

**`--no-verify` is allowed only during a replay** — a rebase, merge, or
cherry-pick, where the tree is transiently inconsistent and a commit mid-replay
may not build even though neither endpoint is broken. The pre-commit hook's
`replay-guard` detects that state and records it, so the skip is measured
rather than silent. Outside a replay, `--no-verify` is a bypass and the rule
stands: never (fw-e7e.6).

**Beads bookkeeping rides along with the work.** A `beads:` commit goes on the
feature branch and through the PR, not straight to `main`. The alternative —
direct pushes for board state — put fifteen commits past the Validate gate
before anyone noticed, and the bypass detector deliberately excludes
bookkeeping subjects so it could never have flagged them (fw-cpd.6).
