# 0015. Builders do not write under .claude/; the outbox carries the diff

Date: 2026-08-27
Status: accepted
Amends: 0003, whose MAY NOT list was silent on this while the harness enforced it

## Context

The harness refuses writes under `.claude/` in unattended sessions, and no
grant is reachable there. Two healthy runs died on that wall (fw-bu2, fw-ajl),
and since every flywheel skill lives under `.claude/skills/`, the whole class
of beads that improve how agents work was unbuildable by agents. But the block
itself is right: the skill files are where ADR 0003's constraints are restated
to the agent that must obey them. An agent that can edit them can edit its own
leash, and the next run would read the edit and believe it — the same shape as
merge authority, which was declined deliberately.

## Decision

The block stays and is now written down: builders MAY NOT write under
`.claude/`, by any path. A deliverable there travels as content, not authority:
the builder commits the full intended file at `.flywheel/outbox/<path>`, which
maps to `.claude/<path>` — the prefix is implied because the harness refuses
any path *containing* `.claude/`, a mirror of it included (measured while
building this). The human runs `tools/flywheel/outbox.sh apply` on the PR
branch, which moves the files into place and stages them, so the same PR shows
the real diff before merge. `apply` refuses to run under a builder identity —
defence in depth per ADR 0003's amendment; the bound remains the human merge.

## Consequences

`.claude/` beads are buildable again: one extra human command per such PR, no
retyping, no lost runs. Harder: until applied, the PR shows a whole file rather
than a diff (`outbox.sh diff` closes that gap), and a deletion under `.claude/`
is still not expressible — a ceiling accepted until one is actually needed.
