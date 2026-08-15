# 0004. Agents stand on beads, blackbird, and Claude Code

Date: 2026-08-15
Status: accepted

## Context

A fleet needs agent identity, file-level mutual exclusion, durable messaging
with acknowledgement, and a tamper-evident audit journal. All four already
exist locally: blackbird runs as a systemd user daemon, is MCP-connected, and
ships repo-scoped registration, reservations with fencing tokens and expiry,
conversations with To/Cc/Bcc and independent read/ack facts, and an
authenticated event journal. It also ships a `bd` compatibility probe. The
standing YAGNI discipline makes building any of it again indefensible.

## Decision

Three layers, each owning its facts exclusively:

- **beads** — the work graph: issues, dependencies, claims, gates, merge-slot.
- **blackbird** — coordination: agent identity, file reservations, messages,
  acknowledgements, event journal.
- **Claude Code** — execution: worktrees, subagents, skills, scheduled runs.

Where the two trackers could overlap: `bd` claims the *work*, blackbird
reserves the *files*. Both are required before editing.

## Consequences

The protocol epic is mostly wiring and convention, which is the only reason
it fits the ADR 0001 budget. Harder: the flywheel now has a runtime
dependency on a third-party daemon being up, so an agent must fail closed —
no reservation, no edit — and blackbird's own availability becomes an Operate
concern.
