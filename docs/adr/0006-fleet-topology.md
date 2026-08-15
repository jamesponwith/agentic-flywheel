# 0006. Fleet topology, roles, and the review-capacity ceiling

Date: 2026-08-15
Status: accepted

## Context

ADR 0001 priced fleets instead of banning them, which makes the sizing question
real. The tempting number to design against is what the machine can run
concurrently. That is the wrong number: unattended agents produce PRs, and a PR
nobody reviews properly is worse than no PR — it is an unreviewed merge waiting
to happen. The scarce resource is one human's review morning.

## Decision

**Roles.** coordinator (allocates work, never edits code), builder (bead → PR),
reviewer (one lens, never merges), operator (watches Operate signals, files
incidents), retro (monthly, proposes beads). Only the coordinator is stateful.

**The ceiling.** Assume one reviewable PR per 15 minutes of a 2-hour review
morning: **8 PRs/day**. Derived caps: **6 builder PRs per night** (headroom for
human-authored work), **3 concurrent builders**, **2 active repos per night**.

Caps are numbers so the retro can check them, not adjectives.

## Consequences

The fleet is sized to be reviewed, not to be busy, and the caps give the
coordinator a rule rather than a judgement. Raising throughput now requires
raising *review* throughput — better PR descriptions, tighter beads, more
trustworthy gates — which is the right pressure. Harder: the caps are a guess
until there is data; the retro re-derives them from the measured
merged-without-edits rate (fw-e7e.1), and they move only on evidence.
