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
- `go build ./tools/fleet/` drops a binary that collides with the `fleet/`
  config directory. Always build with `-o`.

## Agents

- Unattended agents are bound by ADR 0003: branch, commit, gate, PR, comment,
  reserve — never merge, tag, deploy, or read secrets.
- Check `tools/flywheel/guard.sh check` before acting; it is the kill switch.
- Claim work through `fleet claim`, not bare `bd update --claim` — a claim
  without a lease starves the queue if you die.
- Anything expected to take more than one session gets a design note in the
  bead's `--design` field first: signatures and boundaries, no prose (ADR 0008).
- Agent-to-agent messages are `handoff`, `finding`, `escalate`, `status` and
  nothing else; `handoff` and `escalate` require an ack (ADR 0007). If you are
  stuck, `escalate` — do not guess confidently.
- Reserve territory in blackbird before editing. No reservation, no edit.

## Commit protocol

- Small commits, imperative subject, reference the bead: `fw-oef.3: cap builders`.
- Never bypass a failing hook. If a gate gets skipped twice, delete it or
  automate it.
