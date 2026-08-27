# 0003. Autonomy boundaries for unattended agents

Date: 2026-08-15
Status: accepted

## Context

Unattended agents are about to open PRs on their own. The boundaries need to
exist before the first run rather than after the first incident, and they
need to be enforced by permissions and hooks rather than by instructions in a
prompt — an agent that can merge will eventually merge.

## Decision

Unattended agents MAY create branches, commit, run gates, open PRs, comment
on beads, file beads, and reserve territory. They MAY NOT merge, tag,
release, deploy, touch production hosts, read or rotate secrets, force-push,
or write to the audit log except by appending.

Every run carries a hard token/dollar budget and a wall-clock cap; exceeding
either aborts and escalates. An agent that cannot make the gate green closes
nothing: it leaves the bead open with a comment saying where it stopped.

## Amendment, 2026-08-17: this boundary is advisory, and says so now

An adversarial review found every deny rule reachable through an allowed
prefix. `Bash(git -C:*)` accepts `-c alias.x='!sh'`; `Bash(uv run:*)` accepts
`--no-project sh -c`; `git push` plus an unprotected `main` is merge authority
without touching the denied `gh pr merge`. Those four are now denied, but the
general point stands: **an allowlist of command prefixes is defence in depth,
not a sandbox.** A determined agent with shell access can reach most things.

What actually bounds an unattended builder here is the combination of a
worktree, a reviewed PR, an unprivileged user, and a human merge — not this
file. Treating the allowlist as a security boundary would be the same mistake
as treating an unrun gate as a gate.

Three of five repos were also shipping a five-entry allowlist with no deny
block at all, because the working-tree file was never committed and builders
cut worktrees from HEAD. Committed now.

## Amendment, 2026-08-27: the merge denial moved to the spawn

A deny in `.claude/settings.json` applies to whoever reads it, and the
maintainer reads it too. So `Bash(gh pr merge:*)` there meant *nobody* could
merge, and removing it would have handed merge authority to every builder. A
deny also beats an allow from a more specific settings file — measured, not
assumed, so a session-scoped grant could not split them either.

That one rule now rides on the builder spawn as `--disallowed-tools`, and only
that one. The supervised session merges; an unattended builder cannot. It is
also the stronger place for it: a builder can edit `.claude/settings.json`
inside its own worktree, and cannot edit the flags it was launched with.

Everything else ADR 0003 forbids stays in the shared file, because neither
party should be tagging, releasing, force-pushing or reading secrets.

The consequence below is amended: the human is no longer the merge authority by
permission, only by convention and review. What still bounds an unattended
builder is the worktree, the reviewed PR, the unprivileged user, and now a
denial it cannot reach.

## Consequences

The human stays the merge authority, so the worst unattended outcome is a bad
PR rather than a bad main. Failure is legible instead of silent. Harder:
"could not finish" runs cost tokens and produce nothing, and the abort paths
need testing deliberately — a budget cap that has never fired is a budget cap
that does not work.
