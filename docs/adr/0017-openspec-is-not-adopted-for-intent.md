# 0017. OpenSpec is not adopted for the Intent stage

Date: 2026-08-28
Status: accepted

## Context

OpenSpec (`@fission-ai/openspec`, an npm global) generates `proposal.md`,
`design.md`, `tasks.md` and per-capability specs under `openspec/specs/`.
Each already has an owner here: the bead, its `--design` field plus an ADR,
the bead's children, and SPEC.md. Adopting it means two homes for intent or
migrating beads out of a role that leases, gates, weights and the coordinator
all read. Measured on this repo, per the ADR 0001 cost test (fw-e6c):

- `--design` was used on **22 of 106** beads (17 of 101 closed, all 5 open).
  15 of the 17 closed ones are bugs, where it held the shape of the fix; the
  one multi-session task (fw-cpd.5) carried its design across seven runs and
  the eighth built from it unchanged. ADR 0008 is doing what it claims.
- **12 ADRs** were written against **62 merged PRs** since 2026-08-15, and
  they are cited 99 times across the skills, tools and docs (ADR 0003 alone
  31 times). `fleet adr-drift` has run on every PR since #27; `go.mod` has
  one commit in its history, so the new-dependency signal has never had
  anything to fire on. No decision this month was re-derived for lack of one.
- **0** incidents were filed and **22** bugs were closed. None traces to
  intent being unclear: they are gates that never ran, tests that could not
  fail, ledger arithmetic, identity the sandbox could not reach, and board
  state nobody reconciled. Every one arrived as a bead whose acceptance
  criteria were unambiguous; every fix was a check, not a clarification.

The costs are real: a Node runtime in a repo with zero dependencies in either
direction, installed in every builder worktree, for slash commands an
unattended builder cannot invoke (ADR 0015 keeps builders out of `.claude/`).

## Decision

Not adopted. The two-week side-by-side trial the bead proposed is not run
either: it would be measuring a remedy for a gap that has cost nothing in the
month it could have. The trial stands ready if a re-derivation ever shows up.

## Consequences

The gap OpenSpec would have filled is named and **accepted as open**: there is
no durable statement of what a capability is *supposed* to do, separate from
the bead that changed it. SPEC.md is 234 lines organised by stage and epic,
not by capability; closed beads are a changelog. The zero-dependency remedy is
a per-capability section in SPEC.md, written at the first re-derivation that
`adr-drift` or a retro catches — not before. The September retro re-measures
the same three numbers; if a bug or a re-derivation traces to unclear intent,
that is the trigger, and the null hypothesis is tried first.
