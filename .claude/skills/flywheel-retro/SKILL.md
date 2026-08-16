---
name: flywheel-retro
description: Read the flywheel's own history — DORA and agent metrics, closed beads, the review ledger, bypass counts, incidents — and propose what to do next as a short retro plus a list of candidate beads. Use monthly, or when asked to "run the retro" or "what should I work on next". Proposes; the human approves.
---

# flywheel-retro

SPEC.md is candid that the monthly retro is "a calendar reminder, not
automation" — which, by the project's own rule, makes it a process that will
quietly stop happening. This automates the *reading* and the *proposing*. The
human keeps the approving, and that approval is the whole ritual.

## Read the evidence

Everything here is already committed somewhere. The job is to look at it
together, which is the thing that never happens otherwise.

- **`docs/dora.json` git history** — that file's history *is* the trend line.
  Compare the last snapshot against three months back, not just the latest.
- **`agent` block in the same file** — first-pass gate rate, rework,
  autonomous share. Is the loop getting better or just busier?
- **Closed beads and their close reasons** (`bd list --status closed`) — the
  close reasons in this project are unusually good; use them.
- **`.flywheel/review.jsonl`** — findings per PR, acceptance rate, and the
  false-positive rate. This is what says whether the review panel earns its cost.
- **`fleet bypasses`** — any gate over budget.
- **Incidents** — `incident`-labelled issues: what broke, how long, was it
  attributable to a release.
- **Stale beads** (`bd stale`) — work that has been "next" for two months is
  telling you something.

## Judge, don't summarise

A retro that lists numbers is a report. A retro says what the numbers *mean*
and what should change. Three questions:

1. **What got better, and can you tell why?** A metric that improved for
   unknown reasons will regress for unknown reasons.
2. **What is over its budget?** Every stage declares a standing cost (ADR
   0001). A gate bypassed more than twice, a stage costing more than it
   returns, a quarantined test past its deadline — these have a prescribed
   answer: delete it or automate it.
3. **What is the loop not seeing?** The metrics only measure what was
   instrumented. What went wrong this month that no number caught?

## Propose beads, don't create them

Output a short retro document — half a page, not a dashboard — and a list of
candidate beads with titles, one-line rationales, and priorities.

Then stop. The human picks. Proposing ten and having two accepted is a good
month; creating ten unasked is how a board becomes noise nobody trusts.

## The honesty clause

If the evidence says the flywheel is not paying for itself — gates ignored,
metrics flat, beads filed and never worked — **say that**. Recommend deleting
something. A retro that only ever proposes additions is a ratchet, and this
project's governing rule is that a documented-but-ignored process is worse
than none.

If three consecutive retros produce proposals nobody acts on, propose deleting
this skill.
