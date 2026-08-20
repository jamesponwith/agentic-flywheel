# 0007. Agents speak four message types, two of which require an ack

Date: 2026-08-15
Status: accepted, but unimplemented — see the 2026-08 retro

## Context

Agents need to hand work to each other and to reach the human when stuck.
blackbird provides the transport — conversations, replies, independent read and
acknowledgement facts (ADR 0004) — but not what may be said. Open-ended chat
between agents is a token bonfire with no audit value, and worse, an agent with
no defined way to say "I am stuck" will instead do something confident and wrong.

## Decision

One conversation per bead. Four message types, nothing else:

| type | direction | ack |
|------|-----------|-----|
| `handoff` | builder → reviewer: branch, bead, what changed, what to look at | **required** |
| `finding` | reviewer → builder: one finding, severity, file:line, claim | optional |
| `escalate` | any agent → human: blocked, over budget, or genuinely uncertain | **required** |
| `status` | progress note | none |

An unacknowledged `handoff` older than its TTL escalates rather than stalling
silently. `escalate` is the important one: it is the only path by which an agent
admits it should stop.

## Retro amendment, 2026-08: this is aspirational, and now says so

`grep -rn "handoff" tools/` returns nothing. No builder has ever sent a
message, because no builder has ever handed work to a reviewer — the loop has
completed once, start to finish, by one agent. The four types below describe an
intention, not a behaviour.

Kept rather than deleted, because the reasoning is still the reasoning I would
want when a second role exists: `escalate` is the important one, since an agent
with no defined way to say "I am stuck" will do something confident and wrong
instead. But it is marked aspirational so nobody reads it as a description of
the system.

Per ADR 0001, if it still has zero implementation at the next retro it gets
deleted.

## Consequences

Agent traffic is auditable and cheap — four types are countable, and the
escalation rate becomes a metric of how well beads are specified. Harder: a
closed set will fit some future exchange badly, and the temptation will be to
smuggle content into `status`. Adding a fifth type is fine; it just needs to be
a decision rather than a drift.
