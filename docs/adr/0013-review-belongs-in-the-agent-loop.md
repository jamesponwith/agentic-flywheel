# 0013. AI review runs in the agent's loop, not in git's hook path

Date: 2026-08-16
Status: accepted
Amends: 0002 (template), which moved AI review from CI to the pre-push hook

## Context

The first genuine review run took **482 seconds**. The pre-commit gate beside
it takes 1.5. Eight minutes per push is far past the line the review skill sets
for itself — "cut to two lenses rather than letting it be bypassed" — and the
evidence arrived immediately: every push made after measuring it, including the
ones fixing the review itself, used `LEFTHOOK_EXCLUDE=ai-review`. Six bypasses
in an afternoon, by the person who wrote the rule.

ADR 0002 moved review out of CI for good reasons that still hold: no API keys
in CI, no per-PR token spend, and findings land before the PR exists. Those
arguments were about *where the cost falls*, not about git hooks specifically.

The mistake was attaching a multi-minute agent task to a synchronous git
operation a human is waiting on.

## Decision

Review moves from `pre-push` into the **agent's own loop**. `/flywheel-next`
already reviews its work before opening a PR (step 6b); that is where an
eight-minute review is affordable, because an agent session is already running
and nobody is watching a terminal block.

`pre-push` no longer runs it. `/flywheel-review` stays available on demand for
a human who wants it.

ADR 0002's reasoning survives intact — review is still local, still free of CI
secrets, still before the PR exists. Only the trigger changed.

## Consequences

Pushes are fast again, so the gate that remains is one nobody is tempted to
skip. Review still happens on every agent-authored change, which is most of
them. Harder: a **human** pushing by hand now gets no automatic review, which
is a real reduction in coverage and the honest cost of this decision — the
mitigation is that human-authored changes still face CI and a PR.
