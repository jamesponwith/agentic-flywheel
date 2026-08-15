# 0005. Per-project tooling ships in the template; fleet tooling stays here

Date: 2026-08-15
Status: accepted

## Context

`tools/dora` lives in flywheel-template and the Python template already runs it
with `go run github.com/jamesponwith/flywheel-template/tools/dora@latest` — one
canonical copy, no port. V2 adds two kinds of tooling: things that run *inside*
one repo (the SLO prober) and things that run *across* repos (the guard, the
fleet coordinator, the roster). They cannot share a home: a coordinator cloned
into every project is a coordinator per project, which is not a fleet.

## Decision

Split by scope, not by language:

- **flywheel-template/tools/** — runs inside a single repo (`dora`, `watch`).
  Instances invoke it via `go run …@latest`; nothing is copied.
- **agentic-flywheel/tools/** — runs across repos (`flywheel/guard.sh`,
  the coordinator, the roster). Never cloned into a project.

## Consequences

Shared code has exactly one copy, so tooling cannot drift between instances —
`flywheel doctor` only has to reconcile config and workflows, not code. The
`@latest` pin means a bad push to the template reaches every instance's next
scheduled run, so template releases get tags and instances pin to them once
that bites. Fleet tooling has no consumer but this repo, so it never needs to
be language-portable.
