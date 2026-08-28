# 0017. OpenSpec is not adopted for the Intent stage

Date: 2026-08-28
Status: accepted

## Context

OpenSpec (`@fission-ai/openspec`, an npm global) generates `proposal.md`,
`design.md`, `tasks.md` and per-capability specs under `openspec/specs/`.
Each already has an owner here: the bead, its `--design` field plus an ADR,
the bead's children, and SPEC.md. Adopting it means two homes for intent or
migrating beads out of a role that leases, gates, weights and the coordinator
all read. It is priced, not banned (ADR 0001); measured on this repo at
`c106724` for fw-e6c:

- `--design` was used on **22 of 106** beads: 17 of 101 closed (13 bugs, 4
  tasks) and all 5 open. The one multi-session task, fw-cpd.5, carried its
  design across seven runs and the eighth built from it unchanged.
- **16 ADRs** exist, all since v2 opened on 2026-08-15: four in the opening
  commit, twelve alongside **64 merged PRs**. They are cited 99 times across
  the skills, tools and docs (ADR 0003 alone 31 times). No decision this
  month was re-derived for lack of one.
- **22 bugs** were closed and none traces to intent being unclear: they were
  gates that never ran, tests that could not fail, ledger arithmetic, and
  board state nobody reconciled. Each arrived as a bead with unambiguous
  acceptance criteria; each fix was a check, not a clarification. (Zero
  incidents were filed, but this repo has no Operate channel to file them,
  so that count says nothing either way.)

The standing cost would be a Node runtime and an unpinned global on every
builder host, in a repo with zero dependencies in either direction, for slash
commands that live under `.claude/` and so could only be delivered through
the outbox (ADR 0015).

## Decision

Not adopted. The two-week side-by-side trial the bead proposed is not run
either: it would be measuring a remedy for a gap that has cost nothing in the
month it could have.

## Consequences

The gap OpenSpec would have filled is named and **accepted as open**: there is
no durable statement of what a capability is *supposed* to do, separate from
the bead that changed it. SPEC.md is organised by stage and epic, not by
capability; closed beads are a changelog. The September retro re-measures
the same three numbers; a bug or a re-derivation that traces to unclear intent
is the trigger for a per-capability section in SPEC.md, tried before OpenSpec
because it costs zero dependencies.
