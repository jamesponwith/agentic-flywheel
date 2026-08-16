# 0008. Multi-session work gets a design note before code

Date: 2026-08-15
Status: accepted

## Context

ADRs capture decisions and SPEC.md captures scope, but nothing captures *shape*
— the types, the boundaries, the function signatures — before implementation
starts. With an agent doing the typing, shape is exactly where a human's
judgement is worth the most and costs the least: reviewing four signatures takes
a minute, reviewing the four hundred lines that follow from them takes an hour.

## Decision

Any bead expected to take more than one session gets a short design note in its
`--design` field before code: the key types, the public function signatures, and
the boundaries between them. Signatures and boundaries only — no prose, no
justification, no pseudocode. If it needs paragraphs it is a spec section or an
ADR instead.

The note is approved before implementation, and it is a convention rather than a
gate: nothing blocks on it.

## Consequences

The cheapest possible point to redirect an agent, and a bead whose shape is
agreed is one an unattended builder can take safely. Harder: it adds a
round-trip to work that is already understood, so it applies only above the
one-session threshold — and it should be judged honestly in the retro. If it
turns out to be ceremony that never changed an implementation, delete it.
