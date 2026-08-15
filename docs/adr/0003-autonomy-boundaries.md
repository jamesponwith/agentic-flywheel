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

## Consequences

The human stays the merge authority, so the worst unattended outcome is a bad
PR rather than a bad main. Failure is legible instead of silent. Harder:
"could not finish" runs cost tokens and produce nothing, and the abort paths
need testing deliberately — a budget cap that has never fired is a budget cap
that does not work.
